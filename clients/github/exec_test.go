package github

import (
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type recorded struct {
	args  []string
	stdin string
}

func recorder(out string) (Runner, *[]recorded) {
	var calls []recorded
	return func(_ context.Context, _ string, stdin []byte, name string, args ...string) ([]byte, error) {
		calls = append(calls, recorded{append([]string{name}, args...), string(stdin)})
		return []byte(out), nil
	}, &calls
}

func TestExecArgs(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name  string
		out   string
		call  func(r Runner) error
		want  []string
		stdin string
	}{
		{name: "open prs", out: "[]",
			call: func(r Runner) error { _, err := GH{r}.OpenPRs(ctx, "kyle/bw"); return err },
			want: []string{"gh", "pr", "list", "--repo", "kyle/bw", "--state", "open", "--limit", "100", "--json", prFields}},
		{name: "post review",
			call: func(r Runner) error {
				return GH{r}.PostReview(ctx, "kyle/bw", 7, ReviewPost{CommitID: "abc", Event: "COMMENT", Body: "b " + Marker})
			},
			want:  []string{"gh", "api", "repos/kyle/bw/pulls/7/reviews", "--method", "POST", "--input", "-"},
			stdin: `{"commit_id":"abc","body":"b <!-- nabu -->","event":"COMMENT"}`},
		{name: "fetch",
			call: func(r Runner) error { return GitCLI{r}.FetchPR(ctx, "C:/bw", 7, "main") },
			want: []string{"git", "-C", "C:/bw", "fetch", "origin", "pull/7/head", "+refs/heads/main:refs/remotes/origin/main"}},
		{name: "add worktree",
			call: func(r Runner) error { return GitCLI{r}.AddWorktree(ctx, "C:/bw", "C:/wt", "abc") },
			want: []string{"git", "-C", "C:/bw", "worktree", "add", "--detach", "C:/wt", "abc"}},
		{name: "diff",
			call: func(r Runner) error { _, err := GitCLI{r}.Diff(ctx, "C:/wt", "main", "abc"); return err },
			want: []string{"git", "-C", "C:/wt", "diff", "origin/main...abc"}},
		{name: "remove worktree",
			call: func(r Runner) error { return GitCLI{r}.RemoveWorktree(ctx, "C:/bw", "C:/wt") },
			want: []string{"git", "-C", "C:/bw", "worktree", "remove", "--force", "C:/wt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, calls := recorder(tt.out)
			if err := tt.call(r); err != nil {
				t.Fatal(err)
			}
			if len(*calls) != 1 {
				t.Fatalf("calls = %d", len(*calls))
			}
			c := (*calls)[0]
			if !reflect.DeepEqual(c.args, tt.want) {
				t.Errorf("args =\n %q\nwant\n %q", c.args, tt.want)
			}
			if c.stdin != tt.stdin {
				t.Errorf("stdin = %s, want %s", c.stdin, tt.stdin)
			}
		})
	}
}

func TestOpenPRsDecodesGh(t *testing.T) {
	out, _ := json.Marshal([]map[string]any{{
		"number": 7, "headRefOid": "abc", "headRefName": "feat", "baseRefName": "main",
		"isDraft": true, "isCrossRepository": true, "title": "T", "body": "B",
	}})
	r, _ := recorder(string(out))
	prs, err := GH{r}.OpenPRs(context.Background(), "kyle/bw")
	if err != nil {
		t.Fatal(err)
	}
	want := []PR{{Number: 7, HeadSHA: "abc", HeadRef: "feat", BaseRef: "main", Title: "T", Body: "B", Draft: true, Fork: true}}
	if !reflect.DeepEqual(prs, want) {
		t.Errorf("prs = %+v", prs)
	}
}

func TestReviewPrompt(t *testing.T) {
	first := ReviewPrompt("kyle/bw", pr7, nil)
	for _, s := range []string{`"Add the thing"`, "It adds the thing.", "git diff origin/main...abc1234def", "Change no files", "do not use the ask tool", "```json", `"comments"`} {
		if !strings.Contains(first, s) {
			t.Errorf("prompt lacks %q:\n%s", s, first)
		}
	}
	if strings.Contains(first, "reviewed this PR before") {
		t.Error("a first review mentions an earlier one")
	}

	again := ReviewPrompt("kyle/bw", pr7, &Reviewed{SHA: "0ld5ha0000", Summary: "Missing a test."})
	for _, s := range []string{"reviewed this PR before, at 0ld5ha0", "Missing a test.", "git diff 0ld5ha0000..abc1234def"} {
		if !strings.Contains(again, s) {
			t.Errorf("second prompt lacks %q", s)
		}
	}

	long := pr7
	long.Body = strings.Repeat("x", maxBody+10)
	if p := ReviewPrompt("kyle/bw", long, nil); strings.Contains(p, strings.Repeat("x", maxBody+1)) || !strings.Contains(p, "cut at 8000") {
		t.Error("a long description was not cut")
	}
}

// TestBoundary keeps the watcher a client: it may use goclient and protocol,
// never the daemon's insides.
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
