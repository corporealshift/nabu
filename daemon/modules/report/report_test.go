package report

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// fakeSession is the slice of module.Session this module uses.
type fakeSession struct{ workspace string }

func (f fakeSession) ID() string                               { return "01ARZ3NDEKTSV4RRFFQ69G5FAV" }
func (f fakeSession) Workspace() module.Workspace              { return module.Workspace{Path: f.workspace} }
func (f fakeSession) State() protocol.State                    { return protocol.State{} }
func (f fakeSession) Events(*string) ([]protocol.Event, error) { return nil, nil }
func (f fakeSession) Append(protocol.EventType, any) (protocol.Event, error) {
	return protocol.Event{}, nil
}

func newReport(t *testing.T, cfg module.Config) *Module {
	t.Helper()
	m := &Module{}
	if err := m.Init(nil, cfg); err != nil {
		t.Fatal(err)
	}
	return m
}

// run executes a git command in dir, failing the test if it errors.
func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitRepo makes a temp repository with one commit, skipping if git is absent.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	dir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git init: %v\n%s", err, out)
	}
	// Local config only, so the test never reads or writes the real one.
	run(t, dir, "config", "user.email", "test@example.com")
	run(t, dir, "config", "user.name", "Test")
	run(t, dir, "config", "commit.gpgsign", "false")

	write(t, dir, "README.md", "hi\n")
	run(t, dir, "add", "README.md")
	run(t, dir, "commit", "-m", "initial")
	return dir
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNameAndDefaults(t *testing.T) {
	m := newReport(t, module.Config{})
	if got := m.Name(); got != "report" {
		t.Errorf("Name: got %q, want %q", got, "report")
	}
	if m.CommitLimit != DefaultCommitLimit {
		t.Errorf("commit limit: got %d, want %d", m.CommitLimit, DefaultCommitLimit)
	}
}

// A plain directory is a normal case, not an error.
func TestNonRepoReportsNothing(t *testing.T) {
	m := newReport(t, module.Config{})
	fields, err := m.Report(context.Background(), fakeSession{workspace: t.TempDir()})
	if err != nil {
		t.Fatalf("a non-repository must not be an error: %v", err)
	}
	if fields.FilesTouched != nil || fields.Commits != nil || fields.TreeDirty != nil {
		t.Errorf("nothing should be asserted about a non-repository, got %+v", fields)
	}
}

func TestNilSessionIsSurvivable(t *testing.T) {
	m := newReport(t, module.Config{})
	if _, err := m.Report(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

// An untracked file the agent created is a touched file: leaving it out would
// hide exactly the work a caller most wants to see.
func TestFilesTouchedIncludesModifiedAndUntracked(t *testing.T) {
	dir := gitRepo(t)
	write(t, dir, "README.md", "changed\n")
	write(t, dir, "new.go", "package main\n")

	m := newReport(t, module.Config{})
	fields, err := m.Report(context.Background(), fakeSession{workspace: dir})
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}
	for _, f := range fields.FilesTouched {
		got[f] = true
	}
	if !got["README.md"] {
		t.Error("a modified tracked file should be listed")
	}
	if !got["new.go"] {
		t.Error("an untracked file should be listed: the agent created it")
	}
}

func TestCleanRepoTouchesNothing(t *testing.T) {
	dir := gitRepo(t)
	m := newReport(t, module.Config{})
	fields, err := m.Report(context.Background(), fakeSession{workspace: dir})
	if err != nil {
		t.Fatal(err)
	}
	if fields.FilesTouched != nil {
		t.Errorf("a clean repository should touch nothing, got %v", fields.FilesTouched)
	}
}

func TestCommitsAreListed(t *testing.T) {
	dir := gitRepo(t)
	write(t, dir, "a.go", "package a\n")
	run(t, dir, "add", "a.go")
	run(t, dir, "commit", "-m", "add a")

	m := newReport(t, module.Config{})
	fields, err := m.Report(context.Background(), fakeSession{workspace: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(fields.Commits) == 0 {
		t.Fatal("commits should be listed")
	}
	joined := strings.Join(fields.Commits, "\n")
	if !strings.Contains(joined, "add a") {
		t.Errorf("the newest commit should appear, got %q", joined)
	}
}

// A long run can make many commits; the report is meant to be read.
func TestCommitsAreCapped(t *testing.T) {
	dir := gitRepo(t)
	for i := 0; i < 6; i++ {
		name := filepath.Join(dir, "f"+string(rune('a'+i))+".txt")
		if err := os.WriteFile(name, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run(t, dir, "add", filepath.Base(name))
		run(t, dir, "commit", "-m", "commit "+string(rune('a'+i)))
	}

	m := newReport(t, module.Config{"commit_limit": 3})
	fields, err := m.Report(context.Background(), fakeSession{workspace: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(fields.Commits) > 3 {
		t.Errorf("commits: got %d, want at most 3", len(fields.Commits))
	}
}

// verify owns tree_dirty because it is the module that vetoes on it. Two
// reporters asserting the same thing invites them to disagree.
func TestReportNeverAssertsTreeDirty(t *testing.T) {
	dir := gitRepo(t)
	write(t, dir, "dirty.txt", "uncommitted\n")

	m := newReport(t, module.Config{})
	fields, err := m.Report(context.Background(), fakeSession{workspace: dir})
	if err != nil {
		t.Fatal(err)
	}
	if fields.TreeDirty != nil {
		t.Error("report must leave tree_dirty to verify, which is the module that vetoes on it")
	}
	// It still notices the file, so the two are consistent about the facts.
	if len(fields.FilesTouched) == 0 {
		t.Error("the uncommitted file should still be reported as touched")
	}
}

// A repository with no commits has no HEAD to diff against.
func TestRepoWithNoCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	dir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git init: %v\n%s", err, out)
	}
	write(t, dir, "new.txt", "hello\n")

	m := newReport(t, module.Config{})
	fields, err := m.Report(context.Background(), fakeSession{workspace: dir})
	if err != nil {
		t.Fatalf("an unborn repository must not be an error: %v", err)
	}
	found := false
	for _, f := range fields.FilesTouched {
		if f == "new.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("an untracked file should be listed even with no HEAD, got %v", fields.FilesTouched)
	}
}

func TestImplementsReporter(t *testing.T) {
	var m any = &Module{}
	if _, ok := m.(module.Module); !ok {
		t.Error("not a Module")
	}
	if _, ok := m.(module.Reporter); !ok {
		t.Error("not a Reporter")
	}
}
