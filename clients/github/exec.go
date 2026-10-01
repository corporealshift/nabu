package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Runner runs a program and returns its stdout. stdin may be nil.
type Runner func(ctx context.Context, dir string, stdin []byte, name string, args ...string) ([]byte, error)

// ExecRunner is the Runner for real programs. A failure carries the
// program's stderr, which is where gh and git say what went wrong.
func ExecRunner(ctx context.Context, dir string, stdin []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && msg != "" {
			return nil, fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
		}
		return nil, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return stdout.Bytes(), nil
}

// GH is GitHub over the gh program, using whatever login gh already has.
type GH struct{ Run Runner }

// prFields is what OpenPRs asks gh for. Naming them is what makes gh answer
// in JSON.
const prFields = "number,headRefOid,headRefName,baseRefName,isDraft,isCrossRepository,title,body,labels"

func (g GH) OpenPRs(ctx context.Context, repo string) ([]PR, error) {
	out, err := g.Run(ctx, "", nil, "gh", "pr", "list", "--repo", repo, "--state", "open", "--limit", "100", "--json", prFields)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Number            int    `json:"number"`
		HeadRefOid        string `json:"headRefOid"`
		HeadRefName       string `json:"headRefName"`
		BaseRefName       string `json:"baseRefName"`
		IsDraft           bool   `json:"isDraft"`
		IsCrossRepository bool   `json:"isCrossRepository"`
		Title             string `json:"title"`
		Body              string `json:"body"`
		Labels            []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("gh pr list: %w", err)
	}
	prs := make([]PR, 0, len(raw))
	for _, r := range raw {
		p := PR{
			Number: r.Number, HeadSHA: r.HeadRefOid, HeadRef: r.HeadRefName, BaseRef: r.BaseRefName,
			Title: r.Title, Body: r.Body, Draft: r.IsDraft, Fork: r.IsCrossRepository,
		}
		for _, l := range r.Labels {
			p.Labels = append(p.Labels, l.Name)
		}
		prs = append(prs, p)
	}
	return prs, nil
}

func (g GH) PostReview(ctx context.Context, repo string, number int, post ReviewPost) error {
	_, err := g.Run(ctx, "", post.JSON(false), "gh", "api", "repos/"+repo+"/pulls/"+strconv.Itoa(number)+"/reviews",
		"--method", "POST", "--input", "-")
	return err
}

// GitCLI is Git over the git program.
type GitCLI struct{ Run Runner }

func (g GitCLI) FetchPR(ctx context.Context, clone string, number int, base string) error {
	// The base gets a remote-tracking ref, so the diff can name it; the head
	// only needs its objects, since the worktree is made from its SHA.
	_, err := g.Run(ctx, "", nil, "git", "-C", clone, "fetch", "origin",
		"pull/"+strconv.Itoa(number)+"/head",
		"+refs/heads/"+base+":refs/remotes/origin/"+base)
	return err
}

func (g GitCLI) AddWorktree(ctx context.Context, clone, path, sha string) error {
	_, err := g.Run(ctx, "", nil, "git", "-C", clone, "worktree", "add", "--detach", path, sha)
	return err
}

func (g GitCLI) Diff(ctx context.Context, dir, base, sha string) (string, error) {
	out, err := g.Run(ctx, "", nil, "git", "-C", dir, "diff", "origin/"+base+"..."+sha)
	return string(out), err
}

func (g GitCLI) RemoveWorktree(ctx context.Context, clone, path string) error {
	_, err := g.Run(ctx, "", nil, "git", "-C", clone, "worktree", "remove", "--force", path)
	return err
}

// ghUser is the part of a GitHub user a comment names.
type ghUser struct {
	Login string `json:"login"`
}

// PRComments reads the three endpoints a PR's comments come from. gh prints
// one JSON array per page under --paginate, so each is decoded as a stream.
func (g GH) PRComments(ctx context.Context, repo string, number int) ([]Comment, error) {
	n := strconv.Itoa(number)
	var reviews []struct {
		ID          int64     `json:"id"`
		User        ghUser    `json:"user"`
		Body        string    `json:"body"`
		State       string    `json:"state"`
		SubmittedAt time.Time `json:"submitted_at"`
		HTMLURL     string    `json:"html_url"`
	}
	if err := g.pages(ctx, "repos/"+repo+"/pulls/"+n+"/reviews", &reviews); err != nil {
		return nil, err
	}
	var lines []struct {
		ID          int64     `json:"id"`
		ReviewID    int64     `json:"pull_request_review_id"`
		User        ghUser    `json:"user"`
		Body        string    `json:"body"`
		Path        string    `json:"path"`
		Line        *int      `json:"line"`
		OrigLine    *int      `json:"original_line"`
		DiffHunk    string    `json:"diff_hunk"`
		InReplyToID int64     `json:"in_reply_to_id"`
		CreatedAt   time.Time `json:"created_at"`
		HTMLURL     string    `json:"html_url"`
	}
	if err := g.pages(ctx, "repos/"+repo+"/pulls/"+n+"/comments", &lines); err != nil {
		return nil, err
	}
	var issue []struct {
		ID        int64     `json:"id"`
		User      ghUser    `json:"user"`
		Body      string    `json:"body"`
		CreatedAt time.Time `json:"created_at"`
		HTMLURL   string    `json:"html_url"`
	}
	if err := g.pages(ctx, "repos/"+repo+"/issues/"+n+"/comments", &issue); err != nil {
		return nil, err
	}

	// A review not yet submitted is visible to its author's own login, which
	// is the login gh uses. Neither it nor its comments have been said yet.
	pending := map[int64]bool{}
	var out []Comment
	for _, r := range reviews {
		if r.State == "PENDING" {
			pending[r.ID] = true
			continue
		}
		out = append(out, Comment{Kind: CommentReview, ID: r.ID, Author: r.User.Login, Body: r.Body, Created: r.SubmittedAt, URL: r.HTMLURL})
	}
	for _, l := range lines {
		if pending[l.ReviewID] {
			continue
		}
		c := Comment{Kind: CommentLine, ID: l.ID, Author: l.User.Login, Body: l.Body, Path: l.Path,
			DiffHunk: l.DiffHunk, InReplyTo: l.InReplyToID, Created: l.CreatedAt, URL: l.HTMLURL}
		// An outdated comment has no line in the current diff; the line it
		// was written on still says where it was.
		if l.Line != nil {
			c.Line = *l.Line
		} else if l.OrigLine != nil {
			c.Line = *l.OrigLine
		}
		out = append(out, c)
	}
	for _, c := range issue {
		out = append(out, Comment{Kind: CommentIssue, ID: c.ID, Author: c.User.Login, Body: c.Body, Created: c.CreatedAt, URL: c.HTMLURL})
	}
	return out, nil
}

// pages fetches every page of a list endpoint into out, a pointer to a slice.
func (g GH) pages(ctx context.Context, path string, out any) error {
	raw, err := g.Run(ctx, "", nil, "gh", "api", "--paginate", path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	all := reflect.ValueOf(out).Elem()
	for dec.More() {
		page := reflect.New(all.Type())
		if err := dec.Decode(page.Interface()); err != nil {
			return fmt.Errorf("gh api %s: %w", path, err)
		}
		all.Set(reflect.AppendSlice(all, page.Elem()))
	}
	return nil
}

func (g GH) ReplyTo(ctx context.Context, repo string, number int, root int64, body string) error {
	return g.postBody(ctx, "repos/"+repo+"/pulls/"+strconv.Itoa(number)+"/comments/"+strconv.FormatInt(root, 10)+"/replies", body)
}

func (g GH) Comment(ctx context.Context, repo string, number int, body string) error {
	return g.postBody(ctx, "repos/"+repo+"/issues/"+strconv.Itoa(number)+"/comments", body)
}

// postBody posts {"body": body} to an endpoint, without escaping the marker.
func (g GH) postBody(ctx context.Context, path, body string) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]string{"body": body}); err != nil {
		return err
	}
	_, err := g.Run(ctx, "", bytes.TrimRight(b.Bytes(), "\n"), "gh", "api", path, "--method", "POST", "--input", "-")
	return err
}

func (g GitCLI) FetchBranch(ctx context.Context, clone, ref string) error {
	_, err := g.Run(ctx, "", nil, "git", "-C", clone, "fetch", "origin", "+refs/heads/"+ref+":refs/remotes/origin/"+ref)
	return err
}

func (g GitCLI) Head(ctx context.Context, dir string) (string, error) {
	out, err := g.Run(ctx, "", nil, "git", "-C", dir, "rev-parse", "HEAD")
	return strings.TrimSpace(string(out)), err
}

func (g GitCLI) Push(ctx context.Context, dir, ref string) error {
	_, err := g.Run(ctx, "", nil, "git", "-C", dir, "push", "origin", "HEAD:refs/heads/"+ref)
	return err
}

func (g GH) OpenIssues(ctx context.Context, repo, label string) ([]Issue, error) {
	out, err := g.Run(ctx, "", nil, "gh", "issue", "list", "--repo", repo, "--label", label, "--state", "open",
		"--limit", "100", "--json", "number,title,body,url")
	if err != nil {
		return nil, err
	}
	var issues []Issue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("gh issue list: %w", err)
	}
	return issues, nil
}

func (g GH) IssueComments(ctx context.Context, repo string, number int) ([]Comment, error) {
	var raw []struct {
		ID        int64     `json:"id"`
		User      ghUser    `json:"user"`
		Body      string    `json:"body"`
		CreatedAt time.Time `json:"created_at"`
		HTMLURL   string    `json:"html_url"`
	}
	if err := g.pages(ctx, "repos/"+repo+"/issues/"+strconv.Itoa(number)+"/comments", &raw); err != nil {
		return nil, err
	}
	out := make([]Comment, 0, len(raw))
	for _, c := range raw {
		out = append(out, Comment{Kind: CommentIssue, ID: c.ID, Author: c.User.Login, Body: c.Body, Created: c.CreatedAt, URL: c.HTMLURL})
	}
	return out, nil
}
