package notes

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Note is one named piece of working state.
type Note struct {
	Name    string
	Written time.Time
	Body    string
	// Path is the file it came from, for the CLI and for delete.
	Path string
}

// Age is how long since the note was last written.
func (n Note) Age(now time.Time) time.Duration { return now.Sub(n.Written) }

// Store is one workspace's notes on disk.
//
// Plain markdown with frontmatter, rather than a database or a single JSON
// blob, because "share those notes with me" is most of the point: a file the
// owner can open, grep and diff needs no tool at all.
type Store struct{ dir string }

func NewStore(dir string) *Store { return &Store{dir: dir} }

// Dir is where this store keeps its files.
func (s *Store) Dir() string { return s.dir }

var unsafeName = regexp.MustCompile(`[^a-z0-9]+`)

// Slug turns a note name into a filename. Names come from the model, so they
// are not trusted as paths: the result holds no separators and cannot climb out
// of the directory whatever it was handed.
func Slug(name string) string {
	s := unsafeName.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	s = strings.Trim(s, "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	if s == "" {
		return "note"
	}
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	return s
}

// List reads every note, newest first.
//
// A file that will not parse is skipped rather than failing the read: one bad
// note should cost its own line, not the whole set.
func (s *Store) List() []Note {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	var out []Note
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		n, err := parseFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Written.After(out[j].Written) })
	return out
}

// Write creates or replaces a note.
func (s *Store) Write(name, body string, now time.Time) (Note, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Note{}, fmt.Errorf("a note needs a name")
	}
	if strings.TrimSpace(body) == "" {
		return Note{}, fmt.Errorf("a note needs a body; use notes.delete to remove one")
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return Note{}, err
	}
	n := Note{Name: name, Written: now.UTC().Truncate(time.Second), Body: strings.TrimRight(body, "\n")}
	n.Path = filepath.Join(s.dir, Slug(name)+".md")
	if err := os.WriteFile(n.Path, []byte(render(n)), 0o644); err != nil {
		return Note{}, err
	}
	return n, nil
}

// Delete removes a note by name, reporting whether one was there.
func (s *Store) Delete(name string) (bool, error) {
	path := filepath.Join(s.dir, Slug(name)+".md")
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Sweep deletes notes last written before cutoff and returns their names.
func (s *Store) Sweep(cutoff time.Time) []string {
	var gone []string
	for _, n := range s.List() {
		if n.Written.Before(cutoff) {
			if err := os.Remove(n.Path); err == nil {
				gone = append(gone, n.Name)
			}
		}
	}
	sort.Strings(gone)
	return gone
}

// render writes a note's file. The frontmatter carries the name as the model
// spelled it, because the filename is a slug and the two need not match.
func render(n Note) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", n.Name)
	fmt.Fprintf(&b, "written: %s\n", n.Written.Format(time.RFC3339))
	b.WriteString("---\n\n")
	b.WriteString(n.Body)
	b.WriteString("\n")
	return b.String()
}

// parseFile reads one note file.
func parseFile(path string) (Note, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Note{}, err
	}
	n, err := Parse(string(raw))
	if err != nil {
		return Note{}, fmt.Errorf("%s: %w", path, err)
	}
	n.Path = path
	if n.Name == "" {
		n.Name = strings.TrimSuffix(filepath.Base(path), ".md")
	}
	if n.Written.IsZero() {
		// No usable timestamp: fall back to the file's, so a hand-written note
		// still ages rather than living forever or vanishing at once.
		if info, err := os.Stat(path); err == nil {
			n.Written = info.ModTime().UTC()
		}
	}
	return n, nil
}

// Parse reads a note from its file contents. Frontmatter is optional so a note
// the owner wrote by hand still loads.
func Parse(raw string) (Note, error) {
	text := strings.ReplaceAll(raw, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return Note{Body: strings.TrimSpace(text)}, nil
	}
	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return Note{Body: strings.TrimSpace(text)}, nil
	}
	head, body := rest[:end], rest[end+len("\n---"):]
	body = strings.TrimPrefix(body, "\n")

	var n Note
	for _, line := range strings.Split(head, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "name":
			n.Name = value
		case "written":
			if t, err := time.Parse(time.RFC3339, value); err == nil {
				n.Written = t.UTC()
			}
		}
	}
	n.Body = strings.TrimSpace(body)
	return n, nil
}
