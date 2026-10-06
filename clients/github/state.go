package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// State is what the watcher has handled, and what it is in the middle of.
// It is the watcher's own; the daemon knows nothing of it.
type State struct {
	Repos map[string]*RepoState `json:"repos"`
}

// RepoState is one repository's share of State.
type RepoState struct {
	// Seen is when each open PR's current head was first seen. GitHub does not
	// say when a head was pushed, so the quiet period runs from here.
	Seen map[int]Seen `json:"seen,omitempty"`
	// Reviewed is the last head of each PR a review was posted for.
	Reviewed map[int]Reviewed `json:"reviewed,omitempty"`
	// Failed is the head of each PR whose review job failed. It is not tried
	// again until the PR has a new head.
	Failed map[int]string `json:"failed,omitempty"`
	// Handled is, for each PR, the newest comment of each kind that a posted
	// comments job answered.
	Handled map[int]Marks `json:"handled,omitempty"`
	// FailedThrough is, for each PR, how far a failed comments job reached.
	// Only a comment past it starts another.
	FailedThrough map[int]Marks `json:"failed_through,omitempty"`
	// Running is every job started and not yet finished.
	Running []Job `json:"running,omitempty"`
	// Issues is, for each labeled issue, the run made for it.
	Issues map[int]IssueRun `json:"issues,omitempty"`
}

// IssueRun is the run an issue was given: its home session, and the issue's
// fingerprint when the run was last asked for, so an edit or a new comment
// can ask again after a failure.
type IssueRun struct {
	Home string `json:"home"`
	Seen string `json:"seen"`
}

// Seen is a head and when it was first seen.
type Seen struct {
	SHA string    `json:"sha"`
	At  time.Time `json:"at"`
}

// Reviewed is a posted review: the head it was for, and its summary, which
// the next review of the same PR is shown so it does not repeat itself.
// Handled is how far comments jobs had answered when the review started, so
// the next review is shown the comments answered since and can check the new
// commits did what they asked.
type Reviewed struct {
	SHA     string `json:"sha"`
	Summary string `json:"summary,omitempty"`
	Handled Marks  `json:"handled,omitzero"`
}

// Job is one session the watcher started.
type Job struct {
	Kind string `json:"kind"`
	PR   int    `json:"pr"`
	SHA  string `json:"sha"`
	Base string `json:"base"`
	// HeadRef is the branch a comments job pushes to.
	HeadRef string `json:"head_ref,omitempty"`
	// Through is the newest comment of each kind a comments job answers, and
	// Due is those comments, kept so the replies can be built after a restart.
	// For a review job, Through is how far comments had been answered when it
	// started, which the next review counts from.
	Through Marks     `json:"through,omitzero"`
	Due     []Comment `json:"due,omitempty"`
	// Prompt, Goal and MaxTurns are what the session is started with. They
	// are kept so a restart can start it without asking GitHub again.
	Prompt   string `json:"prompt,omitempty"`
	Goal     string `json:"goal,omitempty"`
	MaxTurns int    `json:"max_turns,omitempty"`
	// Pushed and Posted record how far a comments job's results got out, so
	// a retry after a failed post neither pushes nor posts anything twice.
	Pushed bool     `json:"pushed,omitempty"`
	Posted []string `json:"posted,omitempty"`
	// SessionID is empty between recording the job and the daemon creating
	// its session. A job found that way after a restart never got a session.
	SessionID string `json:"session_id,omitempty"`
	// Prompted means the session has its prompt. Sending it again is safe
	// (it carries a client_id), but a session left without one never starts.
	Prompted bool      `json:"prompted,omitempty"`
	Worktree string    `json:"worktree"`
	Started  time.Time `json:"started"`
	// Stopped means the session has been stopped and only the post is left,
	// so a failed post is retried without touching the session again.
	Stopped bool `json:"stopped,omitempty"`
}

// The job kinds.
const (
	KindReview   = "review"
	KindComments = "comments"
)

// Repo returns a repository's state, creating it.
func (s *State) Repo(name string) *RepoState {
	if s.Repos == nil {
		s.Repos = map[string]*RepoState{}
	}
	r := s.Repos[name]
	if r == nil {
		r = &RepoState{}
		s.Repos[name] = r
	}
	if r.Seen == nil {
		r.Seen = map[int]Seen{}
	}
	if r.Reviewed == nil {
		r.Reviewed = map[int]Reviewed{}
	}
	if r.Failed == nil {
		r.Failed = map[int]string{}
	}
	if r.Handled == nil {
		r.Handled = map[int]Marks{}
	}
	if r.FailedThrough == nil {
		r.FailedThrough = map[int]Marks{}
	}
	if r.Issues == nil {
		r.Issues = map[int]IssueRun{}
	}
	return r
}

// RunningCount is how many jobs are running across every repository.
func (s *State) RunningCount() int {
	n := 0
	for _, r := range s.Repos {
		n += len(r.Running)
	}
	return n
}

// StatePath is the state file; a dry run keeps its own, so it never marks
// anything handled for a real run.
func StatePath(root string, dryRun bool) string {
	if dryRun {
		return filepath.Join(Dir(root), "state.dry-run.json")
	}
	return filepath.Join(Dir(root), "state.json")
}

// LoadState reads the state; a missing file is an empty state.
func LoadState(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &State{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("github: %s: %w", path, err)
	}
	return &s, nil
}

// Save writes the state through a temp file and a rename, so a crash leaves
// the old file or the new one and never half of either.
func (s *State) Save(path string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("github: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("github: %w", err)
	}
	_, werr := tmp.Write(append(b, '\n'))
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("github: writing state: %w", errors.Join(werr, cerr))
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("github: %w", err)
	}
	return nil
}
