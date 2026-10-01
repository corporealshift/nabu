package runs

import (
	"reflect"
	"testing"
	"time"
)

func at(s Step) Run { return Run{Home: "H", Step: s} }

func TestTransition(t *testing.T) {
	ok := Outcome{OK: true}
	failed := Outcome{Why: "no plan.md"}
	tests := []struct {
		name string
		from Run
		o    Outcome
		want Step
		// check looks at more than the step, when it matters.
		check func(t *testing.T, r Run)
	}{
		{name: "setup with a brief goes to plan", from: Run{Step: StepSetup, Brief: "add x"}, o: ok, want: StepPlan},
		{name: "setup without one goes to brief", from: at(StepSetup), o: ok, want: StepBrief},
		{name: "brief", from: at(StepBrief), o: ok, want: StepPlan},
		{name: "plan", from: at(StepPlan), o: ok, want: StepPlanReview},
		{name: "plan-review", from: at(StepPlanReview), o: ok, want: StepTasks},
		{name: "tasks", from: at(StepTasks), o: ok, want: StepVerify},
		{name: "verify", from: at(StepVerify), o: ok, want: StepVerifyReview},
		{name: "verify-review with tasks", from: at(StepVerifyReview), o: Outcome{OK: true, TasksLeft: 3}, want: StepWork},
		{name: "verify-review with none left", from: at(StepVerifyReview), o: ok, want: StepCheck},
		{name: "work, more tasks", from: Run{Step: StepWork, Attempt: 1}, o: Outcome{OK: true, TasksLeft: 2}, want: StepWork,
			check: func(t *testing.T, r Run) {
				if r.Attempt != 0 || !r.Waiting {
					t.Errorf("a new task should start fresh and wait for a slot: %+v", r)
				}
			}},
		{name: "work, last task", from: at(StepWork), o: ok, want: StepCheck},
		{name: "check passes, not yet reviewed", from: at(StepCheck), o: Outcome{OK: true, CheckPassed: true}, want: StepFinalReview},
		{name: "check passes, reviewed", from: Run{Step: StepCheck, FinalReviewed: true}, o: Outcome{OK: true, CheckPassed: true}, want: StepPR},
		{name: "check fails", from: at(StepCheck), o: ok, want: StepFix,
			check: func(t *testing.T, r Run) {
				if r.Fixes != 1 {
					t.Errorf("fixes = %d", r.Fixes)
				}
			}},
		{name: "check fails at the cap", from: Run{Step: StepCheck, Fixes: MaxFixes}, o: ok, want: StepFailed,
			check: func(t *testing.T, r Run) {
				if r.FailedAt != StepFix || r.Why == "" {
					t.Errorf("failed at %q, why %q", r.FailedAt, r.Why)
				}
			}},
		{name: "fix goes back to check", from: at(StepFix), o: ok, want: StepCheck},
		{name: "fix asks for a revision", from: at(StepFix), o: Outcome{OK: true, Revision: true}, want: StepRevise},
		{name: "fix asks for a third revision", from: Run{Step: StepFix, Revisions: MaxRevisions}, o: Outcome{OK: true, Revision: true}, want: StepFailed,
			check: func(t *testing.T, r Run) {
				if r.FailedAt != StepRevise {
					t.Errorf("failed at %q", r.FailedAt)
				}
			}},
		{name: "revise", from: at(StepRevise), o: ok, want: StepCheck,
			check: func(t *testing.T, r Run) {
				if r.Revisions != 1 {
					t.Errorf("revisions = %d", r.Revisions)
				}
			}},
		{name: "final review with blockers", from: at(StepFinalReview), o: Outcome{OK: true, Blockers: 2}, want: StepWork,
			check: func(t *testing.T, r Run) {
				if !r.FinalReviewed {
					t.Error("the final review happens once")
				}
			}},
		{name: "final review clean", from: at(StepFinalReview), o: ok, want: StepPR},
		{name: "pr", from: at(StepPR), o: ok, want: StepDone},

		{name: "a session step is retried once", from: at(StepPlan), o: failed, want: StepPlan,
			check: func(t *testing.T, r Run) {
				if r.Attempt != 1 || !r.Waiting {
					t.Errorf("retry: %+v", r)
				}
			}},
		{name: "then fails", from: Run{Step: StepPlan, Attempt: 1}, o: failed, want: StepFailed,
			check: func(t *testing.T, r Run) {
				if r.FailedAt != StepPlan || r.Why != "no plan.md" {
					t.Errorf("failed at %q, why %q", r.FailedAt, r.Why)
				}
			}},
		{name: "a claude gate gets three tries", from: Run{Step: StepVerifyReview, Attempt: 1}, o: failed, want: StepVerifyReview},
		{name: "and fails on the third", from: Run{Step: StepVerifyReview, Attempt: 2}, o: failed, want: StepFailed},
		{name: "the pr gets three tries", from: Run{Step: StepPR, Attempt: 2}, o: failed, want: StepFailed},
		{name: "a finished run stays finished", from: at(StepDone), o: failed, want: StepDone},
		{name: "a new step clears the session", from: Run{Step: StepPlan, Session: "S", Start: "abc", Prompted: true, Stopped: true}, o: ok, want: StepPlanReview,
			check: func(t *testing.T, r Run) {
				if r.Session != "" || r.Start != "" || r.Prompted || r.Stopped || r.Waiting {
					t.Errorf("session left behind: %+v", r)
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Transition(tt.from, tt.o)
			if got.Step != tt.want {
				t.Fatalf("step = %q, want %q", got.Step, tt.want)
			}
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

// Blockers go back to work once: the second time the run passes its check,
// it goes to the PR without another review.
func TestBlockersBounceOnce(t *testing.T) {
	r := at(StepFinalReview)
	r = Transition(r, Outcome{OK: true, Blockers: 1})
	r = Transition(r, Outcome{OK: true})                    // the blocker's task
	r = Transition(r, Outcome{OK: true, CheckPassed: true}) // check
	if r.Step != StepPR {
		t.Errorf("after the blockers the run went to %q, want pr", r.Step)
	}
}

func TestResume(t *testing.T) {
	tests := []struct {
		failedAt Step
		want     Step
		check    func(t *testing.T, r Run)
	}{
		{StepPlan, StepPlan, nil},
		{StepFix, StepCheck, func(t *testing.T, r Run) {
			if r.Fixes != 0 {
				t.Errorf("fixes = %d", r.Fixes)
			}
		}},
		{StepRevise, StepRevise, func(t *testing.T, r Run) {
			if r.Revisions != 0 {
				t.Errorf("revisions = %d", r.Revisions)
			}
		}},
	}
	for _, tt := range tests {
		r := Run{Step: StepFailed, FailedAt: tt.failedAt, Why: "x", Attempt: 2, Fixes: MaxFixes, Revisions: MaxRevisions}
		got := Resume(r)
		if got.Step != tt.want || got.FailedAt != "" || got.Why != "" || got.Attempt != 0 || got.Waiting != tt.want.Session() {
			t.Errorf("resume from %q = %+v", tt.failedAt, got)
		}
		if tt.check != nil {
			tt.check(t, got)
		}
	}
	if r := Resume(at(StepWork)); r.Step != StepWork {
		t.Error("resuming a run that has not failed changed it")
	}
}

func TestRunRoundTrip(t *testing.T) {
	root := t.TempDir()
	if _, ok, err := Load(root, "H"); ok || err != nil {
		t.Fatalf("missing run: %v, %v", ok, err)
	}
	r := Run{Home: "H", Workspace: "C:/src/x", Slug: "add-x-0001", Branch: "nabu/add-x-0001", Worktree: "w",
		Step: StepFix, Fixes: 3, Session: "S1", Start: "abc", Notes: []string{"n"},
		Started: time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC), Updated: time.Date(2026, 9, 30, 2, 0, 0, 0, time.UTC)}
	if err := r.Save(root); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Load(root, "H")
	if err != nil || !ok || !reflect.DeepEqual(got, r) {
		t.Fatalf("round trip = %+v, %v, %v", got, ok, err)
	}
	all, err := LoadAll(root)
	if err != nil || len(all) != 1 {
		t.Fatalf("LoadAll = %d, %v", len(all), err)
	}
	if r.File(PlanFile) != ".nabu/runs/add-x-0001/plan.md" {
		t.Errorf("File = %q", r.File(PlanFile))
	}
}
