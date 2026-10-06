package runs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GoalStep names where a goal is (docs/specs/2026-10-06-goals-design.md).
type GoalStep string

const (
	GoalSetup     GoalStep = "setup"
	GoalBreakdown GoalStep = "breakdown"
	GoalRuns      GoalStep = "runs"
	GoalCheck     GoalStep = "check"
	GoalPR        GoalStep = "pr"
	GoalCI        GoalStep = "ci"
	GoalDone      GoalStep = "done"
	GoalBlocked   GoalStep = "blocked"
)

// Over reports whether a goal has stopped moving: done, or waiting for Kyle.
func (s GoalStep) Over() bool { return s == GoalDone || s == GoalBlocked }

// The states of one of a goal's briefs.
const (
	BriefPending = "pending"
	BriefRunning = "running"
	BriefDone    = "done"
	BriefFailed  = "failed"
	// BriefDropped is a brief a failed run left unstarted. The check that
	// follows sees it, and writes the next round's briefs instead.
	BriefDropped = "dropped"
)

// GoalBrief is one brief of a goal: a run, once it is started.
type GoalBrief struct {
	Title string `json:"title"`
	Brief string `json:"brief"`
	Round int    `json:"round"`
	State string `json:"state"`
	// Run is the run's home, once there is one.
	Run   string   `json:"run,omitempty"`
	PRURL string   `json:"pr_url,omitempty"`
	Notes []string `json:"notes,omitempty"`
	// FailedAt and Why say where and why the run failed.
	FailedAt Step   `json:"failed_at,omitempty"`
	Why      string `json:"why,omitempty"`
}

// Verdict is what a check said at the end of a round.
type Verdict struct {
	Round  int    `json:"round"`
	Met    bool   `json:"met"`
	Reason string `json:"reason"`
}

// Goal is a broad piece of work on its way to one pull request, through as
// many runs as it needs.
type Goal struct {
	// Home is the session the goal was asked for in; its id is the goal's id.
	Home string `json:"home"`
	// Workspace is Kyle's checkout. The goal keeps its own worktree on
	// Branch, which starts from Base, the default branch.
	Workspace string `json:"workspace"`
	Slug      string `json:"slug"`
	Branch    string `json:"branch"`
	Base      string `json:"base,omitempty"`
	Worktree  string `json:"worktree"`
	// Text is the goal, from the home's description.
	Text     string      `json:"text"`
	DoneWhen []string    `json:"done_when,omitempty"`
	Briefs   []GoalBrief `json:"briefs,omitempty"`
	Verdicts []Verdict   `json:"verdicts,omitempty"`

	Step      GoalStep `json:"step"`
	BlockedAt GoalStep `json:"blocked_at,omitempty"`
	Why       string   `json:"why,omitempty"`
	// Round is the round under way, from 1. RoundsFrom is the round the
	// current allowance of rounds began at: 1, or where a resume began.
	Round      int `json:"round,omitempty"`
	RoundsFrom int `json:"rounds_from,omitempty"`
	// Failures is how many of the goal's runs have failed in a row.
	Failures int `json:"failures,omitempty"`
	// Attempt counts failed tries of the current step.
	Attempt int `json:"attempt,omitempty"`
	// CIFailure is the final PR's failed checks, for the check it led to.
	CIFailure string `json:"ci_failure,omitempty"`

	PR      int       `json:"pr,omitempty"`
	PRURL   string    `json:"pr_url,omitempty"`
	CISince time.Time `json:"ci_since,omitzero"`
	// Labelled is the goal labels last put on the home, so they are set
	// only when they change. Tasked is the same for its task list.
	Labelled string `json:"labelled,omitempty"`
	Tasked   string `json:"tasked,omitempty"`

	Started time.Time `json:"started"`
	Updated time.Time `json:"updated"`
}

// Dir is where the goal's files are committed on its branch.
func (g Goal) Dir() string { return filepath.ToSlash(filepath.Join(".nabu", "goals", g.Slug)) }

// File is one of the goal's files, relative to the worktree.
func (g Goal) File(name string) string { return g.Dir() + "/" + name }

// The goal's files.
const (
	GoalFile    = "goal.md"
	RoadmapFile = "roadmap.md"
)

// Current is the index of the brief being worked on or next to start, or -1
// when the round has none left.
func (g Goal) Current() int {
	for i, b := range g.Briefs {
		if b.Round == g.Round && (b.State == BriefPending || b.State == BriefRunning) {
			return i
		}
	}
	return -1
}

// InRound is the briefs of one round.
func (g Goal) InRound(round int) []GoalBrief {
	var out []GoalBrief
	for _, b := range g.Briefs {
		if b.Round == round {
			out = append(out, b)
		}
	}
	return out
}

// GoalsDir holds one state file per goal.
func GoalsDir(root string) string { return filepath.Join(root, "runner", "goals") }

func goalPath(root, home string) string { return filepath.Join(GoalsDir(root), home+".json") }

// Save writes a goal's state through a temp file and a rename.
func (g Goal) Save(root string) error {
	return saveJSON(GoalsDir(root), g.Home, g)
}

// LoadGoals reads every goal, in no particular order.
func LoadGoals(root string) ([]Goal, error) {
	entries, err := os.ReadDir(GoalsDir(root))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("runs: %w", err)
	}
	var out []Goal
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(GoalsDir(root), name))
		if err != nil {
			return nil, fmt.Errorf("runs: %w", err)
		}
		var g Goal
		if err := json.Unmarshal(b, &g); err != nil {
			return nil, fmt.Errorf("runs: %s: %w", name, err)
		}
		out = append(out, g)
	}
	return out, nil
}

// saveJSON writes v to <dir>/<name>.json through a temp file and a rename, so
// a crash leaves the old state or the new, never half of one.
func saveJSON(dir, name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("runs: %w", err)
	}
	tmp, err := os.CreateTemp(dir, name+".*.tmp")
	if err != nil {
		return fmt.Errorf("runs: %w", err)
	}
	_, werr := tmp.Write(append(b, '\n'))
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("runs: writing %s: %w", name, errors.Join(werr, cerr))
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name+".json")); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("runs: %w", err)
	}
	return nil
}
