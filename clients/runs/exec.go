package runs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/corporealshift/nabu/clients/github"
)

// GitCLI is Git over the git program.
type GitCLI struct{ Run github.Runner }

func (g GitCLI) git(ctx context.Context, args ...string) (string, error) {
	out, err := g.Run(ctx, "", nil, "git", args...)
	return string(out), err
}

func (g GitCLI) DefaultBranch(ctx context.Context, clone string) (string, error) {
	out, err := g.git(ctx, "-C", clone, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil || strings.TrimSpace(out) == "" {
		// A clone made before origin/HEAD existed has no pointer; main is
		// what nearly every repository calls it.
		return "main", nil
	}
	return strings.TrimPrefix(strings.TrimSpace(out), "origin/"), nil
}

func (g GitCLI) Fetch(ctx context.Context, clone string) error {
	_, err := g.git(ctx, "-C", clone, "fetch", "origin")
	return err
}

func (g GitCLI) AddBranchWorktree(ctx context.Context, clone, path, branch, from string) error {
	// -B, not -b: a run that failed during setup may have left the branch,
	// and nothing is on it yet.
	_, err := g.git(ctx, "-C", clone, "worktree", "add", "-B", branch, path, "origin/"+from)
	return err
}

func (g GitCLI) Head(ctx context.Context, dir string) (string, error) {
	out, err := g.git(ctx, "-C", dir, "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

func lines(out string) []string {
	var paths []string
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			paths = append(paths, l)
		}
	}
	return paths
}

func (g GitCLI) Changed(ctx context.Context, dir, from string) ([]string, error) {
	out, err := g.git(ctx, "-C", dir, "diff", "--name-only", from, "HEAD")
	return lines(out), err
}

func (g GitCLI) Dirty(ctx context.Context, dir string) ([]string, error) {
	out, err := g.git(ctx, "-C", dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if len(l) > 3 {
			paths = append(paths, strings.Trim(l[3:], `"`))
		}
	}
	return paths, nil
}

func (g GitCLI) ResetHard(ctx context.Context, dir, sha string) error {
	if _, err := g.git(ctx, "-C", dir, "reset", "--hard", sha); err != nil {
		return err
	}
	// Untracked files a thrown-away session left, but not ignored build
	// output, which is expensive to make again and was never the session's.
	_, err := g.git(ctx, "-C", dir, "clean", "-fd")
	return err
}

func (g GitCLI) Commit(ctx context.Context, dir, msg string, paths ...string) error {
	if _, err := g.git(ctx, append([]string{"-C", dir, "add", "--"}, paths...)...); err != nil {
		return err
	}
	_, err := g.git(ctx, "-C", dir, "commit", "-q", "-m", msg)
	return err
}

func (g GitCLI) Remove(ctx context.Context, dir, msg, path string) error {
	if _, err := g.git(ctx, "-C", dir, "rm", "-q", "--", path); err != nil {
		return err
	}
	_, err := g.git(ctx, "-C", dir, "commit", "-q", "-m", msg)
	return err
}

func (g GitCLI) Push(ctx context.Context, dir, branch string) error {
	_, err := g.git(ctx, "-C", dir, "push", "-u", "origin", branch)
	return err
}

func (g GitCLI) Diffed(ctx context.Context, dir, base string) ([]string, error) {
	out, err := g.git(ctx, "-C", dir, "diff", "--name-only", "origin/"+base+"...HEAD")
	return lines(out), err
}

// GHCLI is GH over the gh program.
type GHCLI struct{ Run github.Runner }

func (g GHCLI) CreatePR(ctx context.Context, dir, base, head, title, body, label string) (int, string, error) {
	// The label may not exist yet in this repository; making it is harmless
	// when it does, and gh says so, which is not a failure worth stopping on.
	_, _ = g.Run(ctx, dir, nil, "gh", "label", "create", label, "--color", "5319e7", "--description", "nabu works on this")
	out, err := g.Run(ctx, dir, []byte(body), "gh", "pr", "create",
		"--base", base, "--head", head, "--title", title, "--body-file", "-", "--label", label)
	if err != nil {
		return 0, "", err
	}
	ls := lines(string(out))
	if len(ls) == 0 {
		return 0, "", errors.New("gh pr create printed no URL")
	}
	url := ls[len(ls)-1]
	n, err := strconv.Atoi(url[strings.LastIndex(url, "/")+1:])
	if err != nil {
		return 0, url, fmt.Errorf("gh pr create printed %q, not a PR URL", url)
	}
	return n, url, nil
}

// ClaudeCLI is Claude Code, asked read-only in the run's worktree.
type ClaudeCLI struct {
	// Path is the CLI; empty looks for claude on PATH.
	Path    string
	Model   string
	Timeout time.Duration
}

// readOnly is every tool the reviewer gets. It returns text; the runner
// writes and commits files.
var readOnly = []string{"Read", "Grep", "Glob"}

func (c ClaudeCLI) exe() string {
	if c.Path != "" {
		return c.Path
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	return ""
}

func (c ClaudeCLI) Available() bool { return c.exe() != "" }

// Args is the command line for one question. The prompt is one argument,
// never part of a shell string.
func (c ClaudeCLI) Args(prompt string) []string {
	args := []string{"-p", prompt}
	for _, t := range readOnly {
		args = append(args, "--allowedTools", t)
	}
	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}
	return args
}

func (c ClaudeCLI) Ask(ctx context.Context, dir, prompt string) (string, error) {
	exe := c.exe()
	if exe == "" {
		return "", errors.New("the claude CLI is not installed")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, exe, c.Args(prompt)...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if cctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("claude did not answer within %s", timeout)
	}
	if err != nil {
		// The error names the program, never the prompt: that is pages long.
		return "", fmt.Errorf("claude: %v: %s", err, firstLines(stderr.String(), 10))
	}
	answer := strings.TrimSpace(stdout.String())
	if answer == "" {
		return "", errors.New("claude said nothing")
	}
	return answer, nil
}

func firstLines(s string, n int) string {
	ls := strings.Split(strings.TrimSpace(s), "\n")
	if len(ls) > n {
		ls = append(ls[:n], "…")
	}
	return strings.Join(ls, "\n")
}

// Bash runs the check with bash.
type Bash struct{}

// maxCheckOutput is how much of a check's output is kept, from the end.
const maxCheckOutput = 20000

func (Bash) Verify(ctx context.Context, dir, script string, timeout time.Duration) (bool, string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "bash", script)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	text := out.String()
	if len(text) > maxCheckOutput {
		text = "(earlier output cut)\n" + text[len(text)-maxCheckOutput:]
	}
	if cctx.Err() == context.DeadlineExceeded {
		return false, text + fmt.Sprintf("\n(the check was stopped after %s)", timeout), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, text, nil
	}
	if err != nil {
		return false, text, fmt.Errorf("running %s: %w", script, err)
	}
	return true, text, nil
}

// PRChecks is a PR's state (OPEN, MERGED or CLOSED) and its checks. It reads
// them with gh pr view, which, unlike gh pr checks, does not exit non-zero
// while a check is pending or failing.
func (g GHCLI) PRChecks(ctx context.Context, dir string, n int) (string, []Check, error) {
	out, err := g.Run(ctx, dir, nil, "gh", "pr", "view", strconv.Itoa(n), "--json", "state,statusCheckRollup")
	if err != nil {
		return "", nil, err
	}
	var v struct {
		State  string `json:"state"`
		Checks []struct {
			Typename   string `json:"__typename"`
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			DetailsURL string `json:"detailsUrl"`
			Context    string `json:"context"`
			State      string `json:"state"`
			TargetURL  string `json:"targetUrl"`
		} `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", nil, fmt.Errorf("gh pr view: %w", err)
	}
	var checks []Check
	for _, c := range v.Checks {
		if c.Typename == "StatusContext" {
			checks = append(checks, Check{Name: c.Context, State: c.State, Link: c.TargetURL})
			continue
		}
		state := c.Conclusion
		if !strings.EqualFold(c.Status, "COMPLETED") {
			state = c.Status
		}
		checks = append(checks, Check{Name: c.Name, State: state, Link: c.DetailsURL})
	}
	return v.State, checks, nil
}

// FailedLog is the end of the failed steps' output of an Actions run.
func (g GHCLI) FailedLog(ctx context.Context, dir, runID string) (string, error) {
	out, err := g.Run(ctx, dir, nil, "gh", "run", "view", runID, "--log-failed")
	if err != nil {
		return "", err
	}
	text := string(out)
	if len(text) > maxCheckOutput {
		text = "(earlier log cut)\n" + text[len(text)-maxCheckOutput:]
	}
	return text, nil
}
