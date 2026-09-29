package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
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
const prFields = "number,headRefOid,headRefName,baseRefName,isDraft,isCrossRepository,title,body"

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
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("gh pr list: %w", err)
	}
	prs := make([]PR, 0, len(raw))
	for _, r := range raw {
		prs = append(prs, PR{
			Number: r.Number, HeadSHA: r.HeadRefOid, HeadRef: r.HeadRefName, BaseRef: r.BaseRefName,
			Title: r.Title, Body: r.Body, Draft: r.IsDraft, Fork: r.IsCrossRepository,
		})
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
