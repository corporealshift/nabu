package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Scope is where a memory lives.
type Scope string

const (
	// ScopeGlobal is user-level: who the owner is, how they like to work.
	ScopeGlobal Scope = "global"
	// ScopeWorkspace is per repository.
	ScopeWorkspace Scope = "workspace"
	// ScopeAll searches both. It is a query scope, never a storage one.
	ScopeAll Scope = "all"
)

// Types a memory may have, per spec 11.2.
var validTypes = map[string]bool{
	"user": true, "feedback": true, "project": true, "reference": true,
}

// Memory is one remembered fact.
type Memory struct {
	Name        string
	Description string
	Type        string
	Modified    time.Time
	Body        string

	// Scope is where it was loaded from.
	Scope Scope
	// Imported marks a memory read from another tool's directory, never written.
	Imported bool
	// Path is the file it came from.
	Path string

	// extra holds frontmatter keys this parser does not use, so a round trip
	// does not strip them.
	extra map[string]string
	// extraMeta is the same for keys under the metadata block.
	extraMeta map[string]string
}

// IndexFile is the one-line-per-memory index, and is never itself a memory.
const IndexFile = "MEMORY.md"

// Store is one directory of memories.
type Store struct {
	dir      string
	scope    Scope
	imported bool
}

// NewStore describes a directory without reading it.
func NewStore(dir string, scope Scope, imported bool) *Store {
	return &Store{dir: dir, scope: scope, imported: imported}
}

// Dir is the directory this store reads.
func (s *Store) Dir() string { return s.dir }

// Load reads every memory in the directory, sorted by name. A directory that
// does not exist is empty, not an error.
func (s *Store) Load() ([]Memory, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("memory: reading %s: %w", s.dir, err)
	}

	var out []Memory
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || name == IndexFile {
			continue
		}
		m, err := parseFile(filepath.Join(s.dir, name))
		if err != nil {
			continue // one malformed memory must not cost the others
		}
		m.Scope, m.Imported = s.scope, s.imported
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Save writes one memory, replacing any file of the same name. Spec 11.4 wants
// same-subject files updated rather than duplicated; the name enforces it.
func (s *Store) Save(m Memory) error {
	if s.imported {
		return fmt.Errorf("memory: %s is read-only", s.dir)
	}
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("memory: a name is required")
	}
	if strings.TrimSpace(m.Body) == "" {
		return fmt.Errorf("memory: a body is required")
	}
	if m.Type != "" && !validTypes[m.Type] {
		return fmt.Errorf("memory: unknown type %q (user|feedback|project|reference)", m.Type)
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	m.Name = Slug(m.Name)
	m.Modified = time.Now().UTC()
	return os.WriteFile(s.Path(m.Name), []byte(render(m)), 0o644)
}

// Forget deletes one memory. Git keeps the history.
func (s *Store) Forget(name string) error {
	if s.imported {
		return fmt.Errorf("memory: %s is read-only", s.dir)
	}
	path := s.Path(Slug(name))
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("memory: no memory named %q", name)
	}
	return os.Remove(path)
}

// Path is where a named memory lives.
func (s *Store) Path(name string) string {
	return filepath.Join(s.dir, Slug(name)+".md")
}

// unsafe matches anything that may not appear in a memory's filename.
var unsafe = regexp.MustCompile(`[^a-z0-9-]+`)

// Slug turns a name into a filename that cannot escape its directory. It must
// be stable, because the name is the identity of a memory.
func Slug(name string) string {
	s := unsafe.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	s = strings.Trim(s, "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	if s == "" {
		return "memory"
	}
	return s
}

// parseFile reads one memory file.
func parseFile(path string) (Memory, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Memory{}, err
	}
	m, err := Parse(string(raw))
	if err != nil {
		return Memory{}, fmt.Errorf("%s: %w", path, err)
	}
	m.Path = path
	return m, nil
}

// Parse reads the frontmatter and body of a memory file. The real files differ
// from the spec's summary in two ways that matter: `type` sits under a
// `metadata` block, and values may be quoted. A parser written from the summary
// reads every existing memory as typeless.
func Parse(content string) (Memory, error) {
	content = strings.TrimLeft(content, "\ufeff \t\r\n")
	if !strings.HasPrefix(content, "---") {
		return Memory{}, fmt.Errorf("no frontmatter")
	}
	rest := content[3:]
	if i := strings.IndexAny(rest, "\r\n"); i >= 0 {
		rest = rest[i+1:]
	} else {
		return Memory{}, fmt.Errorf("no frontmatter")
	}
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return Memory{}, fmt.Errorf("unterminated frontmatter")
	}
	front, body := rest[:end], rest[end+4:]

	m := Memory{
		extra:     map[string]string{},
		extraMeta: map[string]string{},
	}
	inMeta := false
	for _, line := range strings.Split(front, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		indented := strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t")
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = unquote(strings.TrimSpace(value))

		// "metadata:" with nothing after it opens the nested block.
		if key == "metadata" && value == "" {
			inMeta = true
			continue
		}
		if !indented {
			inMeta = false
		}

		switch {
		case inMeta && key == "type":
			m.Type = value
		case inMeta && key == "modified":
			m.Modified = parseTime(value)
		case inMeta:
			m.extraMeta[key] = value
		case key == "name":
			m.Name = value
		case key == "description":
			m.Description = value
		case key == "type": // tolerated at the top level too
			m.Type = value
		case key == "modified":
			m.Modified = parseTime(value)
		default:
			m.extra[key] = value
		}
	}

	if m.Name == "" {
		return Memory{}, fmt.Errorf("frontmatter has no name")
	}
	m.Body = strings.TrimLeft(body, "\r\n")
	return m, nil
}

// modifiedLayout keeps the millisecond precision Claude Code writes. At second
// precision two saves inside one second are indistinguishable.
const modifiedLayout = "2006-01-02T15:04:05.000Z07:00"

// parseTime accepts either precision. An unparseable date leaves the field zero
// rather than failing the memory.
func parseTime(v string) time.Time {
	for _, layout := range []string{modifiedLayout, time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

// unquote strips one layer of surrounding quotes.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// render writes a memory back in the format it was read in, keeping unknown
// keys: nabu is not the only writer of these files.
func render(m Memory) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + m.Name + "\n")
	if m.Description != "" {
		b.WriteString("description: " + quote(m.Description) + "\n")
	}
	for _, k := range sortedKeys(m.extra) {
		b.WriteString(k + ": " + m.extra[k] + "\n")
	}

	b.WriteString("metadata:\n")
	if m.Type != "" {
		b.WriteString("  type: " + m.Type + "\n")
	}
	b.WriteString("  modified: " + m.Modified.UTC().Format(modifiedLayout) + "\n")
	for _, k := range sortedKeys(m.extraMeta) {
		b.WriteString("  " + k + ": " + m.extraMeta[k] + "\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimRight(m.Body, "\n") + "\n")
	return b.String()
}

// quote wraps a value only when it would otherwise be ambiguous.
func quote(s string) string {
	if strings.ContainsAny(s, ":#\"'") {
		return `"` + strings.ReplaceAll(s, `"`, `'`) + `"`
	}
	return s
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
