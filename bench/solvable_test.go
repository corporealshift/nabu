package bench

import (
	"context"
	"os/exec"
	"testing"
)

// A held-back check cannot be tried against, which is the point of it — and
// also means nothing else proves the task is achievable. Every task that holds
// a check back therefore ships the change that satisfies it, and this applies
// that change and insists the check goes quiet.
//
// It replaces "Claude managed it" as the evidence a fixture is sound: free,
// deterministic, and it does not depend on a model having a good day.
func TestEveryHiddenTaskIsAchievable(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}

	tasks, err := LoadTasks(tasksDir)
	if err != nil {
		t.Fatalf("loading the suite: %v", err)
	}

	for _, task := range tasks {
		if !task.HasHidden() {
			continue
		}

		t.Run(task.ID, func(t *testing.T) {
			if !task.HasSolution() {
				t.Fatal("holds a check back but ships no solution, so nothing proves it can be done")
			}

			ws, err := NewWorkspace(context.Background(), task.Fixture(), t.TempDir())
			if err != nil {
				t.Fatalf("workspace: %v", err)
			}
			defer ws.Remove()

			if err := ws.Overlay(task.Solution()); err != nil {
				t.Fatalf("applying the solution: %v", err)
			}
			if err := ws.Overlay(task.Hidden()); err != nil {
				t.Fatalf("applying the hidden check: %v", err)
			}

			passed, runnable, why := verify(context.Background(), ws, task)
			if !runnable {
				t.Fatalf("the check could not be run: %s", why)
			}
			if !passed {
				t.Error("the shipped solution does not satisfy the hidden check, " +
					"so no harness could pass this task")
			}
		})
	}
}

// The solution must not be what makes the task start failing, and the visible
// state must not already satisfy the hidden check — otherwise the held-back
// half is decoration.
func TestAHiddenCheckActuallyHoldsSomethingBack(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}

	tasks, err := LoadTasks(tasksDir)
	if err != nil {
		t.Fatalf("loading the suite: %v", err)
	}

	for _, task := range tasks {
		if !task.HasHidden() {
			continue
		}

		t.Run(task.ID, func(t *testing.T) {
			ws, err := NewWorkspace(context.Background(), task.Fixture(), t.TempDir())
			if err != nil {
				t.Fatalf("workspace: %v", err)
			}
			defer ws.Remove()

			if err := ws.Overlay(task.Hidden()); err != nil {
				t.Fatalf("applying the hidden check: %v", err)
			}
			if passed, _, _ := verify(context.Background(), ws, task); passed {
				t.Error("the fixture already satisfies its own hidden check")
			}
		})
	}
}
