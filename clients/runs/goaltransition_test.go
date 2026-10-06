package runs

import (
	"strings"
	"testing"
)

// twoBriefs is a goal in round 1 with two briefs, the first running.
func twoBriefs() Goal {
	return Goal{Home: "G", Step: GoalRuns, Round: 1, RoundsFrom: 1, Briefs: []GoalBrief{
		{Title: "A", Round: 1, State: BriefRunning, Run: "R1"},
		{Title: "B", Round: 1, State: BriefPending},
	}}
}

func TestGoalTransition(t *testing.T) {
	ok := GoalOutcome{OK: true}
	next := []GoalBrief{{Title: "C", Brief: "do C"}}
	tests := []struct {
		name  string
		from  Goal
		o     GoalOutcome
		want  GoalStep
		check func(t *testing.T, g Goal)
	}{
		{name: "setup", from: Goal{Step: GoalSetup}, o: ok, want: GoalBreakdown},
		{name: "breakdown starts round 1", from: Goal{Step: GoalBreakdown},
			o: GoalOutcome{OK: true, DoneWhen: []string{"x"}, Briefs: []GoalBrief{{Title: "A"}, {Title: "B"}}}, want: GoalRuns,
			check: func(t *testing.T, g Goal) {
				if g.Round != 1 || g.RoundsFrom != 1 || len(g.Briefs) != 2 || g.Briefs[1].State != BriefPending || g.Briefs[1].Round != 1 || g.DoneWhen[0] != "x" {
					t.Errorf("goal = %+v", g)
				}
			}},
		{name: "a run ends and the next brief is up", from: twoBriefs(), o: GoalOutcome{OK: true, Run: RunEnded, PRURL: "pr/1", Notes: []string{"n"}}, want: GoalRuns,
			check: func(t *testing.T, g Goal) {
				if b := g.Briefs[0]; b.State != BriefDone || b.PRURL != "pr/1" || len(b.Notes) != 1 || g.Current() != 1 {
					t.Errorf("goal = %+v", g)
				}
			}},
		{name: "the last run ends the round", from: func() Goal {
			g := twoBriefs()
			g.Briefs[0].State = BriefDone
			g.Briefs[1].State = BriefRunning
			return g
		}(),
			o: GoalOutcome{OK: true, Run: RunEnded}, want: GoalCheck},
		{name: "a run that ends clears the failures", from: func() Goal { g := twoBriefs(); g.Failures = 1; return g }(),
			o: GoalOutcome{OK: true, Run: RunEnded}, want: GoalRuns,
			check: func(t *testing.T, g Goal) {
				if g.Failures != 0 {
					t.Errorf("failures = %d", g.Failures)
				}
			}},
		{name: "a failed run ends the round early", from: twoBriefs(), o: GoalOutcome{OK: true, Run: RunFailed, FailedAt: StepFix, RunWhy: "still fails"}, want: GoalCheck,
			check: func(t *testing.T, g Goal) {
				if g.Briefs[0].State != BriefFailed || g.Briefs[0].FailedAt != StepFix || g.Briefs[1].State != BriefDropped || g.Failures != 1 {
					t.Errorf("goal = %+v", g)
				}
			}},
		{name: "a second failure in a row blocks", from: func() Goal { g := twoBriefs(); g.Failures = 1; return g }(),
			o: GoalOutcome{OK: true, Run: RunFailed, FailedAt: StepPlan, RunWhy: "no plan"}, want: GoalBlocked,
			check: func(t *testing.T, g Goal) {
				if g.BlockedAt != GoalRuns || !strings.Contains(g.Why, "no plan") {
					t.Errorf("blocked at %q: %q", g.BlockedAt, g.Why)
				}
			}},
		{name: "a round with nothing left goes to check", from: Goal{Step: GoalRuns, Round: 1}, o: GoalOutcome{OK: true, Run: RoundOver}, want: GoalCheck},
		{name: "met", from: Goal{Step: GoalCheck, Round: 1, RoundsFrom: 1, CIFailure: "x"}, o: GoalOutcome{OK: true, Met: true, Reason: "done"}, want: GoalPR,
			check: func(t *testing.T, g Goal) {
				if len(g.Verdicts) != 1 || !g.Verdicts[0].Met || g.CIFailure != "" {
					t.Errorf("goal = %+v", g)
				}
			}},
		{name: "unmet starts the next round", from: Goal{Step: GoalCheck, Round: 1, RoundsFrom: 1}, o: GoalOutcome{OK: true, Reason: "no C", Briefs: next}, want: GoalRuns,
			check: func(t *testing.T, g Goal) {
				if g.Round != 2 || len(g.Briefs) != 1 || g.Briefs[0].Round != 2 || g.Current() != 0 || g.Verdicts[0].Met {
					t.Errorf("goal = %+v", g)
				}
			}},
		{name: "unmet at the round cap blocks", from: Goal{Step: GoalCheck, Round: 5, RoundsFrom: 1}, o: GoalOutcome{OK: true, Reason: "no C", Briefs: next}, want: GoalBlocked,
			check: func(t *testing.T, g Goal) {
				if g.BlockedAt != GoalCheck || !strings.Contains(g.Why, "after 5 rounds: no C") || len(g.Verdicts) != 1 {
					t.Errorf("goal = %+v", g)
				}
			}},
		{name: "the cap counts from a resume", from: Goal{Step: GoalCheck, Round: 5, RoundsFrom: 3}, o: GoalOutcome{OK: true, Briefs: next}, want: GoalRuns},
		{name: "pr", from: Goal{Step: GoalPR}, o: ok, want: GoalCI},
		{name: "ci passes", from: Goal{Step: GoalCI}, o: GoalOutcome{OK: true, CI: CIPass}, want: GoalDone},
		{name: "ci merged", from: Goal{Step: GoalCI}, o: GoalOutcome{OK: true, CI: CIMerged}, want: GoalDone},
		{name: "ci fails back to check", from: Goal{Step: GoalCI}, o: GoalOutcome{OK: true, CI: CIFail}, want: GoalCheck},
		{name: "the PR closed blocks", from: Goal{Step: GoalCI}, o: GoalOutcome{OK: true, CI: CIClosed}, want: GoalBlocked},
		{name: "a failed try waits for the next poll", from: Goal{Step: GoalBreakdown}, o: GoalOutcome{Why: "claude timed out"}, want: GoalBreakdown,
			check: func(t *testing.T, g Goal) {
				if g.Attempt != 1 {
					t.Errorf("attempt = %d", g.Attempt)
				}
			}},
		{name: "the third failed try blocks", from: Goal{Step: GoalCheck, Attempt: otherTries - 1}, o: GoalOutcome{Why: "no verdict"}, want: GoalBlocked,
			check: func(t *testing.T, g Goal) {
				if g.BlockedAt != GoalCheck || g.Why != "no verdict" {
					t.Errorf("goal = %+v", g)
				}
			}},
		{name: "ci gets more tries", from: Goal{Step: GoalCI, Attempt: otherTries}, o: GoalOutcome{Why: "gh failed"}, want: GoalCI},
		{name: "waiting changes nothing", from: twoBriefs(), o: GoalOutcome{Wait: true}, want: GoalRuns},
		{name: "a finished goal stays finished", from: Goal{Step: GoalDone}, o: GoalOutcome{OK: true, CI: CIClosed}, want: GoalDone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := GoalTransition(tt.from, tt.o, 5)
			if g.Step != tt.want {
				t.Fatalf("step = %q, want %q (%+v)", g.Step, tt.want, g)
			}
			if tt.check != nil {
				tt.check(t, g)
			}
		})
	}
}

func TestResumeGoal(t *testing.T) {
	tests := []struct {
		name string
		from Goal
		want GoalStep
	}{
		{"blocked by failed runs goes to check", Goal{Step: GoalBlocked, BlockedAt: GoalRuns, Round: 2, Failures: 2}, GoalCheck},
		{"blocked by the round cap goes to check", Goal{Step: GoalBlocked, BlockedAt: GoalCheck, Round: 5}, GoalCheck},
		{"blocked at breakdown tries it again", Goal{Step: GoalBlocked, BlockedAt: GoalBreakdown}, GoalBreakdown},
		{"blocked at setup tries it again", Goal{Step: GoalBlocked, BlockedAt: GoalSetup}, GoalSetup},
		{"blocked at pr tries it again", Goal{Step: GoalBlocked, BlockedAt: GoalPR}, GoalPR},
		{"blocked at ci opens the PR again", Goal{Step: GoalBlocked, BlockedAt: GoalCI}, GoalPR},
		{"a goal that is not blocked is left alone", Goal{Step: GoalRuns}, GoalRuns},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := ResumeGoal(tt.from)
			if g.Step != tt.want || g.BlockedAt != "" || g.Why != "" || g.Attempt != 0 {
				t.Errorf("goal = %+v", g)
			}
			if tt.from.Step == GoalBlocked && (tt.from.BlockedAt == GoalRuns || tt.from.BlockedAt == GoalCheck) {
				if g.Failures != 0 || g.RoundsFrom != tt.from.Round+1 {
					t.Errorf("failures %d, rounds from %d", g.Failures, g.RoundsFrom)
				}
				// A fresh allowance: the check after a resume may start a round.
				if after := GoalTransition(g, GoalOutcome{OK: true, Briefs: []GoalBrief{{Title: "C"}}}, 5); after.Step != GoalRuns {
					t.Errorf("after the resume the check went to %q", after.Step)
				}
			}
		})
	}
}
