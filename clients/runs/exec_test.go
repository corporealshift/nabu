package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
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
		{"worktree", "", func(g GitCLI) error { return g.AddBranchWorktree(ctx, "C:/c", "C:/w", "rho/x-1", "main") },
			[][]string{{"git", "-C", "C:/c", "worktree", "add", "-B", "rho/x-1", "C:/w", "origin/main"}}},
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
		{"push", "", func(g GitCLI) error { return g.Push(ctx, "C:/w", "rho/x-1") },
			[][]string{{"git", "-C", "C:/w", "push", "-u", "origin", "rho/x-1"}}},
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
	run, calls := recorder("Creating pull request for rho/x-1 into main\n\nhttps://github.com/kyle/bw/pull/42\n")
	n, url, err := GHCLI{run}.CreatePR(context.Background(), "C:/w", "main", "rho/x-1", "Add x", "the body", "rho")
	if err != nil || n != 42 || url != "https://github.com/kyle/bw/pull/42" {
		t.Fatalf("CreatePR = %d, %q, %v", n, url, err)
	}
	c := (*calls)[1]
	want := []string{"gh", "pr", "create", "--base", "main", "--head", "rho/x-1", "--title", "Add x", "--body-file", "-", "--label", "rho"}
	if !reflect.DeepEqual(c.args, want) || c.stdin != "the body" || c.dir != "C:/w" {
		t.Errorf("pr create = %+v", c)
	}
	if (*calls)[0].args[1] != "label" {
		t.Errorf("the label is not made first: %q", (*calls)[0].args)
	}
}

// TestMain lets the test binary stand in for the claude CLI: with
// RHO_FAKE_CLAUDE set it answers, as a JSON result, with how much prompt it
// read on stdin. RHO_FAKE_CLAUDE_OUT replaces what it prints and
// RHO_FAKE_CLAUDE_EXIT its exit code, so it can refuse the ways the real CLI
// does; RHO_FAKE_CLAUDE_HANG makes it never answer.
func TestMain(m *testing.M) {
	if os.Getenv("RHO_FAKE_CLAUDE") != "" {
		in, _ := io.ReadAll(os.Stdin)
		if os.Getenv("RHO_FAKE_CLAUDE_HANG") != "" {
			time.Sleep(time.Minute)
		}
		out := os.Getenv("RHO_FAKE_CLAUDE_OUT")
		if out == "" {
			b, _ := json.Marshal(map[string]any{"type": "result", "subtype": "success", "is_error": false,
				"result": fmt.Sprintf("read %d bytes, %d args", len(in), len(os.Args)-1)})
			out = string(b)
		}
		fmt.Println(out)
		code, _ := strconv.Atoi(os.Getenv("RHO_FAKE_CLAUDE_EXIT"))
		os.Exit(code)
	}
	os.Exit(m.Run())
}

// What the CLI prints for each way it answers or does not, taken from the
// real CLI's output.
func TestClaudeAsk(t *testing.T) {
	limit := `{"type":"result","subtype":"success","is_error":true,"api_error_status":429,"api_error":"usage_limit_reached","result":"You've hit your weekly limit · resets 2pm","terminal_reason":"api_error"}`
	cases := []struct {
		name, out, exit string
		want            string // the answer, when there is one
		unavailable     string // the reason Claude is not answering
		err             string // a plain error containing this
	}{
		{name: "answer", out: `{"type":"result","subtype":"success","is_error":false,"result":"  APPROVED\n","terminal_reason":"completed"}`, want: "APPROVED"},
		{name: "progress before the result", out: "{\"type\":\"system\"}\n" + `{"type":"result","subtype":"success","is_error":false,"result":"OK"}`, want: "OK"},
		{name: "usage limit", out: limit, exit: "1", unavailable: "weekly limit"},
		{name: "logged out", out: `{"type":"result","is_error":true,"api_error_status":401,"result":"Invalid API key · Please run /login"}`, exit: "1", unavailable: "/login"},
		{name: "overloaded", out: `{"type":"result","is_error":true,"api_error_status":529,"api_error":"overloaded_error"}`, exit: "1", unavailable: "overloaded_error"},
		{name: "bad model", out: `{"type":"result","is_error":true,"api_error_status":404,"result":"model not found"}`, exit: "1", err: "model not found (status 404)"},
		{name: "error with no status", out: `{"type":"result","subtype":"error_max_turns","is_error":true}`, exit: "1", err: "error_max_turns"},
		{name: "no result", out: "error: unknown option '--frob'", exit: "1", err: "unknown option"},
		{name: "empty answer", out: `{"type":"result","is_error":false,"result":"  "}`, err: "said nothing"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("RHO_FAKE_CLAUDE", "1")
			t.Setenv("RHO_FAKE_CLAUDE_OUT", c.out)
			t.Setenv("RHO_FAKE_CLAUDE_EXIT", c.exit)
			got, err := ClaudeCLI{Path: os.Args[0]}.Ask(context.Background(), t.TempDir(), "review the plan")
			var un ClaudeUnavailable
			isUn := errors.As(err, &un)
			switch {
			case c.want != "":
				if got != c.want || err != nil {
					t.Errorf("Ask = %q, %v, want %q", got, err, c.want)
				}
			case c.unavailable != "":
				if !isUn || !strings.Contains(un.Reason, c.unavailable) {
					t.Errorf("Ask err = %#v, want ClaudeUnavailable with %q", err, c.unavailable)
				}
			default:
				if err == nil || isUn || !strings.Contains(err.Error(), c.err) {
					t.Errorf("Ask err = %#v, want a plain error with %q", err, c.err)
				}
			}
		})
	}
}

// With the API unreachable the CLI prints nothing and keeps trying, so a
// timeout is Claude not answering, not a failed review.
func TestClaudeAskTimeoutIsUnavailable(t *testing.T) {
	t.Setenv("RHO_FAKE_CLAUDE", "1")
	t.Setenv("RHO_FAKE_CLAUDE_HANG", "1")
	_, err := ClaudeCLI{Path: os.Args[0], Timeout: 300 * time.Millisecond}.Ask(context.Background(), t.TempDir(), "review the plan")
	var un ClaudeUnavailable
	if !errors.As(err, &un) || !strings.Contains(un.Reason, "did not answer within 300ms") {
		t.Errorf("Ask err = %#v", err)
	}
}

// A prompt past Windows' 32K command-line limit still reaches claude, on
// stdin.
func TestClaudeAskTakesALongPrompt(t *testing.T) {
	t.Setenv("RHO_FAKE_CLAUDE", "1")
	prompt := strings.Repeat("x", 100000)
	got, err := ClaudeCLI{Path: os.Args[0]}.Ask(context.Background(), t.TempDir(), prompt)
	if err != nil || !strings.HasPrefix(got, "read 100000 bytes") {
		t.Errorf("Ask = %q, %v", got, err)
	}
}

func TestClaudeArgsAreReadOnly(t *testing.T) {
	args := ClaudeCLI{Model: "opus"}.Args()
	want := []string{"-p", "--output-format", "json", "--allowedTools", "Read", "--allowedTools", "Grep", "--allowedTools", "Glob", "--model", "opus"}
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
			if strings.HasPrefix(strings.Trim(imp.Path.Value, `"`), "github.com/corporealshift/rho/daemon") {
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
	if _, _, found, err := (GHCLI{none}).OpenPRFor(ctx, "C:/w", "rho/x-1"); found || err != nil {
		t.Errorf("no PR: found %v, %v", found, err)
	}
	if got := (*calls)[0].args; !reflect.DeepEqual(got, []string{"gh", "pr", "list", "--head", "rho/x-1", "--state", "open", "--json", "number,url"}) {
		t.Errorf("args = %q", got)
	}
	one, _ := recorder(`[{"number":5,"url":"https://github.com/kyle/x/pull/5"}]`)
	if n, url, found, err := (GHCLI{one}).OpenPRFor(ctx, "C:/w", "rho/x-1"); n != 5 || url != "https://github.com/kyle/x/pull/5" || !found || err != nil {
		t.Errorf("one PR = %d, %q, %v, %v", n, url, found, err)
	}

	run, calls := recorder("")
	if err := (GHCLI{run}).EditPR(ctx, "C:/w", 5, "Add x", "the body", "rho"); err != nil {
		t.Fatal(err)
	}
	if (*calls)[0].args[1] != "label" {
		t.Errorf("the label is not made first: %q", (*calls)[0].args)
	}
	c := (*calls)[1]
	if !reflect.DeepEqual(c.args, []string{"gh", "pr", "edit", "5", "--title", "Add x", "--body-file", "-", "--add-label", "rho"}) || c.stdin != "the body" {
		t.Errorf("edit = %+v", c)
	}
}

// A goal's runs open their PRs with no label, and the runner merges them.
func TestNoLabelAndMerge(t *testing.T) {
	ctx := context.Background()
	run, calls := recorder("https://github.com/kyle/bw/pull/42\n")
	if _, _, err := (GHCLI{run}).CreatePR(ctx, "C:/w", "rho/goal-x", "rho/x-1", "Add x", "the body", ""); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || !reflect.DeepEqual((*calls)[0].args, []string{"gh", "pr", "create", "--base", "rho/goal-x", "--head", "rho/x-1", "--title", "Add x", "--body-file", "-"}) {
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
