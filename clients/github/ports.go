package github

import (
	"context"

	"github.com/corporealshift/nabu/protocol"
)

// GitHub is what the watcher reads from and posts to GitHub. The real one
// runs gh; tests use a fake.
type GitHub interface {
	OpenPRs(ctx context.Context, repo string) ([]PR, error)
	PostReview(ctx context.Context, repo string, number int, post ReviewPost) error
	// PRComments is every comment on a PR, of all three kinds, leaving out
	// reviews not yet submitted and their comments.
	PRComments(ctx context.Context, repo string, number int) ([]Comment, error)
	// ReplyTo answers in the thread that starts with root.
	ReplyTo(ctx context.Context, repo string, number int, root int64, body string) error
	// Comment posts in the PR's conversation.
	Comment(ctx context.Context, repo string, number int, body string) error
}

// Git is what the watcher does with git. Everything runs in Kyle's clone or
// in a worktree the watcher made; none of it changes the clone's checkout.
type Git interface {
	// FetchPR brings a PR's head and its base branch into the clone.
	FetchPR(ctx context.Context, clone string, number int, base string) error
	AddWorktree(ctx context.Context, clone, path, sha string) error
	// Diff is the change a PR makes: its head against where it left base.
	Diff(ctx context.Context, dir, base, sha string) (string, error)
	RemoveWorktree(ctx context.Context, clone, path string) error
	// FetchBranch brings a branch into the clone as origin/<ref>.
	FetchBranch(ctx context.Context, clone, ref string) error
	// Head is the commit a worktree is at.
	Head(ctx context.Context, dir string) (string, error)
	// Push sends a worktree's HEAD to a branch, never forcing it.
	Push(ctx context.Context, dir, ref string) error
}

// Daemon is the part of the protocol the watcher uses. It never subscribes:
// with nobody subscribed a permission request is refused at once (spec
// §7.18), so a session that reaches for the network learns so immediately.
type Daemon interface {
	Create(ctx context.Context, workspace string, maxTurns int) (string, error)
	SetGoal(ctx context.Context, sessionID, condition string) error
	SendPrompt(ctx context.Context, sessionID, text string) error
	State(ctx context.Context, sessionID string) (protocol.State, error)
	Stop(ctx context.Context, sessionID string) error
	Events(ctx context.Context, sessionID string) ([]protocol.Event, error)
	Close()
}
