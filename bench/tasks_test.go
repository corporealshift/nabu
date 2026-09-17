package bench

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tasksDir is the real suite, checked from the package directory.
const tasksDir = "tasks"

func TestTheSuiteLoads(t *testing.T) {
	tasks, err := LoadTasks(tasksDir)
	if err != nil {
		t.Fatalf("loading the suite: %v", err)
	}
	if len(tasks) == 0 {
		t.Fatal("the suite is empty")
	}
	for _, task := range tasks {
		if task.TimeoutSeconds <= 0 {
			t.Errorf("%s has no timeout", task.ID)
		}
	}
}

// A fixture whose check already passes measures nothing: every harness would
// score a point for doing nothing at all. This is the test that keeps the
// instrument honest.
func TestEveryFixtureStartsFailing(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}

	tasks, err := LoadTasks(tasksDir)
	if err != nil {
		t.Fatalf("loading the suite: %v", err)
	}

	for _, task := range tasks {
		t.Run(task.ID, func(t *testing.T) {
			ws, err := NewWorkspace(context.Background(), task.Fixture(), t.TempDir())
			if err != nil {
				t.Fatalf("workspace: %v", err)
			}
			defer ws.Remove()

			passed, runnable, why := verify(context.Background(), ws, task)
			if !runnable {
				t.Fatalf("the check %v could not be run: %s", task.Verify, why)
			}
			if passed {
				t.Errorf("the fixture already passes %v, so the task asks for nothing", task.Verify)
			}
		})
	}
}

// Whatever a task forbids changing must actually be in the fixture, or the
// constraint silently protects nothing.
func TestForbiddenFilesExist(t *testing.T) {
	tasks, err := LoadTasks(tasksDir)
	if err != nil {
		t.Fatalf("loading the suite: %v", err)
	}

	for _, task := range tasks {
		for _, f := range task.Unchanged {
			path := filepath.Join(task.Fixture(), filepath.FromSlash(f))
			if !fileExists(path) {
				t.Errorf("%s forbids changing %s, which is not in the fixture", task.ID, f)
			}
		}
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
