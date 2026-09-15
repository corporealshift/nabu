package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// realFormat is copied from the shape of the owner's actual memory files, which
// nest `type` under `metadata` and quote the description.
const realFormat = `---
name: llm-prompts-need-data-not-rules
description: "When an LLM's output is vague, Kyle wants more data given to it, not more restrictions."
metadata:
  node_type: memory
  type: feedback
  modified: 2026-09-14T13:47:36.986Z
---

Give the model more data, not more rules.

**Why:** Restrictions narrow the output without making it better informed.
**How to apply:** Add examples and context before adding constraints.
`

func TestParseRealFormat(t *testing.T) {
	m, err := Parse(realFormat)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Name != "llm-prompts-need-data-not-rules" {
		t.Errorf("name = %q", m.Name)
	}
	if m.Type != "feedback" {
		t.Errorf("type = %q, want feedback read from under metadata", m.Type)
	}
	if strings.HasPrefix(m.Description, `"`) || !strings.HasPrefix(m.Description, "When an LLM") {
		t.Errorf("description = %q, want the quotes stripped", m.Description)
	}
	if m.Modified.IsZero() {
		t.Error("modified was not parsed")
	}
	if !strings.Contains(m.Body, "**Why:**") {
		t.Errorf("body = %q", m.Body)
	}
	if strings.HasPrefix(m.Body, "\n") {
		t.Errorf("body keeps leading blank line: %q", m.Body)
	}
}

func TestParseRejects(t *testing.T) {
	for _, tc := range []struct{ name, content string }{
		{"no frontmatter", "just a body\n"},
		{"unterminated", "---\nname: x\n"},
		{"no name", "---\ndescription: a thing\n---\n\nbody\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.content); err == nil {
				t.Fatal("want an error, got none")
			}
		})
	}
}

func TestRoundTripPreservesUnknownKeys(t *testing.T) {
	m, err := Parse(realFormat)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	dir := t.TempDir()
	s := NewStore(dir, ScopeGlobal, false)
	if err := s.Save(m); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(s.Path(m.Name))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// node_type is Claude Code's and must survive nabu rewriting the file.
	if !strings.Contains(string(raw), "node_type: memory") {
		t.Errorf("unknown key was dropped:\n%s", raw)
	}

	again, err := Parse(string(raw))
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if again.Type != m.Type || again.Description != m.Description {
		t.Errorf("round trip changed the memory: %+v", again)
	}
}

func TestSlugCannotEscapeTheStore(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, ScopeGlobal, false)
	err := s.Save(Memory{Name: "../../escape", Type: "project", Body: "no"})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	escaped := filepath.Join(filepath.Dir(filepath.Dir(dir)), "escape.md")
	if _, err := os.Stat(escaped); err == nil {
		t.Fatalf("wrote outside the store at %s", escaped)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 || strings.Contains(got[0].Name, "..") {
		t.Fatalf("loaded %+v", got)
	}
}

func TestSaveOverwritesSameName(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, ScopeWorkspace, false)
	if err := s.Save(Memory{Name: "rancher-desktop", Type: "project", Body: "first"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	first, err := s.Load()
	if err != nil || len(first) != 1 {
		t.Fatalf("Load: %v %+v", err, first)
	}
	if err := s.Save(Memory{Name: "rancher-desktop", Type: "project", Body: "second"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	second, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(second) != 1 {
		t.Fatalf("a same-subject save duplicated the memory: %d files", len(second))
	}
	if !strings.Contains(second[0].Body, "second") {
		t.Errorf("body = %q, want the new one", second[0].Body)
	}
	if second[0].Modified.Before(first[0].Modified) {
		t.Errorf("modified went backwards: %v then %v", first[0].Modified, second[0].Modified)
	}
	if second[0].Modified.IsZero() {
		t.Error("modified was not written")
	}
}

func TestSaveRejects(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, ScopeGlobal, false)
	for _, tc := range []struct {
		name string
		m    Memory
		want string
	}{
		{"no name", Memory{Body: "x"}, "name"},
		{"no body", Memory{Name: "a"}, "body"},
		{"bad type", Memory{Name: "a", Body: "x", Type: "notes"}, "notes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := s.Save(tc.m)
			if err == nil {
				t.Fatal("want an error, got none")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestImportedStoreIsReadOnly(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, ScopeGlobal, true)
	if err := s.Save(Memory{Name: "a", Type: "user", Body: "x"}); err == nil {
		t.Fatal("an imported store accepted a save")
	}
	if err := s.Forget("a"); err == nil {
		t.Fatal("an imported store accepted a forget")
	}
}

func TestForget(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, ScopeGlobal, false)
	if err := s.Save(Memory{Name: "doomed", Type: "user", Body: "x"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Forget("doomed"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	got, err := s.Load()
	if err != nil || len(got) != 0 {
		t.Fatalf("Load after forget: %v %+v", err, got)
	}
	err = s.Forget("never-existed")
	if err == nil || !strings.Contains(err.Error(), "never-existed") {
		t.Errorf("error = %v, want it to name the memory", err)
	}
}

func TestLoadSkipsBadFilesAndMissingDirs(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, ScopeGlobal, false)
	if err := s.Save(Memory{Name: "good", Type: "user", Body: "x"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	for name, content := range map[string]string{
		"broken.md":   "no frontmatter here\n",
		"nameless.md": "---\ndescription: x\n---\n\nbody\n",
		IndexFile:     "- [Good](good.md)\n",
		"notes.txt":   "---\nname: ignored\n---\n\nbody\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 || got[0].Name != "good" {
		t.Fatalf("loaded %+v, want only the good memory", got)
	}
	if got[0].Scope != ScopeGlobal || got[0].Imported {
		t.Errorf("scope/imported not stamped: %+v", got[0])
	}

	missing := NewStore(filepath.Join(dir, "nope"), ScopeGlobal, false)
	empty, err := missing.Load()
	if err != nil {
		t.Fatalf("a missing directory is empty, not an error: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("loaded %+v", empty)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Rancher Desktop": "rancher-desktop",
		"../../escape":    "escape",
		"UPPER_snake":     "upper-snake",
		"already-kebab":   "already-kebab",
		"  spaced  out  ": "spaced-out",
		"!!!":             "memory",
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

// The real writer emits "metadata: " with a trailing space and an unknown key.
// Neither may stop the block being recognised as nested.
func TestParseMetadataWithTrailingSpace(t *testing.T) {
	const content = "---\n" +
		"name: nabu-project\n" +
		"description: \"what nabu is\"\n" +
		"metadata: \n" +
		"  node_type: memory\n" +
		"  type: project\n" +
		"  originSessionId: 99494281-9b1c-52e3-9a9c-9b69f05627c6\n" +
		"  modified: 2026-09-12T23:04:27.048Z\n" +
		"---\n\nthe fact\n"

	m, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Type != "project" {
		t.Errorf("type = %q, want project", m.Type)
	}
	if m.extraMeta["originSessionId"] == "" {
		t.Error("an unknown metadata key was dropped")
	}
	if m.Modified.IsZero() {
		t.Error("a millisecond timestamp did not parse")
	}
}

// Regression: the writer used a second-precision layout while the constant
// beside it promised milliseconds.
func TestModifiedIsWrittenWithMillisecondPrecision(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, ScopeGlobal, false)
	if err := s.Save(Memory{Name: "dated", Type: "user", Description: "d", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(s.Path("dated"))
	if err != nil {
		t.Fatal(err)
	}
	line := ""
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.Contains(l, "modified:") {
			line = strings.TrimSpace(l)
		}
	}
	if line == "" {
		t.Fatalf("no modified line:\n%s", raw)
	}
	value := strings.TrimSpace(strings.TrimPrefix(line, "modified:"))
	if !strings.Contains(value, ".") {
		t.Errorf("modified = %q, want a fractional second", value)
	}
	if _, err := time.Parse(modifiedLayout, value); err != nil {
		t.Errorf("modified = %q does not parse with the layout used to write it: %v", value, err)
	}
}
