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
}

// Daemon is the part of the protocol the watcher uses. It never subscribes:
// with nobody subscribed a permission request is refused at once (spec
// §7.18), so a session that reaches for the network learns so immediately.
type Daemon interface {
	Create(ctx context.Context, workspace string, maxTurns int) (string, error)
	SendPrompt(ctx context.Context, sessionID, text string) error
	State(ctx context.Context, sessionID string) (protocol.State, error)
	Stop(ctx context.Context, sessionID string) error
	Events(ctx context.Context, sessionID string) ([]protocol.Event, error)
	Close()
}
