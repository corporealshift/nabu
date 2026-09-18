package watch

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

type fakeSession struct{ id, dir string }

func (f fakeSession) ID() string                               { return f.id }
func (f fakeSession) Workspace() module.Workspace              { return module.Workspace{Path: f.dir, Key: "k"} }
func (f fakeSession) Events(*string) ([]protocol.Event, error) { return nil, nil }
func (f fakeSession) State() protocol.State                    { return protocol.State{} }
func (f fakeSession) Append(protocol.EventType, any) (protocol.Event, error) {
	return protocol.Event{}, nil
}

func newModule(t *testing.T, cfg module.Config) (*Module, fakeSession) {
	t.Helper()
	m := &Module{}
	if err := m.Init(nil, cfg); err != nil {
		t.Fatal(err)
	}
	return m, fakeSession{id: "S1", dir: t.TempDir()}
}

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

// touch rewrites a file with different content, so the change is visible
// whatever the filesystem's timestamp granularity.
func touch(t *testing.T, dir, name, body string) {
	t.Helper()
	write(t, dir, name, body)
}

func request(t *testing.T, m *Module, s fakeSession) string {
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

// Everything on disk when a session opens is the starting state, not a change.
func TestASessionDoesNotOpenByReportingTheWholeRepository(t *testing.T) {
	m, s := newModule(t, nil)
	write(t, s.dir, "a.txt", "one")
	write(t, s.dir, "b/c.txt", "two")

	if _, err := m.SessionStart(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if got := request(t, m, s); got != "" {
		t.Fatalf("a fresh session should report nothing, got:\n%s", got)
	}
}

func TestChangesMadeOutsideTheAgentAreReported(t *testing.T) {
	m, s := newModule(t, nil)
	write(t, s.dir, "kept.txt", "unchanged")
	write(t, s.dir, "edited.txt", "before")
	write(t, s.dir, "gone.txt", "doomed")
	if _, err := m.SessionStart(context.Background(), s); err != nil {
		t.Fatal(err)
	}

	// Somebody else's terminal, between turns.
	touch(t, s.dir, "edited.txt", "after, and longer")
	write(t, s.dir, "new.txt", "appeared")
	if err := os.Remove(filepath.Join(s.dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}

	got := request(t, m, s)
	if got == "" {
		t.Fatal("changes should have been reported")
	}
	for _, want := range []string{"modified: ", "edited.txt", "added: ", "new.txt", "deleted: ", "gone.txt"} {
		if !strings.Contains(got, want) {
			t.Errorf("block is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "kept.txt") {
		t.Errorf("an unchanged file should not be reported:\n%s", got)
	}
}

// Reporting the model's own edits back to it is noise, and noise in every
// request is worse than a gap in an unlikely one.
func TestTheAgentsOwnEditsAreNotReportedBackToIt(t *testing.T) {
	m, s := newModule(t, nil)
	write(t, s.dir, "mine.txt", "before")
	write(t, s.dir, "theirs.txt", "before")
	if _, err := m.SessionStart(context.Background(), s); err != nil {
		t.Fatal(err)
	}

	// The agent edits one file through a tool...
	touch(t, s.dir, "mine.txt", "the agent wrote this")
	m.ToolResult(context.Background(), s,
		protocol.ToolCallData{Tool: "edit", Arguments: json.RawMessage(`{"path":"mine.txt"}`)},
		protocol.ToolResultData{Status: "ok"})

	// ...and somebody else changes another.
	touch(t, s.dir, "theirs.txt", "somebody else wrote this")

	got := request(t, m, s)
	if strings.Contains(got, "mine.txt") {
		t.Errorf("the agent's own edit was reported back to it:\n%s", got)
	}
	if !strings.Contains(got, "theirs.txt") {
		t.Errorf("the external change should still be reported:\n%s", got)
	}
}

// A failed write changed nothing, so it must not suppress a later report about
// that path.
func TestAFailedWriteDoesNotSuppressAnything(t *testing.T) {
	m, s := newModule(t, nil)
	write(t, s.dir, "f.txt", "before")
	if _, err := m.SessionStart(context.Background(), s); err != nil {
		t.Fatal(err)
	}

	m.ToolResult(context.Background(), s,
		protocol.ToolCallData{Tool: "write", Arguments: json.RawMessage(`{"path":"f.txt"}`)},
		protocol.ToolResultData{Status: "error"})
	touch(t, s.dir, "f.txt", "changed by someone else")

	if got := request(t, m, s); !strings.Contains(got, "f.txt") {
		t.Errorf("a failed write should not suppress the report:\n%s", got)
	}
}

// Suppression lasts one turn. A file the agent wrote two turns ago and someone
// else changed since is news again.
func TestSuppressionLastsOnlyOneTurn(t *testing.T) {
	m, s := newModule(t, nil)
	write(t, s.dir, "f.txt", "before")
	if _, err := m.SessionStart(context.Background(), s); err != nil {
		t.Fatal(err)
	}

	touch(t, s.dir, "f.txt", "the agent wrote this")
	m.ToolResult(context.Background(), s,
		protocol.ToolCallData{Tool: "write", Arguments: json.RawMessage(`{"path":"f.txt"}`)},
		protocol.ToolResultData{Status: "ok"})
	if got := request(t, m, s); strings.Contains(got, "f.txt") {
		t.Fatalf("the agent's own write should be suppressed this turn:\n%s", got)
	}

	touch(t, s.dir, "f.txt", "and now somebody else has")
	if got := request(t, m, s); !strings.Contains(got, "f.txt") {
		t.Errorf("a later external change should be reported:\n%s", got)
	}
}

func TestAQuietWorkspaceSaysNothing(t *testing.T) {
	m, s := newModule(t, nil)
	write(t, s.dir, "a.txt", "still")
	if _, err := m.SessionStart(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if got := request(t, m, s); got != "" {
			t.Fatalf("nothing changed, so nothing should be said, got:\n%s", got)
		}
	}
}

// Naming a hundred files costs more context than saying there were a hundred.
func TestALongListIsCappedAndSaysSo(t *testing.T) {
	m, s := newModule(t, module.Config{"max_reported": 3})
	if _, err := m.SessionStart(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		write(t, s.dir, "f"+string(rune('a'+i))+".txt", "new")
	}

	got := request(t, m, s)
	if strings.Count(got, "- added:") != 3 {
		t.Errorf("want 3 named files, got:\n%s", got)
	}
	if !strings.Contains(got, "and 7 more") {
		t.Errorf("the remainder should be counted:\n%s", got)
	}
}

// A per-turn walk of a huge tree costs more than the answer is worth, and doing
// it silently would be worse.
func TestAWorkspaceTooLargeToScanTurnsItselfOff(t *testing.T) {
	m, s := newModule(t, module.Config{"max_files": 5})
	for i := 0; i < 20; i++ {
		write(t, s.dir, "f"+string(rune('a'+i))+".txt", "x")
	}
	if _, err := m.SessionStart(context.Background(), s); err != nil {
		t.Fatal(err)
	}

	write(t, s.dir, "another.txt", "x")
	if got := request(t, m, s); got != "" {
		t.Errorf("a workspace over the budget should not be scanned, got:\n%s", got)
	}
}

// Noisy directories are skipped, or every build would look like the repository
// had been rewritten.
func TestBuildOutputIsNotAChange(t *testing.T) {
	m, s := newModule(t, nil)
	write(t, s.dir, "src.go", "package x")
	if _, err := m.SessionStart(context.Background(), s); err != nil {
		t.Fatal(err)
	}

	write(t, s.dir, ".git/objects/ab/cdef", "blob")
	write(t, s.dir, "node_modules/left-pad/index.js", "module.exports=1")
	write(t, s.dir, "build/out.bin", "binary")

	if got := request(t, m, s); got != "" {
		t.Errorf("build and vcs output should not be reported, got:\n%s", got)
	}
}

func TestDisabledDoesNothing(t *testing.T) {
	m, s := newModule(t, module.Config{"enabled": false})
	write(t, s.dir, "a.txt", "x")
	if _, err := m.SessionStart(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	write(t, s.dir, "b.txt", "y")
	if got := request(t, m, s); got != "" {
		t.Errorf("a disabled module should say nothing, got:\n%s", got)
	}
}

// A daemon that runs for weeks should not hold a file list for every session it
// ever opened.
func TestSessionEndDropsTheSnapshot(t *testing.T) {
	m, s := newModule(t, nil)
	if _, err := m.SessionStart(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	held := len(m.state)
	m.mu.Unlock()
	if held != 1 {
		t.Fatalf("state entries = %d, want 1", held)
	}

	m.SessionEnd(context.Background(), s)
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.state) != 0 {
		t.Errorf("state entries = %d after the session ended, want 0", len(m.state))
	}
}

func TestDiffClassifiesEachKind(t *testing.T) {
	now := time.Now()
	before := snapshot{
		"/w/same.txt":    {mod: now, size: 10},
		"/w/changed.txt": {mod: now, size: 10},
		"/w/gone.txt":    {mod: now, size: 10},
	}
	after := snapshot{
		"/w/same.txt":    {mod: now, size: 10},
		"/w/changed.txt": {mod: now.Add(time.Second), size: 10},
		"/w/added.txt":   {mod: now, size: 4},
	}

	got := map[string]string{}
	for _, c := range diff(before, after, nil) {
		got[c.Path] = c.Kind
	}
	want := map[string]string{
		"/w/changed.txt": "modified",
		"/w/gone.txt":    "deleted",
		"/w/added.txt":   "added",
	}
	for p, kind := range want {
		if got[p] != kind {
			t.Errorf("%s = %q, want %q", p, got[p], kind)
		}
	}
	if _, reported := got["/w/same.txt"]; reported {
		t.Error("an unchanged file should not appear")
	}
}

// Same mtime, different size: a rewrite within the filesystem's timestamp
// granularity still has to register.
func TestASizeChangeAloneIsAChange(t *testing.T) {
	now := time.Now()
	got := diff(
		snapshot{"/w/f.txt": {mod: now, size: 10}},
		snapshot{"/w/f.txt": {mod: now, size: 11}},
		nil,
	)
	if len(got) != 1 || got[0].Kind != "modified" {
		t.Errorf("diff = %+v, want one modification", got)
	}
}
