// Package runs is the runner behind `nabu runner`: it takes a brief to a green
// pull request through a fixed sequence of named steps, starting one short
// session for each step that needs the model, doing the mechanical steps
// itself, and asking Claude at three gates
// (docs/specs/2026-09-30-orchestrated-runs-design.md).
//
// The order is this package's code, never the model's choice. The local model
// does badly at long tasks and at following a workflow it was only told
// about, so each session gets one step, one file to produce, and nothing else.
//
// It is a client, like the GitHub watcher: it imports goclient, the watcher's
// helpers and protocol, never the daemon. boundary_test.go enforces that.
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

// Step names where a run is. The names are what Kyle and the labels use.
type Step string

const (
	StepSetup        Step = "setup"
	StepBrief        Step = "brief"
	StepPlan         Step = "plan"
	StepPlanReview   Step = "plan-review"
	StepTasks        Step = "tasks"
	StepVerify       Step = "verify"
	StepVerifyReview Step = "verify-review"
	StepWork         Step = "work"
	StepCheck        Step = "check"
	StepFix          Step = "fix"
	StepRevise       Step = "revise"
	StepFinalReview  Step = "final-review"
	StepPR           Step = "pr"
	StepCI           Step = "ci"
	StepCIFix        Step = "ci-fix"
	StepPush         Step = "push"
	StepMerge        Step = "merge"
	StepDone         Step = "done"
	StepFailed       Step = "failed"
)

// Session reports whether a step is done by a model session.
func (s Step) Session() bool {
	switch s {
	case StepBrief, StepPlan, StepTasks, StepVerify, StepWork, StepFix, StepCIFix:
		return true
	}
	return false
}

// Claude reports whether a step is one of Claude's gates.
func (s Step) Claude() bool {
	switch s {
	case StepPlanReview, StepVerifyReview, StepRevise, StepFinalReview:
		return true
	}
	return false
}

// Over reports whether a run has finished, either way.
func (s Step) Over() bool { return s == StepDone || s == StepFailed }

// Run is one brief on its way to a pull request.
type Run struct {
	// Home is the session the run was asked for in; its id is the run's id.
	Home string `json:"home"`
	// Workspace is Kyle's checkout. The run works in Worktree beside it, on
	// Branch, which starts from Base.
	Workspace string `json:"workspace"`
	Slug      string `json:"slug"`
	Branch    string `json:"branch"`
	Base      string `json:"base,omitempty"`
	Worktree  string `json:"worktree"`
	// Goal is the home of the goal this run is one brief of, if any. Such a
	// run starts from the goal's branch, which Base names, opens its PR
	// against it, and is merged into it once green
	// (docs/specs/2026-10-06-goals-design.md).
	Goal string `json:"goal,omitempty"`
	// Brief is the text the run was given, when it was given one; empty
	// means the brief step writes it from the home's conversation.
	Brief string `json:"brief,omitempty"`

	Step     Step   `json:"step"`
	FailedAt Step   `json:"failed_at,omitempty"`
	Why      string `json:"why,omitempty"`
	// Attempt counts failed tries of the current step, or of the current
	// task during work. It starts again whenever the step or task changes.
	Attempt       int  `json:"attempt,omitempty"`
	Fixes         int  `json:"fixes,omitempty"`
	CIFixes       int  `json:"ci_fixes,omitempty"`
	Revisions     int  `json:"revisions,omitempty"`
	FinalReviewed bool `json:"final_reviewed,omitempty"`

	// Waiting means the current step needs a session and has no slot yet.
	Waiting bool `json:"waiting,omitempty"`
	// Session is the current step's session, and Start the commit the
	// worktree was at when it began.
	Session  string `json:"session,omitempty"`
	Start    string `json:"start,omitempty"`
	Prompted bool   `json:"prompted,omitempty"`
	Stopped  bool   `json:"stopped,omitempty"`
	// NudgedAt is how long the session's log was when it was told it had
	// stopped short; zero until then. A session is told once.
	NudgedAt int `json:"nudged_at,omitempty"`

	// Task is the task a work session is doing, by its index in tasks.md.
	Task int `json:"task,omitempty"`
	// Output is the end of the last check's output, for the fix session and
	// for Claude.
	Output string `json:"output,omitempty"`
	// Advice is what Claude said when it refused to change the check, for
	// the next fix session.
	Advice string `json:"advice,omitempty"`
	// Labelled is the run label last put on the home, so it is set only when
	// it changes.
	Labelled string `json:"labelled,omitempty"`

	// CISince is when the run started watching its PR's checks, to give up
	// waiting on a repository that has none.
	CISince time.Time `json:"ci_since,omitzero"`

	// Issue is the GitHub issue the run was asked for, if any, and Reported
	// whether the issue has been told how the run ended.
	Issue    int  `json:"issue,omitempty"`
	Reported bool `json:"reported,omitempty"`

	PR    int      `json:"pr,omitempty"`
	PRURL string   `json:"pr_url,omitempty"`
	Notes []string `json:"notes,omitempty"`

	Started time.Time `json:"started"`
	Updated time.Time `json:"updated"`
}

// Dir is where the run files are committed on the branch, relative to the
// worktree.
func (r Run) Dir() string { return filepath.ToSlash(filepath.Join(".nabu", "runs", r.Slug)) }

// File is one of the run's files, relative to the worktree.
func (r Run) File(name string) string { return r.Dir() + "/" + name }

// The run's files.
const (
	BriefFile    = "brief.md"
	PlanFile     = "plan.md"
	TasksFile    = "tasks.md"
	VerifyFile   = "verify.sh"
	RevisionFile = "verify-revision.md"
)

// RunsDir holds one state file per run.
func RunsDir(root string) string { return filepath.Join(root, "runner", "runs") }

func runPath(root, home string) string { return filepath.Join(RunsDir(root), home+".json") }

// Save writes a run's state through a temp file and a rename.
func (r Run) Save(root string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	dir := RunsDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("runs: %w", err)
	}
	tmp, err := os.CreateTemp(dir, r.Home+".*.tmp")
	if err != nil {
		return fmt.Errorf("runs: %w", err)
	}
	_, werr := tmp.Write(append(b, '\n'))
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("runs: writing %s: %w", r.Home, errors.Join(werr, cerr))
	}
	if err := os.Rename(tmp.Name(), runPath(root, r.Home)); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("runs: %w", err)
	}
	return nil
}

// Load reads one run; ok is false when there is none.
func Load(root, home string) (Run, bool, error) {
	b, err := os.ReadFile(runPath(root, home))
	if errors.Is(err, fs.ErrNotExist) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, fmt.Errorf("runs: %w", err)
	}
	var r Run
	if err := json.Unmarshal(b, &r); err != nil {
		return Run{}, false, fmt.Errorf("runs: %s: %w", runPath(root, home), err)
	}
	return r, true, nil
}

// LoadAll reads every run, in no particular order.
func LoadAll(root string) ([]Run, error) {
	entries, err := os.ReadDir(RunsDir(root))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("runs: %w", err)
	}
	var out []Run
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		r, ok, err := Load(root, strings.TrimSuffix(name, ".json"))
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, r)
		}
	}
	return out, nil
}
