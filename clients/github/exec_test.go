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
		{name: "reply in a thread",
			call:  func(r Runner) error { return GH{r}.ReplyTo(ctx, "kyle/bw", 7, 42, "ok "+Marker) },
			want:  []string{"gh", "api", "repos/kyle/bw/pulls/7/comments/42/replies", "--method", "POST", "--input", "-"},
			stdin: `{"body":"ok <!-- nabu -->"}`},
		{name: "comment",
			call:  func(r Runner) error { return GH{r}.Comment(ctx, "kyle/bw", 7, "ok") },
			want:  []string{"gh", "api", "repos/kyle/bw/issues/7/comments", "--method", "POST", "--input", "-"},
			stdin: `{"body":"ok"}`},
		{name: "fetch branch",
			call: func(r Runner) error { return GitCLI{r}.FetchBranch(ctx, "C:/bw", "feat/x") },
			want: []string{"git", "-C", "C:/bw", "fetch", "origin", "+refs/heads/feat/x:refs/remotes/origin/feat/x"}},
		{name: "head",
			call: func(r Runner) error { _, err := GitCLI{r}.Head(ctx, "C:/wt"); return err },
			want: []string{"git", "-C", "C:/wt", "rev-parse", "HEAD"}},
		{name: "push",
			call: func(r Runner) error { return GitCLI{r}.Push(ctx, "C:/wt", "feat/x") },
			want: []string{"git", "-C", "C:/wt", "push", "origin", "HEAD:refs/heads/feat/x"}},
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
		"labels": []map[string]any{{"name": "nabu", "color": "fff"}},
	}})
	r, _ := recorder(string(out))
	prs, err := GH{r}.OpenPRs(context.Background(), "kyle/bw")
	if err != nil {
		t.Fatal(err)
	}
	want := []PR{{Number: 7, HeadSHA: "abc", HeadRef: "feat", BaseRef: "main", Title: "T", Body: "B", Draft: true, Fork: true, Labels: []string{"nabu"}}}
	if !reflect.DeepEqual(prs, want) {
		t.Errorf("prs = %+v", prs)
	}
}

func TestReviewPrompt(t *testing.T) {
	first := ReviewPrompt("kyle/bw", pr7, nil, nil)
	for _, s := range []string{`"Add the thing"`, "It adds the thing.", "git diff origin/main...abc1234def", "Change no files", "do not use the ask tool", "```json", `"comments"`} {
		if !strings.Contains(first, s) {
			t.Errorf("prompt lacks %q:\n%s", s, first)
		}
	}
	if strings.Contains(first, "reviewed this PR before") {
		t.Error("a first review mentions an earlier one")
	}

	again := ReviewPrompt("kyle/bw", pr7, &Reviewed{SHA: "0ld5ha0000", Summary: "Missing a test."}, nil)
	for _, s := range []string{"reviewed this PR before, at 0ld5ha0", "Missing a test.", "git diff 0ld5ha0000..abc1234def"} {
		if !strings.Contains(again, s) {
			t.Errorf("second prompt lacks %q", s)
		}
	}

	long := pr7
	long.Body = strings.Repeat("x", maxBody+10)
	if p := ReviewPrompt("kyle/bw", long, nil, nil); strings.Contains(p, strings.Repeat("x", maxBody+1)) || !strings.Contains(p, "cut at 8000") {
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

// A pending review is visible to its author's login, which is the login gh
// uses; neither it nor its comments count until it is submitted.
func TestPRCommentsReadsAllThreeKindsAndSkipsPendingReviews(t *testing.T) {
	pages := map[string]string{
		"repos/kyle/bw/pulls/7/reviews": `[{"id":5,"user":{"login":"kyle"},"body":"ok","state":"COMMENTED","submitted_at":"2026-09-29T10:00:00Z","html_url":"r5"},` +
			`{"id":6,"user":{"login":"kyle"},"body":"draft","state":"PENDING"}]`,
		// Two pages, as --paginate prints them.
		"repos/kyle/bw/pulls/7/comments": `[{"id":10,"pull_request_review_id":5,"user":{"login":"kyle"},"body":"here","path":"a.go","line":3,"diff_hunk":"@@","created_at":"2026-09-29T10:00:00Z","html_url":"c10"}]` +
			`[{"id":11,"pull_request_review_id":5,"user":{"login":"kyle"},"body":"outdated","path":"a.go","line":null,"original_line":8,"in_reply_to_id":10,"created_at":"2026-09-29T10:01:00Z"},` +
			`{"id":12,"pull_request_review_id":6,"user":{"login":"kyle"},"body":"pending","path":"a.go","line":4}]`,
		"repos/kyle/bw/issues/7/comments": `[{"id":900,"user":{"login":"kyle"},"body":"readme","created_at":"2026-09-29T10:02:00Z","html_url":"i900"}]`,
	}
	run := func(_ context.Context, _ string, _ []byte, name string, args ...string) ([]byte, error) {
		if name != "gh" || len(args) != 3 || args[0] != "api" || args[1] != "--paginate" {
			t.Fatalf("unexpected call %s %q", name, args)
		}
		return []byte(pages[args[2]]), nil
	}
	got, err := GH{run}.PRComments(context.Background(), "kyle/bw", 7)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, c := range got {
		keys = append(keys, key(c))
	}
	if want := []string{"review-5", "line-10", "line-11", "issue-900"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("comments = %v, want %v", keys, want)
	}
	if got[2].Line != 8 || got[2].InReplyTo != 10 || got[1].Path != "a.go" || got[1].DiffHunk != "@@" || got[3].URL != "i900" {
		t.Errorf("fields: %+v", got)
	}
}

func TestIssueCalls(t *testing.T) {
	run, calls := recorder(`[{"number":12,"title":"T","body":"B","url":"u"}]`)
	issues, err := GH{run}.OpenIssues(context.Background(), "kyle/bw", "nabu")
	if err != nil || !reflect.DeepEqual(issues, []Issue{{Number: 12, Title: "T", Body: "B", URL: "u"}}) {
		t.Fatalf("issues = %+v, %v", issues, err)
	}
	want := []string{"gh", "issue", "list", "--repo", "kyle/bw", "--label", "nabu", "--state", "open", "--limit", "100", "--json", "number,title,body,url"}
	if !reflect.DeepEqual((*calls)[0].args, want) {
		t.Errorf("args = %q", (*calls)[0].args)
	}

	run, calls = recorder(`[{"id":5,"user":{"login":"kyle"},"body":"more","created_at":"2026-10-01T10:00:00Z","html_url":"c5"}]`)
	cs, err := GH{run}.IssueComments(context.Background(), "kyle/bw", 12)
	if err != nil || len(cs) != 1 || cs[0].ID != 5 || cs[0].Author != "kyle" || cs[0].Kind != CommentIssue {
		t.Fatalf("comments = %+v, %v", cs, err)
	}
	if got := (*calls)[0].args; !reflect.DeepEqual(got, []string{"gh", "api", "--paginate", "repos/kyle/bw/issues/12/comments"}) {
		t.Errorf("args = %q", got)
	}
}
