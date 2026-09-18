package vcs

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// fakeSession gives the tools a workspace and nothing else.
type fakeSession struct{ dir string }

func (f fakeSession) ID() string                               { return "s" }
func (f fakeSession) Workspace() module.Workspace              { return module.Workspace{Path: f.dir, Key: "k"} }
func (f fakeSession) Events(*string) ([]protocol.Event, error) { return nil, nil }
func (f fakeSession) State() protocol.State                    { return protocol.State{} }
func (f fakeSession) Append(protocol.EventType, any) (protocol.Event, error) {
	return protocol.Event{}, nil
}

// repo builds a throwaway git repository with two commits and returns a module
// pointed at it. A real repository rather than canned output: the whole value of
// this module is that it parses what git actually prints, and a fixture would
// test the fixture.
func repo(t *testing.T) (*Module, fakeSession) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_CONFIG_GLOBAL=", "GIT_CONFIG_SYSTEM=")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run("init", "--initial-branch=main")
	run("config", "user.name", "Test")
	run("config", "user.email", "t@example.com")
	write("a.txt", "one\ntwo\nthree\n")
	run("add", "a.txt")
	run("commit", "-m", "first: add a.txt")
	write("a.txt", "one\ntwo changed\nthree\nfour\n")
	run("add", "a.txt")
	run("commit", "-m", "second: change a.txt")

	m := &Module{}
	if err := m.Init(nil, module.Config{}); err != nil {
		t.Fatal(err)
	}
	return m, fakeSession{dir}
}

// call runs one git op and decodes the reply.
func call(t *testing.T, m *Module, s fakeSession, args string, into any) {
	t.Helper()
	out, err := m.runGit(context.Background(), s, json.RawMessage(args))
	if err != nil {
		t.Fatalf("git %s: %v", args, err)
	}
	if err := json.Unmarshal([]byte(out), into); err != nil {
		t.Fatalf("reply is not the JSON it claims to be: %v\n%s", err, out)
	}
}

func TestStatusReportsBranchAndChanges(t *testing.T) {
	m, s := repo(t)

	t.Run("a clean tree says so", func(t *testing.T) {
		var st Status
		call(t, m, s, `{"op":"status"}`, &st)
		if st.Branch != "main" {
			t.Errorf("branch = %q, want main", st.Branch)
		}
		if !st.Clean {
			t.Errorf("a fresh repo should be clean, got %+v", st.Files)
		}
	})

	t.Run("staged and unstaged are kept apart", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(s.dir, "a.txt"), []byte("edited\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.dir, "new.txt"), []byte("new\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		var st Status
		call(t, m, s, `{"op":"status"}`, &st)

		if st.Clean {
			t.Fatal("a modified tree is not clean")
		}
		if len(st.Files) != 1 || st.Files[0].Path != "a.txt" {
			t.Fatalf("files = %+v, want just a.txt", st.Files)
		}
		// The edit is in the worktree and not the index; conflating the two is
		// exactly the mistake this shape exists to prevent.
		if st.Files[0].Worktree != "M" {
			t.Errorf("worktree = %q, want M", st.Files[0].Worktree)
		}
		if st.Files[0].Index != "." {
			t.Errorf("index = %q, want . (nothing staged)", st.Files[0].Index)
		}
		if len(st.Untracked) != 1 || st.Untracked[0] != "new.txt" {
			t.Errorf("untracked = %v, want [new.txt]", st.Untracked)
		}
	})
}

func TestLogReturnsCommits(t *testing.T) {
	m, s := repo(t)

	var got struct {
		Commits []Commit `json:"commits"`
	}
	call(t, m, s, `{"op":"log"}`, &got)

	if len(got.Commits) != 2 {
		t.Fatalf("got %d commits, want 2", len(got.Commits))
	}
	// Newest first, as git prints it.
	if got.Commits[0].Subject != "second: change a.txt" {
		t.Errorf("subject = %q", got.Commits[0].Subject)
	}
	if len(got.Commits[0].SHA) != 40 {
		t.Errorf("sha = %q, want 40 characters", got.Commits[0].SHA)
	}
	if got.Commits[0].Author != "Test" {
		t.Errorf("author = %q, want Test", got.Commits[0].Author)
	}
	if got.Commits[0].Date == "" {
		t.Error("date is empty")
	}
}

func TestLogRespectsLimit(t *testing.T) {
	m, s := repo(t)
	var got struct {
		Commits []Commit `json:"commits"`
	}
	call(t, m, s, `{"op":"log","limit":1}`, &got)
	if len(got.Commits) != 1 {
		t.Fatalf("got %d commits, want 1", len(got.Commits))
	}
}

func TestDiffCountsAndPatch(t *testing.T) {
	m, s := repo(t)
	if err := os.WriteFile(filepath.Join(s.dir, "a.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var d Diff
	call(t, m, s, `{"op":"diff"}`, &d)

	if len(d.Files) != 1 || d.Files[0].Path != "a.txt" {
		t.Fatalf("files = %+v", d.Files)
	}
	if d.Files[0].Removed == 0 {
		t.Error("removing lines should be counted")
	}
	if d.Removed != d.Files[0].Removed || d.Added != d.Files[0].Added {
		t.Error("totals should be the sum of the files")
	}
	// The counts are the usual answer, but the patch has to be there for the
	// times the model needs to read the edit itself.
	if d.Patch == "" {
		t.Error("patch is empty")
	}
}

// A binary file has no line counts. Reporting 0 would be a claim that nothing
// changed, which is a different and wrong answer from "not countable".
func TestBinaryDiffIsNotCountedAsZero(t *testing.T) {
	d := parseNumstat("-\t-\tlogo.png\n3\t1\tmain.go\n")

	if len(d.Files) != 2 {
		t.Fatalf("files = %+v", d.Files)
	}
	if !d.Files[0].Binary || d.Files[0].Added != -1 || d.Files[0].Removed != -1 {
		t.Errorf("binary file = %+v, want binary with -1 counts", d.Files[0])
	}
	// The binary file must not drag the totals.
	if d.Added != 3 || d.Removed != 1 {
		t.Errorf("totals = +%d -%d, want +3 -1", d.Added, d.Removed)
	}
}

func TestShowReturnsTheCommitAndItsDiff(t *testing.T) {
	m, s := repo(t)
	var got struct {
		Commit Commit `json:"commit"`
		Diff   Diff   `json:"diff"`
	}
	call(t, m, s, `{"op":"show","ref":"HEAD"}`, &got)

	if got.Commit.Subject != "second: change a.txt" {
		t.Errorf("subject = %q", got.Commit.Subject)
	}
	if len(got.Diff.Files) != 1 {
		t.Errorf("diff files = %+v", got.Diff.Files)
	}
}

func TestBlameAttributesLines(t *testing.T) {
	m, s := repo(t)
	var got struct {
		Path  string      `json:"path"`
		Lines []BlameLine `json:"lines"`
	}
	call(t, m, s, `{"op":"blame","path":"a.txt"}`, &got)

	if len(got.Lines) != 4 {
		t.Fatalf("got %d lines, want 4", len(got.Lines))
	}
	if got.Lines[0].Line != 1 || got.Lines[0].Text != "one" {
		t.Errorf("first line = %+v", got.Lines[0])
	}
	if got.Lines[0].Author != "Test" {
		t.Errorf("author = %q", got.Lines[0].Author)
	}
	// Line 1 is untouched since the first commit; line 2 was changed in the
	// second. Different commits, or blame is not telling us anything.
	if got.Lines[0].SHA == got.Lines[1].SHA {
		t.Error("an unchanged line and a changed one should blame to different commits")
	}
}

func TestBlameRange(t *testing.T) {
	m, s := repo(t)
	var got struct {
		Lines []BlameLine `json:"lines"`
	}
	call(t, m, s, `{"op":"blame","path":"a.txt","start":2,"end":3}`, &got)

	if len(got.Lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(got.Lines))
	}
	if got.Lines[0].Line != 2 {
		t.Errorf("first line = %d, want 2", got.Lines[0].Line)
	}
}

func TestCommitRecordsStagedWork(t *testing.T) {
	m, s := repo(t)

	t.Run("nothing staged is refused before anything happens", func(t *testing.T) {
		_, err := m.runGit(context.Background(), s, json.RawMessage(`{"op":"commit","message":"empty"}`))
		if err == nil {
			t.Fatal("committing nothing should fail")
		}
		kind, _ := module.ClassifyToolError(err)
		if kind != protocol.ToolErrorInvalidArgs {
			t.Errorf("kind = %q, want %q", kind, protocol.ToolErrorInvalidArgs)
		}
	})

	t.Run("a message is required", func(t *testing.T) {
		_, err := m.runGit(context.Background(), s, json.RawMessage(`{"op":"commit"}`))
		if err == nil {
			t.Fatal("committing without a message should fail")
		}
	})

	t.Run("staged work is committed and read back", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(s.dir, "b.txt"), []byte("b\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("git", "add", "b.txt")
		cmd.Dir = s.dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("staging: %v\n%s", err, out)
		}

		var got struct {
			Committed Commit `json:"committed"`
		}
		call(t, m, s, `{"op":"commit","message":"third: add b.txt"}`, &got)

		if got.Committed.Subject != "third: add b.txt" {
			t.Errorf("subject = %q", got.Committed.Subject)
		}
		var st Status
		call(t, m, s, `{"op":"status"}`, &st)
		if !st.Clean {
			t.Errorf("the tree should be clean after committing, got %+v", st.Files)
		}
	})

	// Staging is a decision about what belongs in the commit. A tool that swept
	// up the rest of the tree would make that decision for the model.
	t.Run("unstaged work is left out", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(s.dir, "c.txt"), []byte("c\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.dir, "a.txt"), []byte("touched\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("git", "add", "c.txt")
		cmd.Dir = s.dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("staging: %v\n%s", err, out)
		}

		var got struct {
			Committed Commit `json:"committed"`
		}
		call(t, m, s, `{"op":"commit","message":"fourth: add c.txt only"}`, &got)

		var st Status
		call(t, m, s, `{"op":"status"}`, &st)
		if len(st.Files) != 1 || st.Files[0].Path != "a.txt" {
			t.Fatalf("a.txt should still be modified and uncommitted, got %+v", st.Files)
		}
	})
}

func TestBadGitRequestsAreInvalidArgs(t *testing.T) {
	m, s := repo(t)
	cases := []struct{ name, args string }{
		{"no op", `{}`},
		{"unknown op", `{"op":"rebase"}`},
		{"blame with no path", `{"op":"blame"}`},
		{"blame with an inverted range", `{"op":"blame","path":"a.txt","start":9,"end":2}`},
		{"a log limit beyond the maximum", `{"op":"log","limit":9999}`},
		{"arguments that are not an object", `"nope"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.runGit(context.Background(), s, json.RawMessage(tc.args))
			if err == nil {
				t.Fatal("want an error")
			}
			kind, _ := module.ClassifyToolError(err)
			if kind != protocol.ToolErrorInvalidArgs {
				t.Errorf("kind = %q, want %q", kind, protocol.ToolErrorInvalidArgs)
			}
		})
	}
}

// git's own failures arrive as an exit, with the code, rather than as prose the
// model has to recognise.
func TestGitFailureCarriesItsExitCode(t *testing.T) {
	m, s := repo(t)
	_, err := m.runGit(context.Background(), s, json.RawMessage(`{"op":"show","ref":"no-such-ref"}`))
	if err == nil {
		t.Fatal("showing a ref that does not exist should fail")
	}
	kind, code := module.ClassifyToolError(err)
	if kind != protocol.ToolErrorExit {
		t.Fatalf("kind = %q, want %q", kind, protocol.ToolErrorExit)
	}
	if code == nil || *code == 0 {
		t.Fatalf("exit code = %v, want a non-zero one", code)
	}
}
