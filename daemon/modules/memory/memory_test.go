package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// fakeSession is the slice of module.Session these hooks touch.
type fakeSession struct {
	id     string
	ws     module.Workspace
	events []protocol.Event
}

func (f *fakeSession) ID() string                  { return f.id }
func (f *fakeSession) Workspace() module.Workspace { return f.ws }
func (f *fakeSession) Events(*string) ([]protocol.Event, error) {
	return nil, nil
}
func (f *fakeSession) State() protocol.State { return protocol.State{} }
func (f *fakeSession) Append(t protocol.EventType, data any) (protocol.Event, error) {
	e := protocol.Event{Type: t}
	f.events = append(f.events, e)
	return e, nil
}

func newSession(t *testing.T, key string) *fakeSession {
	t.Helper()
	return &fakeSession{id: "01ARZ3NDEKTSV4RRFFQ69G5FAV", ws: module.Workspace{Path: t.TempDir(), Key: key}}
}

// newModule builds a module rooted in a temp directory, so no test can touch
// the developer's real ~/.nabu or ~/.claude.
func newModule(t *testing.T, importDirs ...string) *Module {
	t.Helper()
	m := &Module{Root: t.TempDir(), ImportDirs: importDirs, NoGit: true}
	if err := m.Init(nil, module.Config{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return m
}

func TestNoMemoriesMeansNoBlock(t *testing.T) {
	m := newModule(t)
	blocks, err := m.SessionStart(context.Background(), newSession(t, "repo-abc123"))
	if err != nil {
		t.Fatalf("SessionStart: %v", err)
	}
	if len(blocks) != 0 {
		t.Fatalf("got %d blocks with no memories, want none: %+v", len(blocks), blocks)
	}
}

func TestSessionStartCarriesBothIndexesAndTheInstructions(t *testing.T) {
	m := newModule(t)
	s := newSession(t, "repo-abc123")

	if err := m.GlobalStore().Save(Memory{
		Name: "owner-prefers-tables", Type: "user",
		Description: "the owner wants numbers in tables, not prose",
		Body:        "put measurements on their own line",
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.WorkspaceStore(s).Save(Memory{
		Name: "gate-is-build-vet-test", Type: "project",
		Description: "the project gate; run it whole",
		Body:        "go build ./... and go vet ./... and go test ./...",
	}); err != nil {
		t.Fatal(err)
	}

	blocks, err := m.SessionStart(context.Background(), s)
	if err != nil {
		t.Fatalf("SessionStart: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want one", len(blocks))
	}
	if blocks[0].Slot != "prefix" {
		t.Errorf("slot = %q, want prefix", blocks[0].Slot)
	}
	body := blocks[0].Content
	for _, want := range []string{"owner-prefers-tables.md", "gate-is-build-vet-test.md"} {
		if !strings.Contains(body, want) {
			t.Errorf("block is missing %s:\n%s", want, body)
		}
	}
	// The wording governs when the model saves, so it is fixed, not paraphrased.
	if !strings.Contains(body, SaveInstructions) {
		t.Errorf("the save instructions are not in the block:\n%s", body)
	}
	// Global before workspace, per spec 11.3.
	if strings.Index(body, "owner-prefers-tables") > strings.Index(body, "gate-is-build-vet-test") {
		t.Error("the workspace index came before the global one")
	}
}

func TestAfterCompactionMatchesSessionStart(t *testing.T) {
	m := newModule(t)
	s := newSession(t, "repo-abc123")
	if err := m.GlobalStore().Save(Memory{Name: "a", Type: "user", Description: "a fact", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	start, err := m.SessionStart(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	after, err := m.AfterCompaction(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(start) != 1 || len(after) != 1 || start[0].Content != after[0].Content {
		t.Errorf("a compaction retires the prefix block; it must come back identical")
	}
}

func TestWorkspacesDoNotShareMemory(t *testing.T) {
	m := newModule(t)
	one := newSession(t, "repo-aaaaaaaa")
	two := newSession(t, "repo-bbbbbbbb")

	if err := m.WorkspaceStore(one).Save(Memory{
		Name: "only-here", Type: "project", Description: "a fact about repo one", Body: "x",
	}); err != nil {
		t.Fatal(err)
	}

	blocks, err := m.SessionStart(context.Background(), two)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range blocks {
		if strings.Contains(b.Content, "only-here") {
			t.Fatalf("repo two sees repo one's memory:\n%s", b.Content)
		}
	}
}

func TestImportsAreReadOnlyAndMarked(t *testing.T) {
	importDir := t.TempDir()
	write := func(name, body string) {
		content := "---\nname: " + name + "\ndescription: \"imported fact\"\nmetadata:\n  type: project\n---\n\n" + body + "\n"
		if err := os.WriteFile(filepath.Join(importDir, name+".md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("from-claude", "a fact Claude Code knew")
	write("shadowed", "the imported version")

	// A directory that is not there must be skipped, not fail Init.
	m := newModule(t, importDir, filepath.Join(importDir, "does-not-exist"))
	s := newSession(t, "repo-abc123")

	blocks, err := m.SessionStart(context.Background(), s)
	if err != nil {
		t.Fatalf("SessionStart: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want one", len(blocks))
	}
	if !strings.Contains(blocks[0].Content, "from-claude.md") {
		t.Errorf("an imported memory is not in the index:\n%s", blocks[0].Content)
	}
	if !strings.Contains(strings.ToLower(blocks[0].Content), "imported") {
		t.Errorf("imports are not marked:\n%s", blocks[0].Content)
	}

	// The nabu copy shadows the import rather than editing it.
	if err := m.GlobalStore().Save(Memory{
		Name: "shadowed", Type: "project", Description: "the nabu version", Body: "the nabu version",
	}); err != nil {
		t.Fatal(err)
	}
	var seen int
	for _, mem := range m.globalCorpus() {
		if mem.Name == "shadowed" {
			seen++
			if mem.Imported {
				t.Error("the import won over the nabu copy")
			}
		}
	}
	if seen != 1 {
		t.Errorf("shadowed appears %d times, want once", seen)
	}

	raw, err := os.ReadFile(filepath.Join(importDir, "shadowed.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "the imported version") {
		t.Error("the import directory was modified")
	}
}

func TestOverCapRaisesANotice(t *testing.T) {
	m := newModule(t)
	s := newSession(t, "repo-abc123")
	for i := 0; i < maxIndexLines+2; i++ {
		name := "memory-" + strings.Repeat("a", i/26+1) + string(rune('a'+i%26))
		if err := m.GlobalStore().Save(Memory{
			Name: name, Type: "user", Description: "a fact", Body: "x",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.SessionStart(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	var notices int
	for _, e := range s.events {
		if e.Type == protocol.EventNotice {
			notices++
		}
	}
	if notices == 0 {
		t.Error("over the cap and the session was never told")
	}
}

func TestModuleSatisfiesItsHooks(t *testing.T) {
	var m any = &Module{}
	if _, ok := m.(module.Module); !ok {
		t.Error("not a Module")
	}
	if _, ok := m.(module.SessionStarter); !ok {
		t.Error("not a SessionStarter")
	}
	if _, ok := m.(module.ToolProvider); !ok {
		t.Error("not a ToolProvider")
	}
	if _, ok := m.(module.CompactionHook); !ok {
		t.Error("not a CompactionHook")
	}
	if _, ok := m.(module.SessionEnder); !ok {
		t.Error("not a SessionEnder")
	}
}

func TestDisabledModuleInjectsNothing(t *testing.T) {
	m := &Module{Root: t.TempDir(), NoGit: true}
	if err := m.Init(nil, module.Config{"enabled": false}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := m.GlobalStore().Save(Memory{Name: "a", Type: "user", Description: "d", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	blocks, err := m.SessionStart(context.Background(), newSession(t, "repo-abc123"))
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 0 {
		t.Errorf("a disabled module injected %d blocks", len(blocks))
	}
	if len(m.Tools()) != 0 {
		t.Errorf("a disabled module offered %d tools", len(m.Tools()))
	}
}
