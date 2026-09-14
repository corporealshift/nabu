package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/corporealshift/nabu/daemon/module"
)

// File and size limits. The index rides on every request until the next
// compaction, so its cost is standing rather than one-off.
const (
	// SkillFile is the marker that makes a directory a skill.
	SkillFile = "SKILL.md"
	// maxDescription is how much of a description reaches the index.
	maxDescription = 200
	// maxIndexBytes caps the whole index block.
	maxIndexBytes = 8 << 10
	// maxBodyBytes caps what skill.load returns in one call.
	maxBodyBytes = 32 << 10
	// maxDepth bounds recursion, so a symlink loop or a pathological tree
	// cannot walk forever.
	maxDepth = 8
)

// Skill is one discovered skill. The body is read on demand, never held.
type Skill struct {
	Name        string
	Description string
	// Path is the absolute path to the skill's SKILL.md.
	Path string
}

// Module discovers skills and offers them to the model: an index as a prefix
// context block, and a tool to read one body on demand.
type Module struct {
	// Paths overrides the default search paths. Set from config.
	Paths []string

	skills []Skill
	log    logger
}

// logger is the slice of the host we need, so tests need no host at all.
type logger interface {
	Warn(msg string, args ...any)
}

// discardLog is used when no host is supplied.
type discardLog struct{}

func (discardLog) Warn(string, ...any) {}

// Name implements module.Module.
func (m *Module) Name() string { return "skills" }

// Init implements module.Module. Discovery runs once, here: the search paths
// are global, so every session gets the same index and it can be computed a
// single time. A daemon therefore outlives edits to the skill directories.
func (m *Module) Init(h module.Host, cfg module.Config) error {
	m.log = discardLog{}
	if h != nil && h.Log() != nil {
		m.log = h.Log()
	}

	paths, err := pathsFromConfig(cfg)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		paths = m.defaultPaths(h)
	}
	m.Paths = paths

	m.skills = discover(paths, m.log)
	return nil
}

// defaultPaths are the global skill directories: Claude Code's, so an existing
// library works unchanged, and nabu's own under the module data directory.
func (m *Module) defaultPaths(h module.Host) []string {
	var out []string
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".claude", "skills"))
	}
	if h != nil {
		if dir, err := h.DataDir("skills"); err == nil {
			out = append(out, dir)
		}
	}
	return out
}

// pathsFromConfig reads the "paths" list. When set it replaces the defaults
// entirely, so a user can point nabu somewhere else without inheriting them.
func pathsFromConfig(cfg module.Config) ([]string, error) {
	raw, ok := cfg["paths"]
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("skills: paths must be a list, got %T", raw)
	}
	var out []string
	for i, entry := range list {
		s, ok := entry.(string)
		if !ok {
			return nil, fmt.Errorf("skills: path %d must be a string, got %T", i, entry)
		}
		out = append(out, expandHome(s))
	}
	return out, nil
}

// expandHome resolves a leading ~ so a configured path can be written the way
// a person would type it.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, `~\`) {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimLeft(p[1:], `/\`))
}

// Skills returns what was discovered, for tests and for callers that want the
// list rather than the rendered index.
func (m *Module) Skills() []Skill { return m.skills }

// discover walks each directory for skills. A directory is a skill when it
// holds a SKILL.md; its subdirectories are then not searched, so a skill's own
// examples cannot register themselves. Malformed skills are skipped with a
// warning: one bad skill on disk must not cost the others.
func discover(dirs []string, log logger) []Skill {
	if log == nil {
		log = discardLog{}
	}
	byName := map[string]Skill{}
	var order []string
	seenDir := map[string]bool{}

	for _, dir := range dirs {
		walk(dir, 0, seenDir, func(s Skill) {
			if _, clash := byName[s.Name]; clash {
				log.Warn("skills: duplicate skill name, keeping the first found",
					"name", s.Name, "ignored", s.Path)
				return
			}
			byName[s.Name] = s
			order = append(order, s.Name)
		}, log)
	}

	out := make([]Skill, 0, len(order))
	for _, n := range order {
		out = append(out, byName[n])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// walk recurses, following symlinks. The standard walker does not follow them,
// and entries under ~/.claude/skills commonly are symlinks, so skipping them
// would silently lose skills. A visited set keeps a symlink loop finite.
func walk(dir string, depth int, seen map[string]bool, found func(Skill), log logger) {
	if depth > maxDepth {
		return
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return // missing or unreadable directories are simply not searched
	}
	if seen[real] {
		return
	}
	seen[real] = true

	// A directory holding SKILL.md is a skill, and is not descended into.
	skillPath := filepath.Join(real, SkillFile)
	if info, err := os.Stat(skillPath); err == nil && !info.IsDir() {
		s, err := parseSkill(skillPath)
		if err != nil {
			log.Warn("skills: ignoring a skill", "path", skillPath, "error", err)
			return
		}
		found(s)
		return
	}

	entries, err := os.ReadDir(real)
	if err != nil {
		log.Warn("skills: cannot read directory", "path", real, "error", err)
		return
	}
	for _, e := range entries {
		child := filepath.Join(real, e.Name())
		if e.IsDir() {
			walk(child, depth+1, seen, found, log)
			continue
		}
		if e.Type().IsRegular() {
			continue // a plain file is never a skill directory
		}
		// Anything else might still be a directory once followed. Do NOT test
		// for fs.ModeSymlink here: on Windows, Go reports a link created by
		// Git Bash as ModeIrregular, because its reparse tag is not one Go
		// classifies as a symlink. Checking the symlink bit silently skipped a
		// real skill in ~/.claude/skills. Stat follows whatever it is.
		if info, err := os.Stat(child); err == nil && info.IsDir() {
			walk(child, depth+1, seen, found, log)
		}
	}
}

// parseSkill reads a SKILL.md's frontmatter. Only name and description are
// used; any other key is ignored.
func parseSkill(path string) (Skill, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	front, ok := frontmatter(string(raw))
	if !ok {
		return Skill{}, fmt.Errorf("no frontmatter")
	}
	name := front["name"]
	if name == "" {
		return Skill{}, fmt.Errorf("frontmatter has no name")
	}
	return Skill{Name: name, Description: front["description"], Path: path}, nil
}

// frontmatter extracts the leading --- delimited block as key/value pairs.
// It is parsed line by line rather than with a YAML library, which keeps the
// dependency count at zero; the frontmatter nabu reads is flat.
func frontmatter(content string) (map[string]string, bool) {
	content = strings.TrimLeft(content, "\ufeff \t\r\n")
	if !strings.HasPrefix(content, "---") {
		return nil, false
	}
	rest := content[3:]
	if i := strings.IndexAny(rest, "\r\n"); i >= 0 {
		rest = rest[i+1:]
	} else {
		return nil, false
	}
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, false
	}

	out := map[string]string{}
	for _, line := range strings.Split(rest[:end], "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key != "" {
			out[key] = value
		}
	}
	return out, true
}

// SessionStart implements module.SessionStarter: the index goes in front of
// the model as a prefix block.
func (m *Module) SessionStart(context.Context, module.Session) ([]module.ContextBlock, error) {
	return m.indexBlocks(), nil
}

// AfterCompaction implements module.CompactionHook. A prefix block is fixed
// between compactions, so a summarize retires the index and it has to be put
// back or the model forgets which skills exist.
func (m *Module) AfterCompaction(context.Context, module.Session) ([]module.ContextBlock, error) {
	return m.indexBlocks(), nil
}

// BeforeCompaction implements module.CompactionHook. Nothing needs preserving
// in the summary: the index is re-injected afterwards instead.
func (m *Module) BeforeCompaction(context.Context, module.Session, module.Range) []string {
	return nil
}

// indexBlocks is the index, or nothing at all when no skills were found. A
// block announcing that there are no skills is context spent to say nothing.
func (m *Module) indexBlocks() []module.ContextBlock {
	if len(m.skills) == 0 {
		return nil
	}
	return []module.ContextBlock{{Slot: "prefix", Content: FormatIndex(m.skills)}}
}

// FormatIndex renders the skill index. Descriptions are shortened before any
// skill is dropped, because a name alone is still useful: the model can load
// it. If names alone will not fit, the block says how many were omitted, so a
// truncated list is never mistaken for the whole set.
func FormatIndex(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}
	const header = "## Skills\n\nThe following skills are available. " +
		"To read one, call `skill.load` with its name.\n\n"

	for _, limit := range []int{maxDescription, 80, 0} {
		body := renderLines(skills, limit)
		if len(header)+len(body) <= maxIndexBytes {
			return header + body
		}
	}

	// Even bare names overflow: keep as many as fit and say so.
	var b strings.Builder
	b.WriteString(header)
	kept := 0
	for _, s := range skills {
		line := "- `" + s.Name + "`\n"
		if b.Len()+len(line) > maxIndexBytes-64 {
			break
		}
		b.WriteString(line)
		kept++
	}
	if kept < len(skills) {
		fmt.Fprintf(&b, "\n(%d more skills not listed; the list is incomplete.)\n",
			len(skills)-kept)
	}
	return b.String()
}

// renderLines renders one line per skill, truncating descriptions to limit.
// A limit of 0 omits descriptions entirely.
func renderLines(skills []Skill, limit int) string {
	var b strings.Builder
	for _, s := range skills {
		b.WriteString("- `" + s.Name + "`")
		if limit > 0 {
			desc := s.Description
			if desc == "" {
				desc = "(no description)"
			}
			b.WriteString(": " + truncate(desc, limit))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}

// Tools implements module.ToolProvider.
func (m *Module) Tools() []module.Tool {
	return []module.Tool{{
		Name: "skill.load",
		Description: "Read the full text of a skill listed in the Skills index. " +
			"Call this before following a skill, not instead of it.",
		Schema: json.RawMessage(`{"type":"object","required":["name"],` +
			`"properties":{"name":{"type":"string","description":"the skill name as listed in the index"}}}`),
		Run: m.runLoad,
	}}
}

// runLoad is skill.load: it returns one skill's SKILL.md verbatim.
func (m *Module) runLoad(_ context.Context, _ module.Session, args json.RawMessage) (string, error) {
	var p struct {
		Name string `json:"name"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("skill.load: invalid arguments: %w", err)
		}
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return "", fmt.Errorf("skill.load: name is required")
	}

	for _, s := range m.skills {
		if !strings.EqualFold(s.Name, name) {
			continue
		}
		raw, err := os.ReadFile(s.Path)
		if err != nil {
			return "", fmt.Errorf("skill.load: reading %s: %w", s.Name, err)
		}
		if len(raw) > maxBodyBytes {
			return string(raw[:maxBodyBytes]) +
				"\n\n[truncated: this skill is longer than the tool result limit. " +
				"Read the rest from " + s.Path + " with the read tool.]", nil
		}
		return string(raw), nil
	}
	return "", fmt.Errorf("no skill named %q", name)
}
