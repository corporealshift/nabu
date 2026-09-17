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
	Unchanged      []string `json:"unchanged"`
	MaxTurns       int      `json:"max_turns"`
	TimeoutSeconds int      `json:"timeout_seconds"`

	// Dir is where the task was loaded from; its repo/ is the fixture.
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
	return nil
}
