package report

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
)

// DefaultCommitLimit bounds how many commits the report names. A long run can
// make many; the report is meant to be read.
const DefaultCommitLimit = 20

// Module observes the workspace at stop or pause and reports what actually
// changed there, so a caller can check the outcome without trusting the
// model's narration.
type Module struct {
	// CommitLimit caps how many commits are listed. 0 uses the default.
	CommitLimit int

	log logger
}

type logger interface {
	Warn(msg string, args ...any)
}

type discardLog struct{}

func (discardLog) Warn(string, ...any) {}

// Name implements module.Module.
func (m *Module) Name() string { return "report" }

// Init implements module.Module.
func (m *Module) Init(h module.Host, cfg module.Config) error {
	m.log = discardLog{}
	if h != nil && h.Log() != nil {
		m.log = h.Log()
	}
	if n := cfg.Int("commit_limit", 0); n > 0 {
		m.CommitLimit = n
	} else {
		m.CommitLimit = DefaultCommitLimit
	}
	return nil
}

// Report implements module.Reporter: files touched and commits made.
//
// It deliberately does not set TreeDirty. Both this module and `verify` can
// observe the tree, and two reporters asserting it invites them to disagree.
// `verify` owns it, because `verify` is the module that vetoes a stop over it.
//
// A directory that is not a git repository is a normal case, not an error: the
// fields are simply absent.
func (m *Module) Report(_ context.Context, s module.Session) (module.ReportFields, error) {
	var out module.ReportFields
	if s == nil {
		return out, nil
	}
	dir := s.Workspace().Path
	if dir == "" || !isRepo(dir) {
		return out, nil
	}

	out.FilesTouched = filesTouched(dir)
	out.Commits = m.commits(dir)
	return out, nil
}

// isRepo reports whether dir is inside a git working tree.
func isRepo(dir string) bool {
	out, err := git(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// filesTouched lists what changed since HEAD, tracked and untracked alike.
// An untracked file the agent created is a touched file: leaving it out would
// hide exactly the work a caller most wants to see.
func filesTouched(dir string) []string {
	seen := map[string]bool{}

	if out, err := git(dir, "diff", "--name-only", "HEAD"); err == nil {
		for _, f := range splitLines(out) {
			seen[f] = true
		}
	} else if out, err := git(dir, "diff", "--name-only"); err == nil {
		// A repository with no commits has no HEAD to diff against.
		for _, f := range splitLines(out) {
			seen[f] = true
		}
	}
	if out, err := git(dir, "ls-files", "--others", "--exclude-standard"); err == nil {
		for _, f := range splitLines(out) {
			seen[f] = true
		}
	}

	if len(seen) == 0 {
		return nil
	}
	files := make([]string, 0, len(seen))
	for f := range seen {
		files = append(files, f)
	}
	sort.Strings(files)
	return files
}

// commits lists commits made on the current branch that are not on its
// upstream, which is the set a run is responsible for. With no upstream it
// falls back to the most recent commits.
func (m *Module) commits(dir string) []string {
	limit := m.CommitLimit
	if limit <= 0 {
		limit = DefaultCommitLimit
	}
	arg := fmt.Sprintf("-%d", limit)

	if out, err := git(dir, "log", "--format=%h %s", arg, "@{upstream}..HEAD"); err == nil {
		return splitLines(out)
	}
	out, err := git(dir, "log", "--format=%h %s", arg)
	if err != nil {
		return nil // no commits yet, or not readable
	}
	return splitLines(out)
}

// git runs one git command in dir.
func git(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

// splitLines returns the non-empty trimmed lines of command output.
func splitLines(out string) []string {
	var result []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			result = append(result, l)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
