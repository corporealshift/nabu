// Package bench compares how well coding-agent harnesses complete the same
// tasks. It is run by hand, never in CI: it prints a report and nothing else.
package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Task is one benchmark task: a fixture repository, the instruction every
// harness is given, and what proves it done.
type Task struct {
	ID string `json:"id"`
	// Prompt is given verbatim to every harness, so they are compared on the
	// same words rather than on how each was asked.
	Prompt string `json:"prompt"`
	// Verify is argv, run in the workspace. Exit 0 means the task was done.
	Verify []string `json:"verify"`
	// Unchanged names files the task forbids touching, usually the test that
	// defines success.
	Unchanged []string `json:"unchanged"`
	// Tier separates tasks that confirm a harness works at all from tasks meant
	// to tell good harnesses apart. A tier everyone passes measures nothing.
	Tier           string `json:"tier"`
	MaxTurns       int    `json:"max_turns"`
	TimeoutSeconds int    `json:"timeout_seconds"`

	// Dir is where the task was loaded from; its repo/ is the fixture and
	// its _hidden/, if any, is the half the harness never sees. Both that and
	// _solution/ start with an underscore, which is how the go command knows
	// to leave them out of this repository own build.
	Dir string `json:"-"`
}

// Timeout is the wall-clock cap for one run of this task.
func (t Task) Timeout() time.Duration {
	if t.TimeoutSeconds <= 0 {
		return 15 * time.Minute
	}
	return time.Duration(t.TimeoutSeconds) * time.Second
}

// Fixture is the pristine repository this task's workspaces are copied from.
func (t Task) Fixture() string { return filepath.Join(t.Dir, "repo") }

// Hidden is the directory of files laid over the workspace after the harness
// has finished and before the check runs.
//
// A check the harness can run is a check it can grind against: it changes
// something, runs the command, reads the assertion that failed, and tries
// again until the command is quiet. That measures persistence. Holding the
// real test back measures whether the harness understood the contract it was
// given, which is the thing worth knowing.
func (t Task) Hidden() string { return filepath.Join(t.Dir, "_hidden") }

// HasHidden reports whether this task holds part of its check back.
func (t Task) HasHidden() bool {
	info, err := os.Stat(t.Hidden())
	return err == nil && info.IsDir()
}

// Solution is the known-good patch that proves the task is possible. It is
// never copied into a workspace a harness sees.
func (t Task) Solution() string { return filepath.Join(t.Dir, "_solution") }

// HasSolution reports whether this task can be checked for being achievable.
func (t Task) HasSolution() bool {
	info, err := os.Stat(t.Solution())
	return err == nil && info.IsDir()
}

// LoadTasks reads every task under dir, in id order so a report's rows do not
// move between runs.
func LoadTasks(dir string) ([]Task, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading tasks: %w", err)
	}

	var tasks []Task
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		t, err := LoadTask(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}

	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	return tasks, nil
}

// LoadTask reads one task directory.
func LoadTask(dir string) (Task, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "task.json"))
	if err != nil {
		return Task{}, fmt.Errorf("reading task in %s: %w", dir, err)
	}

	var t Task
	if err := json.Unmarshal(raw, &t); err != nil {
		return Task{}, fmt.Errorf("parsing task in %s: %w", dir, err)
	}
	t.Dir = dir

	if err := t.validate(); err != nil {
		return Task{}, fmt.Errorf("task in %s: %w", dir, err)
	}
	return t, nil
}

// validate rejects a task that cannot be run, at load rather than three hours
// into a suite.
func (t Task) validate() error {
	switch {
	case t.ID == "":
		return fmt.Errorf("has no id")
	case t.Prompt == "":
		return fmt.Errorf("has no prompt")
	case len(t.Verify) == 0:
		return fmt.Errorf("has no verify command")
	}
	if info, err := os.Stat(t.Fixture()); err != nil || !info.IsDir() {
		return fmt.Errorf("has no repo/ fixture")
	}
	switch t.Tier {
	case "basic", "hard":
	case "":
		return fmt.Errorf("has no tier (basic or hard)")
	default:
		return fmt.Errorf("has unknown tier %q", t.Tier)
	}
	return nil
}
