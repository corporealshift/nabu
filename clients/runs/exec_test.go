package runs

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type call struct {
	dir   string
	args  []string
	stdin string
}

// recorder answers every command with out and records it.
func recorder(out string) (func(context.Context, string, []byte, string, ...string) ([]byte, error), *[]call) {
	var calls []call
	return func(_ context.Context, dir string, stdin []byte, name string, args ...string) ([]byte, error) {
		calls = append(calls, call{dir, append([]string{name}, args...), string(stdin)})
		return []byte(out), nil
	}, &calls
}

func TestExecArgs(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		out  string
		call func(g GitCLI) error
		want [][]string
	}{
		{"default branch", "origin/trunk\n", func(g GitCLI) error { b, err := g.DefaultBranch(ctx, "C:/c"); assertEq(t, b, "trunk"); return err },
			[][]string{{"git", "-C", "C:/c", "symbolic-ref", "--short", "refs/remotes/origin/HEAD"}}},
		{"fetch", "", func(g GitCLI) error { return g.Fetch(ctx, "C:/c") }, [][]string{{"git", "-C", "C:/c", "fetch", "origin"}}},
		{"worktree", "", func(g GitCLI) error { return g.AddBranchWorktree(ctx, "C:/c", "C:/w", "nabu/x-1", "main") },
			[][]string{{"git", "-C", "C:/c", "worktree", "add", "-B", "nabu/x-1", "C:/w", "origin/main"}}},
		{"head", "abc\n", func(g GitCLI) error { h, err := g.Head(ctx, "C:/w"); assertEq(t, h, "abc"); return err },
			[][]string{{"git", "-C", "C:/w", "rev-parse", "HEAD"}}},
		{"changed", "a.go\nb/c.go\n", func(g GitCLI) error {
			c, err := g.Changed(ctx, "C:/w", "abc")
			if !reflect.DeepEqual(c, []string{"a.go", "b/c.go"}) {
				t.Errorf("changed = %q", c)
			}
			return err
		}, [][]string{{"git", "-C", "C:/w", "diff", "--name-only", "abc", "HEAD"}}},
		{"dirty", " M a.go\n?? new dir/x.go\n", func(g GitCLI) error {
			d, err := g.Dirty(ctx, "C:/w")
			if !reflect.DeepEqual(d, []string{"a.go", "new dir/x.go"}) {
				t.Errorf("dirty = %q", d)
			}
			return err
		}, [][]string{{"git", "-C", "C:/w", "status", "--porcelain", "--untracked-files=all"}}},
		{"reset", "", func(g GitCLI) error { return g.ResetHard(ctx, "C:/w", "abc") },
			[][]string{{"git", "-C", "C:/w", "reset", "--hard", "abc"}, {"git", "-C", "C:/w", "clean", "-fd"}}},
		{"commit", "", func(g GitCLI) error { return g.Commit(ctx, "C:/w", "run: plan", "p.md") },
			[][]string{{"git", "-C", "C:/w", "add", "--", "p.md"}, {"git", "-C", "C:/w", "commit", "-q", "-m", "run: plan"}}},
		{"remove", "", func(g GitCLI) error { return g.Remove(ctx, "C:/w", "run: gone", "r.md") },
			[][]string{{"git", "-C", "C:/w", "rm", "-q", "--", "r.md"}, {"git", "-C", "C:/w", "commit", "-q", "-m", "run: gone"}}},
		{"push", "", func(g GitCLI) error { return g.Push(ctx, "C:/w", "nabu/x-1") },
			[][]string{{"git", "-C", "C:/w", "push", "-u", "origin", "nabu/x-1"}}},
		{"diffed", "", func(g GitCLI) error { _, err := g.Diffed(ctx, "C:/w", "main"); return err },
			[][]string{{"git", "-C", "C:/w", "diff", "--name-only", "origin/main...HEAD"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run, calls := recorder(tt.out)
			if err := tt.call(GitCLI{run}); err != nil {
				t.Fatal(err)
			}
			var got [][]string
			for _, c := range *calls {
				got = append(got, c.args)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("calls =\n %q\nwant\n %q", got, tt.want)
			}
		})
	}
}

func assertEq(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDefaultBranchFallsBackToMain(t *testing.T) {
	fails := func(context.Context, string, []byte, string, ...string) ([]byte, error) {
		return nil, os.ErrNotExist
	}
	if b, err := (GitCLI{fails}).DefaultBranch(context.Background(), "C:/c"); b != "main" || err != nil {
		t.Errorf("DefaultBranch = %q, %v", b, err)
	}
}

func TestCreatePR(t *testing.T) {
	run, calls := recorder("Creating pull request for nabu/x-1 into main\n\nhttps://github.com/kyle/bw/pull/42\n")
	n, url, err := GHCLI{run}.CreatePR(context.Background(), "C:/w", "main", "nabu/x-1", "Add x", "the body", "nabu")
	if err != nil || n != 42 || url != "https://github.com/kyle/bw/pull/42" {
		t.Fatalf("CreatePR = %d, %q, %v", n, url, err)
	}
	c := (*calls)[1]
	want := []string{"gh", "pr", "create", "--base", "main", "--head", "nabu/x-1", "--title", "Add x", "--body-file", "-", "--label", "nabu"}
	if !reflect.DeepEqual(c.args, want) || c.stdin != "the body" || c.dir != "C:/w" {
		t.Errorf("pr create = %+v", c)
	}
	if (*calls)[0].args[1] != "label" {
		t.Errorf("the label is not made first: %q", (*calls)[0].args)
	}
}

func TestClaudeArgsAreReadOnly(t *testing.T) {
	args := ClaudeCLI{Model: "opus"}.Args("review this")
	want := []string{"-p", "review this", "--allowedTools", "Read", "--allowedTools", "Grep", "--allowedTools", "Glob", "--model", "opus"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("args = %q", args)
	}
	if (ClaudeCLI{Path: filepath.Join(t.TempDir(), "claude")}).Available() != true {
		t.Error("a configured path is taken as given")
	}
}

func TestBashVerify(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	write := func(name, body string) string {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return name
	}
	ctx := context.Background()
	if ok, out, err := (Bash{}).Verify(ctx, dir, write("pass.sh", "echo all good\n"), time.Minute); !ok || err != nil || !strings.Contains(out, "all good") {
		t.Errorf("pass = %v, %q, %v", ok, out, err)
	}
	if ok, out, err := (Bash{}).Verify(ctx, dir, write("fail.sh", "echo broken >&2\nexit 3\n"), time.Minute); ok || err != nil || !strings.Contains(out, "broken") {
		t.Errorf("fail = %v, %q, %v", ok, out, err)
	}
	if _, out, _ := (Bash{}).Verify(ctx, dir, write("long.sh", "for i in $(seq 1 5000); do echo line $i of a long run; done\n"), time.Minute); len(out) > maxCheckOutput+100 || !strings.Contains(out, "line 5000") {
		t.Errorf("long output kept %d bytes, or lost its end", len(out))
	}
	ok, out, err := (Bash{}).Verify(ctx, dir, write("slow.sh", "sleep 30\n"), 500*time.Millisecond)
	if ok || err != nil || !strings.Contains(out, "stopped after") {
		t.Errorf("slow = %v, %q, %v", ok, out, err)
	}
}

// TestBoundary keeps the runner a client.
func TestBoundary(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(fset, f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			if strings.HasPrefix(strings.Trim(imp.Path.Value, `"`), "github.com/corporealshift/nabu/daemon") {
				t.Errorf("%s imports %s", f, imp.Path.Value)
			}
		}
	}
}

func TestCommentIssue(t *testing.T) {
	run, calls := recorder("")
	if err := (GHCLI{run}).CommentIssue(context.Background(), "C:/w", 12, "done"); err != nil {
		t.Fatal(err)
	}
	c := (*calls)[0]
	if !reflect.DeepEqual(c.args, []string{"gh", "issue", "comment", "12", "--body-file", "-"}) || c.stdin != "done" || c.dir != "C:/w" {
		t.Errorf("call = %+v", c)
	}
}

func TestOpenPRForAndEditPR(t *testing.T) {
	ctx := context.Background()
	none, calls := recorder("[]")
	if _, _, found, err := (GHCLI{none}).OpenPRFor(ctx, "C:/w", "nabu/x-1"); found || err != nil {
		t.Errorf("no PR: found %v, %v", found, err)
	}
	if got := (*calls)[0].args; !reflect.DeepEqual(got, []string{"gh", "pr", "list", "--head", "nabu/x-1", "--state", "open", "--json", "number,url"}) {
		t.Errorf("args = %q", got)
	}
	one, _ := recorder(`[{"number":5,"url":"https://github.com/kyle/x/pull/5"}]`)
	if n, url, found, err := (GHCLI{one}).OpenPRFor(ctx, "C:/w", "nabu/x-1"); n != 5 || url != "https://github.com/kyle/x/pull/5" || !found || err != nil {
		t.Errorf("one PR = %d, %q, %v, %v", n, url, found, err)
	}

	run, calls := recorder("")
	if err := (GHCLI{run}).EditPR(ctx, "C:/w", 5, "Add x", "the body", "nabu"); err != nil {
		t.Fatal(err)
	}
	if (*calls)[0].args[1] != "label" {
		t.Errorf("the label is not made first: %q", (*calls)[0].args)
	}
	c := (*calls)[1]
	if !reflect.DeepEqual(c.args, []string{"gh", "pr", "edit", "5", "--title", "Add x", "--body-file", "-", "--add-label", "nabu"}) || c.stdin != "the body" {
		t.Errorf("edit = %+v", c)
	}
}

// A goal's runs open their PRs with no label, and the runner merges them.
func TestNoLabelAndMerge(t *testing.T) {
	ctx := context.Background()
	run, calls := recorder("https://github.com/kyle/bw/pull/42\n")
	if _, _, err := (GHCLI{run}).CreatePR(ctx, "C:/w", "nabu/goal-x", "nabu/x-1", "Add x", "the body", ""); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || !reflect.DeepEqual((*calls)[0].args, []string{"gh", "pr", "create", "--base", "nabu/goal-x", "--head", "nabu/x-1", "--title", "Add x", "--body-file", "-"}) {
		t.Errorf("create with no label = %q", *calls)
	}

	run, calls = recorder("")
	if err := (GHCLI{run}).EditPR(ctx, "C:/w", 5, "Add x", "the body", ""); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || !reflect.DeepEqual((*calls)[0].args, []string{"gh", "pr", "edit", "5", "--title", "Add x", "--body-file", "-"}) {
		t.Errorf("edit with no label = %q", *calls)
	}

	run, calls = recorder("")
	if err := (GHCLI{run}).MergePR(ctx, "C:/w", 5); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual((*calls)[0].args, []string{"gh", "pr", "merge", "5", "--squash"}) || (*calls)[0].dir != "C:/w" {
		t.Errorf("merge = %+v", (*calls)[0])
	}
}
