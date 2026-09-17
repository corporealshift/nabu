package bench

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeHarness edits the workspace according to what it was told to do, so the
// whole pipeline can be tested without a model anywhere near it.
type fakeHarness struct {
	name string
	// behaviour per task id: "fix", "break-test", "nothing", "timeout", "error"
	does map[string]string
	ran  int
}

func (f *fakeHarness) Name() string                    { return f.name }
func (f *fakeHarness) Preflight(context.Context) error { return nil }

func (f *fakeHarness) Run(_ context.Context, ws *Workspace, task Task) (Attempt, error) {
	f.ran++
	a := Attempt{Cost: Cost{Duration: time.Second, Turns: 2}}

	switch f.does[task.ID] {
	case "fix":
		_ = os.WriteFile(filepath.Join(ws.Dir, "answer.txt"), []byte("42\n"), 0o644)
	case "break-test":
		_ = os.WriteFile(filepath.Join(ws.Dir, "check.txt"), []byte("anything\n"), 0o644)
		_ = os.WriteFile(filepath.Join(ws.Dir, "answer.txt"), []byte("42\n"), 0o644)
	case "timeout":
		a.TimedOut = true
	case "error":
		return a, context.Canceled
	}
	return a, nil
}

// answerTask passes when answer.txt holds 42, and forbids touching check.txt.
func answerTask(t *testing.T) Task {
	t.Helper()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, repo, "answer.txt", "0\n")
	write(t, repo, "check.txt", "answer must be 42\n")

	// findstr is on every Windows; grep is not.
	verify := []string{"grep", "-q", "42", "answer.txt"}
	if _, err := os.Stat("/usr/bin/grep"); err != nil {
		verify = []string{"cmd", "/c", "findstr", "42", "answer.txt"}
	}

	task := Task{
		ID: "answer", Prompt: "put 42 in answer.txt",
		Verify: verify, Unchanged: []string{"check.txt"},
		TimeoutSeconds: 30, Dir: dir,
	}
	raw, _ := json.Marshal(task)
	write(t, dir, "task.json", string(raw))
	return task
}

func runOnce(t *testing.T, task Task, h Harness) Run {
	t.Helper()
	r := NewRunner(Options{
		Tasks: []Task{task}, Harnesses: []Harness{h}, Repeat: 1, Reference: "ref",
	})
	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(res.Runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(res.Runs))
	}
	return res.Runs[0]
}

func TestAHarnessThatDoesTheTaskPasses(t *testing.T) {
	task := answerTask(t)
	got := runOnce(t, task, &fakeHarness{name: "good", does: map[string]string{"answer": "fix"}})

	if got.Outcome != Passed {
		t.Errorf("outcome = %q, want %q (note: %s)", got.Outcome, Passed, got.Note)
	}
}

func TestAHarnessThatDoesNothingFails(t *testing.T) {
	task := answerTask(t)
	got := runOnce(t, task, &fakeHarness{name: "idle", does: map[string]string{"answer": "nothing"}})

	if got.Outcome != Failed {
		t.Errorf("outcome = %q, want %q", got.Outcome, Failed)
	}
}

// The whole point of `unchanged`: passing by editing the check is not passing.
func TestEditingAForbiddenFileIsAViolation(t *testing.T) {
	task := answerTask(t)
	got := runOnce(t, task, &fakeHarness{name: "cheat", does: map[string]string{"answer": "break-test"}})

	if got.Outcome != Violated {
		t.Errorf("outcome = %q, want %q", got.Outcome, Violated)
	}
	if strings.Join(got.Broke, ",") != "check.txt" {
		t.Errorf("broke = %v, want check.txt", got.Broke)
	}
}

func TestATimeoutIsItsOwnOutcome(t *testing.T) {
	task := answerTask(t)
	got := runOnce(t, task, &fakeHarness{name: "slow", does: map[string]string{"answer": "timeout"}})

	if got.Outcome != TimedOut {
		t.Errorf("outcome = %q, want %q", got.Outcome, TimedOut)
	}
}

// A suite that dies because one harness is missing has wasted the whole night.
func TestAHarnessThatCannotRunDoesNotStopTheSuite(t *testing.T) {
	task := answerTask(t)
	r := NewRunner(Options{
		Tasks: []Task{task},
		Harnesses: []Harness{
			&fakeHarness{name: "broken", does: map[string]string{"answer": "error"}},
			&fakeHarness{name: "good", does: map[string]string{"answer": "fix"}},
		},
		Repeat: 1, Reference: "good",
	})

	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("the suite should not fail: %v", err)
	}
	if len(res.Runs) != 2 {
		t.Fatalf("got %d runs, want both harnesses attempted", len(res.Runs))
	}
	if res.Runs[0].Outcome != Errored {
		t.Errorf("the broken harness gave %q, want %q", res.Runs[0].Outcome, Errored)
	}
	if res.Runs[1].Outcome != Passed {
		t.Errorf("the working harness gave %q after the broken one", res.Runs[1].Outcome)
	}
}

func TestEveryRepeatIsRun(t *testing.T) {
	task := answerTask(t)
	h := &fakeHarness{name: "good", does: map[string]string{"answer": "fix"}}
	r := NewRunner(Options{Tasks: []Task{task}, Harnesses: []Harness{h}, Repeat: 3, Reference: "good"})

	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if h.ran != 3 {
		t.Errorf("the harness ran %d times, want 3", h.ran)
	}
	if len(res.Runs) != 3 {
		t.Errorf("got %d runs, want 3", len(res.Runs))
	}
}

// The reference is expected to pass everything, so a task it fails says
// something about the task.
func TestATaskTheReferenceFailsIsSuspect(t *testing.T) {
	task := answerTask(t)
	r := NewRunner(Options{
		Tasks: []Task{task},
		Harnesses: []Harness{
			&fakeHarness{name: "ref", does: map[string]string{"answer": "nothing"}},
			&fakeHarness{name: "other", does: map[string]string{"answer": "nothing"}},
		},
		Repeat: 1, Reference: "ref",
	})

	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Suspects, ",") != "answer" {
		t.Errorf("suspects = %v, want the task the reference failed", res.Suspects)
	}
}

func TestATaskTheReferencePassesIsNotSuspect(t *testing.T) {
	task := answerTask(t)
	r := NewRunner(Options{
		Tasks:     []Task{task},
		Harnesses: []Harness{&fakeHarness{name: "ref", does: map[string]string{"answer": "fix"}}},
		Repeat:    1, Reference: "ref",
	})

	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Suspects) != 0 {
		t.Errorf("suspects = %v, want none", res.Suspects)
	}
}

// Craft is scored on work that runs, not on tidy failure.
func TestCraftIsOnlyJudgedOnPasses(t *testing.T) {
	task := answerTask(t)
	judge := &countingJudge{}
	r := NewRunner(Options{
		Tasks: []Task{task},
		Harnesses: []Harness{
			&fakeHarness{name: "good", does: map[string]string{"answer": "fix"}},
			&fakeHarness{name: "idle", does: map[string]string{"answer": "nothing"}},
		},
		Repeat: 1, Reference: "good", Judge: judge,
	})

	if _, err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if judge.calls != 1 {
		t.Errorf("the judge ran %d times, want 1 (only the passing run)", judge.calls)
	}
}

type countingJudge struct{ calls int }

func (c *countingJudge) Name() string { return "counting" }

func (c *countingJudge) Judge(context.Context, Task, string) (Craft, error) {
	c.calls++
	return Craft{Scored: true, Minimal: 4, Tested: 3, Focused: 5}, nil
}

func TestClaudeJudgingClaudeIsMarkedSelfJudged(t *testing.T) {
	if !judgeIsRelated("claude/sonnet", "claude") {
		t.Error("claude judging claude should be marked")
	}
	if judgeIsRelated("claude/sonnet", "nabu") {
		t.Error("claude judging nabu is not self-judging")
	}
	if judgeIsRelated("none", "claude") {
		t.Error("no judge is not self-judging")
	}
}
