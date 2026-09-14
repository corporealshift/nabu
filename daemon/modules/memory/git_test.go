package memory

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
)

// needGit skips when git is unavailable. A machine without git must still be
// able to run the rest of the suite.
func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

// gitModule builds a module with versioning on, rooted in a temp directory so
// no test can commit anywhere near the developer's own repositories.
func gitModule(t *testing.T) *Module {
	t.Helper()
	needGit(t)
	m := &Module{Root: t.TempDir()}
	if err := m.Init(nil, module.Config{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return m
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func TestInitCreatesARepository(t *testing.T) {
	m := gitModule(t)
	out := gitOut(t, m.Root, "rev-parse", "--is-inside-work-tree")
	if strings.TrimSpace(out) != "true" {
		t.Fatalf("the memory directory is not a repository: %q", out)
	}
}

func TestInitLeavesAnExistingRepositoryAlone(t *testing.T) {
	m := gitModule(t)
	// A marker only a second init would disturb.
	first := gitOut(t, m.Root, "rev-parse", "--absolute-git-dir")

	again := &Module{Root: m.Root}
	if err := again.Init(nil, module.Config{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	second := gitOut(t, m.Root, "rev-parse", "--absolute-git-dir")
	if first != second {
		t.Errorf("the repository was reinitialised: %q then %q", first, second)
	}
}

func TestSessionThatWroteLeavesOneCommit(t *testing.T) {
	m := gitModule(t)
	s := newSession(t, "repo-abc123")

	if _, err := run(t, m, s, "memory.save", `{
		"scope":"global","name":"first","type":"user","description":"a fact","body":"x"
	}`); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, m, s, "memory.save", `{
		"scope":"workspace","name":"second","type":"project","description":"another fact","body":"y"
	}`); err != nil {
		t.Fatal(err)
	}

	m.SessionEnd(context.Background(), s)

	log := gitOut(t, m.Root, "log", "--oneline")
	lines := strings.Split(strings.TrimSpace(log), "\n")
	if len(lines) != 1 {
		t.Fatalf("two saves produced %d commits, want one per session:\n%s", len(lines), log)
	}
	// The message has to say which session taught it this, and what changed.
	if !strings.Contains(log, "2 saved") {
		t.Errorf("the message does not say what changed: %q", log)
	}
	if !strings.Contains(log, s.ID()) {
		t.Errorf("the message does not name the session: %q", log)
	}

	// Both files are actually in the commit, not just one store.
	files := gitOut(t, m.Root, "show", "--name-only", "--format=", "HEAD")
	for _, want := range []string{"first.md", "second.md"} {
		if !strings.Contains(files, want) {
			t.Errorf("%s is not in the commit:\n%s", want, files)
		}
	}
}

func TestSessionThatWroteNothingCommitsNothing(t *testing.T) {
	m := gitModule(t)
	s := newSession(t, "repo-abc123")

	if _, err := run(t, m, s, "memory.recall", `{"query":"anything"}`); err != nil {
		t.Fatal(err)
	}
	m.SessionEnd(context.Background(), s)

	cmd := exec.Command("git", "log", "--oneline")
	cmd.Dir = m.Root
	if out, err := cmd.CombinedOutput(); err == nil && strings.TrimSpace(string(out)) != "" {
		t.Errorf("a read-only session committed:\n%s", out)
	}
}

func TestForgetIsCommittedToo(t *testing.T) {
	m := gitModule(t)
	s := newSession(t, "repo-abc123")
	if _, err := run(t, m, s, "memory.save", `{
		"scope":"global","name":"doomed","type":"user","description":"d","body":"x"
	}`); err != nil {
		t.Fatal(err)
	}
	m.SessionEnd(context.Background(), s)

	next := newSession(t, "repo-abc123")
	if _, err := run(t, m, next, "memory.forget", `{"name":"doomed"}`); err != nil {
		t.Fatal(err)
	}
	m.SessionEnd(context.Background(), next)

	log := gitOut(t, m.Root, "log", "--oneline")
	if !strings.Contains(log, "1 forgotten") {
		t.Errorf("the deletion was not committed:\n%s", log)
	}
	// Git keeps the history, which is the whole reason forget may delete.
	if !strings.Contains(gitOut(t, m.Root, "log", "--oneline", "--", "global/doomed.md"), "1 saved") {
		t.Error("the forgotten memory has no history left")
	}
}

func TestMemoryWorksWithoutGit(t *testing.T) {
	// NoGit is what a machine without git resolves to: memory is the product,
	// versioning is a convenience on top of it.
	m := &Module{Root: t.TempDir(), NoGit: true}
	if err := m.Init(nil, module.Config{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	s := newSession(t, "repo-abc123")

	if _, err := run(t, m, s, "memory.save", `{
		"scope":"global","name":"still-works","type":"user","description":"a fact","body":"kafka"
	}`); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := run(t, m, s, "memory.recall", `{"query":"kafka"}`)
	if err != nil || !strings.Contains(out, "still-works") {
		t.Fatalf("recall: %v\n%s", err, out)
	}
	if _, err := run(t, m, s, "memory.forget", `{"name":"still-works"}`); err != nil {
		t.Fatalf("forget: %v", err)
	}
	// SessionEnd must be silent, not a panic and not an error.
	m.SessionEnd(context.Background(), s)
}
