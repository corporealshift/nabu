package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// Reminding the model of a skill when it touches the files the skill covers.
//
// The index is a prefix block, and a local model working through a long task
// does not go back to it: in 01M3B65R it wrote an Android app from scratch in
// 147 tool calls with android-dev listed and never loaded it. A suffix block
// sits next to what it is doing, and that model does act on those. See
// docs/specs/2026-09-25-thinking-loops-and-skill-reminders-design.md.

// glob is one compiled "paths" pattern.
type glob struct {
	pattern string
	re      *regexp.Regexp
}

// parseGlobs reads a comma-separated "paths" value. The frontmatter parser is
// flat key: value, so a list is one line.
func parseGlobs(value string) []glob {
	var out []glob
	for _, p := range strings.Split(value, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, glob{pattern: p, re: compileGlob(p)})
		}
	}
	return out
}

// compileGlob turns a glob into a case-insensitive regexp over slash paths.
// "**/" is any number of directories, "*" and "?" stay within one, and a glob
// with no slash matches a file's name at any depth, as in .gitignore.
func compileGlob(g string) *regexp.Regexp {
	g = filepath.ToSlash(g)
	if !strings.Contains(g, "/") {
		g = "**/" + g
	}
	var b strings.Builder
	b.WriteString("(?i)^")
	for i := 0; i < len(g); i++ {
		switch c := g[i]; {
		case c == '*' && i+1 < len(g) && g[i+1] == '*':
			i++
			if i+1 < len(g) && g[i+1] == '/' {
				i++
				b.WriteString("(?:.*/)?")
			} else {
				b.WriteString(".*")
			}
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// covers reports whether the skill is about a workspace-relative path.
func (s Skill) covers(rel string) bool {
	for _, g := range s.Covers {
		if g.re.MatchString(rel) {
			return true
		}
	}
	return false
}

// reminders holds the blocks waiting for each session's next request.
type reminders struct {
	mu      sync.Mutex
	pending map[string][]pendingReminder
}

type pendingReminder struct {
	skill, text string
}

// ToolResult implements module.ToolObserver: it notes a skill whose files the
// call touched, if the skill is neither loaded nor already named.
func (m *Module) ToolResult(_ context.Context, s module.Session, call protocol.ToolCallData, _ protocol.ToolResultData) {
	if s == nil || call.Tool == "skill.load" {
		return
	}
	var candidates []Skill
	for _, sk := range m.current() {
		if len(sk.Covers) > 0 {
			candidates = append(candidates, sk)
		}
	}
	if len(candidates) == 0 {
		return
	}
	touched := touchedPaths(s.Workspace().Path, call)
	if len(touched) == 0 {
		return
	}

	var known map[string]bool // read from the log only once something matches
	for _, sk := range candidates {
		for _, rel := range touched {
			if !sk.covers(rel) {
				continue
			}
			if known == nil {
				known = settledSkills(s)
			}
			if !known[strings.ToLower(sk.Name)] && !m.isPending(s.ID(), sk.Name) {
				m.queue(s.ID(), pendingReminder{skill: sk.Name, text: reminderText(sk, rel)})
			}
			break
		}
	}
}

// BeforeRequest implements module.RequestHook: the waiting reminders go in
// front of the model as suffix blocks, once.
func (m *Module) BeforeRequest(_ context.Context, s module.Session) ([]module.ContextBlock, error) {
	if s == nil {
		return nil, nil
	}
	m.rem.mu.Lock()
	waiting := m.rem.pending[s.ID()]
	delete(m.rem.pending, s.ID())
	m.rem.mu.Unlock()

	var out []module.ContextBlock
	for _, r := range waiting {
		out = append(out, module.ContextBlock{Slot: "suffix", Content: r.text})
	}
	return out, nil
}

// SessionEnd implements module.SessionEnder: nothing waits for a session that
// has ended.
func (m *Module) SessionEnd(_ context.Context, s module.Session) {
	if s == nil {
		return
	}
	m.rem.mu.Lock()
	delete(m.rem.pending, s.ID())
	m.rem.mu.Unlock()
}

func (m *Module) queue(id string, r pendingReminder) {
	m.rem.mu.Lock()
	defer m.rem.mu.Unlock()
	if m.rem.pending == nil {
		m.rem.pending = map[string][]pendingReminder{}
	}
	m.rem.pending[id] = append(m.rem.pending[id], r)
}

func (m *Module) isPending(id, skill string) bool {
	m.rem.mu.Lock()
	defer m.rem.mu.Unlock()
	for _, r := range m.rem.pending[id] {
		if strings.EqualFold(r.skill, skill) {
			return true
		}
	}
	return false
}

// reminderLead opens every reminder, and is how the log is read back to find
// which skills were already named.
const reminderLead = "Call `skill.load` with `"

func reminderText(sk Skill, rel string) string {
	desc := strings.TrimSpace(sk.Description)
	if desc != "" {
		desc = ": \"" + truncate(desc, maxDescription) + "\""
	}
	return fmt.Sprintf("## Skill\n\nYou are working on `%s`, which the `%s` skill covers%s. "+
		"%s%s` and follow it before going further.\n", rel, sk.Name, desc, reminderLead, sk.Name)
}

var namedInReminder = regexp.MustCompile(regexp.QuoteMeta(reminderLead) + "([^`]+)`")

// settledSkills are the skills, lowercased, that this session has loaded or
// been reminded of. They come from the log rather than memory, so a daemon
// restart does not repeat a reminder.
func settledSkills(s module.Session) map[string]bool {
	out := map[string]bool{}
	events, err := s.Events(nil)
	if err != nil {
		return out
	}
	for _, e := range events {
		switch e.Type {
		case protocol.EventToolCall:
			var d protocol.ToolCallData
			if json.Unmarshal(e.Data, &d) != nil || d.Tool != "skill.load" {
				continue
			}
			var a struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(d.Arguments, &a) == nil && a.Name != "" {
				out[strings.ToLower(strings.TrimSpace(a.Name))] = true
			}
		case protocol.EventContext:
			var d protocol.ContextData
			if json.Unmarshal(e.Data, &d) != nil || d.Source != "module:skills" || d.Slot != "suffix" {
				continue
			}
			for _, match := range namedInReminder.FindAllStringSubmatch(d.Content, -1) {
				out[strings.ToLower(match[1])] = true
			}
		}
	}
	return out
}

// touchedPaths are the workspace-relative paths a call names: a path or glob
// pattern argument, or the path-like words of a shell command.
func touchedPaths(workspace string, call protocol.ToolCallData) []string {
	var a struct {
		Path    string `json:"path"`
		Pattern string `json:"pattern"`
		Command string `json:"command"`
	}
	if len(call.Arguments) > 0 {
		_ = json.Unmarshal(call.Arguments, &a)
	}
	var raw []string
	if a.Path != "" {
		raw = append(raw, a.Path)
	}
	if call.Tool == "glob" && a.Pattern != "" {
		raw = append(raw, a.Pattern)
	}
	if a.Command != "" && (call.Tool == "bash" || call.Tool == "wait") {
		raw = append(raw, commandWords(a.Command)...)
	}
	var out []string
	for _, p := range raw {
		if rel := relative(workspace, p); rel != "" {
			out = append(out, rel)
		}
	}
	return out
}

// commandWords splits a command into its words, dropping flags. Quoting is
// not parsed; a word with its quotes trimmed is close enough to match a path.
func commandWords(command string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(command, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || strings.ContainsRune(";|&()<>", r)
	}) {
		if w = strings.Trim(w, `"'`); w != "" && !strings.HasPrefix(w, "-") {
			out = append(out, w)
		}
	}
	return out
}

// msysDrive matches Git Bash's /c/... form of a Windows drive path.
var msysDrive = regexp.MustCompile(`^/([a-zA-Z])/`)

// relative names p relative to the workspace, with forward slashes. A path
// outside the workspace is "", since no skill covers files it is not about.
func relative(workspace, p string) string {
	p = filepath.ToSlash(strings.TrimSpace(p))
	if m := msysDrive.FindStringSubmatch(p); m != nil {
		p = strings.ToUpper(m[1]) + ":/" + p[3:]
	}
	if filepath.IsAbs(filepath.FromSlash(p)) {
		if workspace == "" {
			return ""
		}
		rel, err := filepath.Rel(workspace, filepath.FromSlash(p))
		if err != nil {
			return ""
		}
		p = filepath.ToSlash(rel)
	}
	p = strings.TrimPrefix(p, "./")
	if p == "" || p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return ""
	}
	return p
}
