package runs

import (
	"context"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

// Home is a run's home session, as the runner needs it.
type Home struct {
	ID        string
	Workspace string
	// Brief is the home's description: the brief, when /run was given one.
	Brief  string
	Labels []string
	// LastPrompt names the run when there is no brief yet.
	LastPrompt string
}

// Daemon is the part of the protocol the runner uses. Like the watcher, it
// never subscribes: with nobody subscribed a permission request is refused
// at once, so a step session that reaches for the network finds out.
type Daemon interface {
	// Requested is every session labeled run:requested.
	Requested(ctx context.Context) ([]Home, error)
	Home(ctx context.Context, id string) (Home, error)
	// Transcript is a home's conversation, for writing the brief from.
	Transcript(ctx context.Context, id string) (string, error)
	SetLabels(ctx context.Context, id string, labels []string) error
	// SetDescription sets a session's description, which starts nothing.
	SetDescription(ctx context.Context, id, text string) error
	SetGoal(ctx context.Context, id, condition string) error
	Create(ctx context.Context, workspace, parent string, maxTurns int) (string, error)
	SendPrompt(ctx context.Context, id, text string) error
	State(ctx context.Context, id string) (protocol.State, error)
	Events(ctx context.Context, id string) ([]protocol.Event, error)
	Stop(ctx context.Context, id string) error
	Close()
}

// Git is what the runner does with git: in Kyle's clone only to fetch and to
// add the run's worktree, and everything else in that worktree.
type Git interface {
	DefaultBranch(ctx context.Context, clone string) (string, error)
	Fetch(ctx context.Context, clone string) error
	// AddBranchWorktree makes a worktree on a new branch from origin/<from>.
	AddBranchWorktree(ctx context.Context, clone, path, branch, from string) error
	Head(ctx context.Context, dir string) (string, error)
	// Changed is every file the commits since from touched.
	Changed(ctx context.Context, dir, from string) ([]string, error)
	// Dirty is every file with changes not committed.
	Dirty(ctx context.Context, dir string) ([]string, error)
	// ResetHard puts a worktree back at a commit, untracked files and all.
	ResetHard(ctx context.Context, dir, sha string) error
	Commit(ctx context.Context, dir, msg string, paths ...string) error
	// Remove deletes a file in a commit of its own.
	Remove(ctx context.Context, dir, msg, path string) error
	Push(ctx context.Context, dir, branch string) error
	// Diffed is every file the branch changed since it left origin/<base>.
	Diffed(ctx context.Context, dir, base string) ([]string, error)
}

// GH opens the run's pull request and watches its checks.
type GH interface {
	CreatePR(ctx context.Context, dir, base, head, title, body, label string) (number int, url string, err error)
	// PRChecks is the PR's state (OPEN, MERGED or CLOSED) and its checks.
	PRChecks(ctx context.Context, dir string, n int) (state string, checks []Check, err error)
	// FailedLog is the end of a failed Actions run's log.
	FailedLog(ctx context.Context, dir, runID string) (string, error)
}

// Claude is the reviewer at the run's gates: the Claude Code CLI, read-only.
type Claude interface {
	// Available reports whether the CLI is there. A run never starts
	// without it: the gates are the point.
	Available() bool
	Ask(ctx context.Context, dir, prompt string) (string, error)
}

// Shell runs the run's check.
type Shell interface {
	// Verify runs a script from dir. A script that fails, or runs past the
	// timeout, is a failed check with its output, not an error; err is for a
	// script that could not be run at all.
	Verify(ctx context.Context, dir, script string, timeout time.Duration) (passed bool, output string, err error)
}
