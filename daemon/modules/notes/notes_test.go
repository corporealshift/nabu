package notes

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

type fakeSession struct{ id, dir, key string }

func (f fakeSession) ID() string { return f.id }
func (f fakeSession) Workspace() module.Workspace {
	return module.Workspace{Path: f.dir, Key: f.key}
}
func (f fakeSession) Events(*string) ([]protocol.Event, error) { return nil, nil }
func (f fakeSession) State() protocol.State                    { return protocol.State{} }
func (f fakeSession) Append(protocol.EventType, any) (protocol.Event, error) {
	return protocol.Event{}, nil
}

// clock is a fixed point the tests move by hand, so expiry is tested by
// arithmetic rather than by waiting fourteen days.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newModule(t *testing.T, cfg module.Config) (*Module, fakeSession, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)}
	m := &Module{Root: t.TempDir(), now: c.now}
	if err := m.Init(nil, cfg); err != nil {
		t.Fatal(err)
	}
	return m, fakeSession{id: "S1", dir: t.TempDir(), key: "proj-abc123"}, c
}

func write(t *testing.T, m *Module, s fakeSession, name, body string) string {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"name": name, "body": body})
	out, err := m.runWrite(context.Background(), s, args)
	if err != nil {
		t.Fatalf("notes.write(%q): %v", name, err)
	}
	return out
}

func block(t *testing.T, m *Module, s fakeSession) string {
	t.Helper()
	blocks, err := m.BeforeRequest(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) == 0 {
		return ""
	}
	if blocks[0].Slot != "suffix" {
		t.Fatalf("slot = %q; the prefix must not change between compactions", blocks[0].Slot)
	}
	return blocks[0].Content
}

func TestANoteSurvivesIntoTheNextRequest(t *testing.T) {
	m, s, _ := newModule(t, nil)

	if got := block(t, m, s); got != "" {
		t.Fatalf("no notes should mean no block, got:\n%s", got)
	}

	write(t, m, s, "simplefin-sync", "Y must be initialised before X calls it.")

	got := block(t, m, s)
	if !strings.Contains(got, "simplefin-sync") {
		t.Errorf("the note's name should be shown:\n%s", got)
	}
	if !strings.Contains(got, "Y must be initialised before X calls it.") {
		t.Errorf("the note's body should be shown:\n%s", got)
	}
}

// Many notes per workspace is the whole answer to "a multi-session task inside
// a repo": an effort gets its own note rather than sharing one.
func TestNotesAreNamedAndIndependent(t *testing.T) {
	m, s, c := newModule(t, nil)

	write(t, m, s, "refactor", "first")
	c.advance(time.Hour)
	write(t, m, s, "gotchas", "second")

	got := block(t, m, s)
	if !strings.Contains(got, "refactor") || !strings.Contains(got, "gotchas") {
		t.Fatalf("both notes should appear:\n%s", got)
	}
	// Newest first, so the most recent work reads first.
	if strings.Index(got, "gotchas") > strings.Index(got, "refactor") {
		t.Errorf("notes should be newest first:\n%s", got)
	}
}

// Writing an existing name replaces it, the way task.update replaces the list.
// Accreting would make the note grow without bound.
func TestWritingTheSameNameReplaces(t *testing.T) {
	m, s, c := newModule(t, nil)

	write(t, m, s, "refactor", "the first thing I thought")
	c.advance(2 * time.Hour)
	write(t, m, s, "refactor", "what I actually found")

	got := block(t, m, s)
	if strings.Contains(got, "the first thing I thought") {
		t.Errorf("the old body should be gone:\n%s", got)
	}
	if !strings.Contains(got, "what I actually found") {
		t.Errorf("the new body should be there:\n%s", got)
	}
	if strings.Count(got, "### refactor") != 1 {
		t.Errorf("there should be exactly one note named refactor:\n%s", got)
	}
}

// Rewriting refreshes the clock, which is how a long effort keeps its note
// alive while a finished one ages out.
func TestRewritingKeepsANoteAliveAndSilenceLetsItExpire(t *testing.T) {
	m, s, c := newModule(t, module.Config{"expire_days": 14})

	write(t, m, s, "long-effort", "still going")
	write(t, m, s, "abandoned", "nobody came back to this")

	// Twelve days on, the effort is rewritten; the other is not touched.
	c.advance(12 * 24 * time.Hour)
	write(t, m, s, "long-effort", "still going, further along")

	// Five more days: the abandoned note is 17 days old, the effort is 5.
	c.advance(5 * 24 * time.Hour)
	m.sweep(s)

	got := block(t, m, s)
	if !strings.Contains(got, "long-effort") {
		t.Errorf("a note still being rewritten should survive:\n%s", got)
	}
	if strings.Contains(got, "abandoned") {
		t.Errorf("a note nobody touched for 17 days should have expired:\n%s", got)
	}
}

func TestSweepRunsAtSessionStart(t *testing.T) {
	m, s, c := newModule(t, module.Config{"expire_days": 3})
	write(t, m, s, "old", "stale")
	c.advance(4 * 24 * time.Hour)

	if _, err := m.SessionStart(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if got := block(t, m, s); strings.Contains(got, "old") {
		t.Errorf("session start should have swept it:\n%s", got)
	}
}

// Notes are per workspace, and one repository's working state must not leak
// into another's.
func TestNotesDoNotLeakBetweenWorkspaces(t *testing.T) {
	m, a, _ := newModule(t, nil)
	b := fakeSession{id: "S2", dir: a.dir, key: "other-999999"}

	write(t, m, a, "mine", "belongs to the first repo")

	if got := block(t, m, b); got != "" {
		t.Fatalf("the other workspace should see nothing:\n%s", got)
	}
	if got := block(t, m, a); !strings.Contains(got, "mine") {
		t.Errorf("the owning workspace should still see it:\n%s", got)
	}
}

// Names come from the model, so they are not trusted as paths.
func TestANameCannotEscapeTheNotesDirectory(t *testing.T) {
	m, s, _ := newModule(t, nil)

	for _, name := range []string{"../escape", "../../etc/passwd", "a/b/c", `..\windows`} {
		write(t, m, s, name, "body")
	}

	// Everything written landed in the one directory and nowhere else.
	dir := filepath.Join(m.Root, "ws", s.key)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("a name created a directory: %s", e.Name())
		}
		if strings.ContainsAny(e.Name(), `/\`) {
			t.Errorf("a separator survived into the filename: %q", e.Name())
		}
	}
	// Nothing was written above the workspace directory.
	up, err := os.ReadDir(filepath.Join(m.Root, "ws"))
	if err != nil {
		t.Fatal(err)
	}
	if len(up) != 1 {
		t.Errorf("only the one workspace directory should exist, got %d entries", len(up))
	}
}

// Past the byte cap, notes are named rather than dropped: a note that vanished
// silently would be worse than one the model knows it has to go and refresh.
func TestNotesPastTheCapAreNamedNotDropped(t *testing.T) {
	m, s, c := newModule(t, module.Config{"max_bytes": 400})

	write(t, m, s, "oldest", strings.Repeat("a", 300))
	c.advance(time.Hour)
	write(t, m, s, "newest", strings.Repeat("b", 300))

	got := block(t, m, s)
	if !strings.Contains(got, "bbb") {
		t.Errorf("the newest note should be shown in full:\n%s", got)
	}
	if strings.Contains(got, "aaa") {
		t.Errorf("the older note's body should have been held back:\n%s", got)
	}
	if !strings.Contains(got, "oldest") {
		t.Errorf("the held-back note should still be named:\n%s", got)
	}
	if !strings.Contains(got, "still saved") {
		t.Errorf("the block should say the note is still there:\n%s", got)
	}
}

func TestDeleteRemovesANote(t *testing.T) {
	m, s, _ := newModule(t, nil)
	write(t, m, s, "doomed", "goodbye")

	args, _ := json.Marshal(map[string]string{"name": "doomed"})
	if _, err := m.runDelete(context.Background(), s, args); err != nil {
		t.Fatal(err)
	}
	if got := block(t, m, s); got != "" {
		t.Errorf("the note should be gone:\n%s", got)
	}

	// Deleting what is not there is not_found, not a silent success: the model
	// asked for something that did not happen.
	_, err := m.runDelete(context.Background(), s, args)
	if err == nil {
		t.Fatal("deleting a missing note should fail")
	}
	if kind, _ := module.ClassifyToolError(err); kind != protocol.ToolErrorNotFound {
		t.Errorf("kind = %q, want %q", kind, protocol.ToolErrorNotFound)
	}
}

func TestBadWritesAreInvalidArgs(t *testing.T) {
	m, s, _ := newModule(t, nil)
	cases := []struct{ name, args string }{
		{"no name", `{"body":"x"}`},
		{"no body", `{"name":"x"}`},
		{"blank body", `{"name":"x","body":"   "}`},
		{"a body past the limit", `{"name":"x","body":"` + strings.Repeat("y", maxBody+1) + `"}`},
		{"arguments that are not an object", `"nope"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.runWrite(context.Background(), s, json.RawMessage(tc.args))
			if err == nil {
				t.Fatal("want an error")
			}
			if kind, _ := module.ClassifyToolError(err); kind != protocol.ToolErrorInvalidArgs {
				t.Errorf("kind = %q, want %q", kind, protocol.ToolErrorInvalidArgs)
			}
		})
	}
}

// A summarize squeezes everything into one pass. The notes are what the model
// most needs to survive it, so the summariser is told to keep them.
func TestCompactionIsToldToPreserveTheNotes(t *testing.T) {
	m, s, _ := newModule(t, nil)
	write(t, m, s, "refactor", "Y must be initialised before X calls it.")

	preserve := m.BeforeCompaction(context.Background(), s, module.Range{})
	if len(preserve) != 1 {
		t.Fatalf("preserve = %v, want one entry", preserve)
	}
	if !strings.Contains(preserve[0], "Y must be initialised") {
		t.Errorf("the body should be preserved, got %q", preserve[0])
	}
	if !strings.Contains(preserve[0], "refactor") {
		t.Errorf("the name should be preserved, got %q", preserve[0])
	}
}

func TestDisabledOffersNothing(t *testing.T) {
	m, s, _ := newModule(t, module.Config{"enabled": false})
	if len(m.Tools()) != 0 {
		t.Error("a disabled module should offer no tools")
	}
	if got := block(t, m, s); got != "" {
		t.Errorf("a disabled module should say nothing, got:\n%s", got)
	}
}

func TestToolsAreOffered(t *testing.T) {
	m, _, _ := newModule(t, nil)
	var names []string
	for _, tool := range m.Tools() {
		names = append(names, tool.Name)
		if !json.Valid(tool.Schema) {
			t.Errorf("%s has an invalid schema: %s", tool.Name, tool.Schema)
		}
	}
	want := map[string]bool{"notes.write": true, "notes.delete": true}
	if len(names) != len(want) {
		t.Fatalf("tools = %v, want exactly %v", names, want)
	}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected tool %q", n)
		}
	}
}

// The files are the sharing mechanism: markdown the owner can open, grep and
// diff without any tool at all.
func TestNotesAreReadableMarkdownOnDisk(t *testing.T) {
	m, s, _ := newModule(t, nil)
	write(t, m, s, "Some Note Name", "the body\nover two lines")

	path := filepath.Join(m.Root, "ws", s.key, "some-note-name.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected a readable file at %s: %v", path, err)
	}
	text := string(raw)
	if !strings.Contains(text, "name: Some Note Name") {
		t.Errorf("frontmatter should carry the name as written:\n%s", text)
	}
	if !strings.Contains(text, "the body\nover two lines") {
		t.Errorf("the body should be plain markdown:\n%s", text)
	}

	// And it round-trips back through the reader used by the CLI.
	live := ForWorkspace(m.Root, s.key)
	if len(live) != 1 || live[0].Name != "Some Note Name" {
		t.Fatalf("round trip lost the note: %+v", live)
	}
}

func TestHumanAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{5 * time.Minute, "5m ago"},
		{3 * time.Hour, "3h ago"},
		{50 * time.Hour, "2d ago"},
	}
	for _, tc := range cases {
		if got := humanAge(tc.d); got != tc.want {
			t.Errorf("humanAge(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// The header alone is a few hundred bytes. If it counted against the first
// note, a tight cap would produce a block that names every note and shows none.
func TestTheNewestNoteIsAlwaysShown(t *testing.T) {
	m, s, _ := newModule(t, module.Config{"max_bytes": 1})
	write(t, m, s, "only", "this has to be visible")

	got := block(t, m, s)
	if !strings.Contains(got, "this has to be visible") {
		t.Errorf("the newest note must survive any cap:\n%s", got)
	}
	if strings.Contains(got, "still saved") {
		t.Errorf("with one note there is nothing to hold back:\n%s", got)
	}
}
