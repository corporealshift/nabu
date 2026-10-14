package runs

import (
	"slices"
	"strings"
	"testing"
)

// skip is /skip on a run's home.
func (g *rig) skip(id string) {
	h := g.d.homes[id]
	h.labels = withRun(h.labels, LabelSkip)
}

// skipGoal is /skip on a goal's home.
func (g *rig) skipGoal(id string) {
	h := g.d.homes[id]
	h.labels = withPrefix(h.labels, goalPrefix, LabelGoalSkip)
}

// parkedAtPlanReview is a run waiting at plan-review for Claude, which is
// not answering.
func (g *rig) parkedAtPlanReview(id string) *Run {
	g.t.Helper()
	g.claude.answers["plan"] = []string{"LIMIT", "LIMIT"}
	g.ask(id, "Add a Median function")
	g.tick()
	r := g.run(id)
	g.finish(map[string]string{r.File(PlanFile): "p"})
	g.tick()
	if r.Step != StepPlanReview || r.Parked == "" {
		g.t.Fatalf("not parked: %+v", r)
	}
	return r
}

func TestASkipPassesAParkedGate(t *testing.T) {
	g := newRig(t)
	r := g.parkedAtPlanReview("H1")
	asked := len(g.claude.asked)
	g.skip("H1")
	g.tick()

	if r.Step != StepVerify || r.Parked != "" || r.Skip {
		t.Fatalf("run = %+v\nlog:\n%s", r, g.log.String())
	}
	if len(g.claude.asked) != asked {
		t.Errorf("claude was asked at a skipped gate: %q", g.claude.asked[asked:])
	}
	last := r.History[len(r.History)-1]
	if last.Step != StepPlanReview || !last.OK || last.Result != "skipped by the owner" {
		t.Errorf("history row = %+v", last)
	}
	if !slices.Equal(r.Skipped, []Step{StepPlanReview}) {
		t.Errorf("skipped = %v", r.Skipped)
	}
	if labels := g.d.homes["H1"].labels; !slices.Equal(labels, []string{"run:verify"}) {
		t.Errorf("labels = %q", labels)
	}
	log := g.log.String()
	if !strings.Contains(log, "plan-review skipped by the owner") || strings.Contains(log, "Claude answered") {
		t.Errorf("log:\n%s", log)
	}
}

// The check is not run just to show Claude the output it will not see.
func TestASkippedVerifyReviewKeepsTheCheck(t *testing.T) {
	g := newRig(t)
	g.claude.answers["verify"] = []string{"LIMIT"}
	g.ask("H1", "Add a Median function")
	g.tick()
	r := g.run("H1")
	g.finish(map[string]string{r.File(PlanFile): "p"})
	g.tick()
	g.finish(map[string]string{r.File(VerifyFile): "go test ./...\n"})
	g.tick() // verify-review parks
	if r.Step != StepVerifyReview || r.Parked == "" {
		t.Fatalf("run = %+v", r)
	}
	runs := g.shell.runs
	g.skip("H1")
	g.tick()
	if r.Step != StepTasks || g.shell.runs != runs {
		t.Errorf("run = %+v, check ran %d more times", r, g.shell.runs-runs)
	}
	if text, _ := g.rn.read(r, VerifyFile); text != "go test ./...\n" {
		t.Errorf("verify.sh = %q", text)
	}
}

func TestASkippedRevisionKeepsVerifyAndTellsTheFix(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false, false, false}
	g.claude.answers["revise"] = []string{"LIMIT"}
	r := g.planned("H1")
	g.finish(map[string]string{"stats.go": "x"})
	g.tick()
	g.finish(map[string]string{"stats_test.go": "x"})
	g.tick() // check fails; fix
	g.finish(map[string]string{r.File(RevisionFile): "the check is wrong"})
	g.tick() // revise parks
	if r.Step != StepRevise || r.Parked == "" {
		t.Fatalf("run = %+v", r)
	}
	g.skip("H1")
	g.tick() // skipped; the check fails again; the next fix starts

	if r.Step != StepFix || r.Revisions != 1 {
		t.Fatalf("run = %+v\nlog:\n%s", r, g.log.String())
	}
	if text, _ := g.rn.read(r, VerifyFile); text != "go test ./...\n" {
		t.Errorf("verify.sh = %q", text)
	}
	if _, ok := g.rn.read(r, RevisionFile); ok {
		t.Error("the revision request was left in the worktree")
	}
	if !slices.Contains(g.git.messages(), "run: revision request answered by the owner") {
		t.Errorf("commits = %q", g.git.messages())
	}
	_, s := g.d.last()
	if !strings.Contains(s.prompts[0], "The owner kept verify.sh as it is") {
		t.Errorf("the fix was not told:\n%s", s.prompts[0])
	}
}

func TestASkippedReplanReviewKeepsThePlan(t *testing.T) {
	g := newRig(t)
	g.claude.answers["replan"] = []string{"LIMIT"}
	r := g.planned("H1")
	g.finish(map[string]string{r.File(ReplanFile): "there is no stats package"})
	g.tick() // replan-review parks
	if r.Step != StepReplanReview || r.Parked == "" {
		t.Fatalf("run = %+v", r)
	}
	g.skip("H1")
	g.tick() // skipped; task 1 starts again

	if r.Step != StepWork || r.Refusals != 1 || r.Replans != 0 {
		t.Fatalf("run = %+v\nlog:\n%s", r, g.log.String())
	}
	if _, ok := g.rn.read(r, ReplanFile); ok {
		t.Error("the request was left in the worktree")
	}
	_, s := g.d.last()
	if !strings.Contains(s.prompts[0], "the plan stands") {
		t.Errorf("the session was not told:\n%s", s.prompts[0])
	}
}

// The PR says the final review was skipped, rather than that Claude
// reviewed the finished work.
func TestASkippedFinalReviewIsInThePR(t *testing.T) {
	g := newRig(t)
	g.claude.answers["final"] = []string{"LIMIT"}
	r := g.planned("H1")
	g.finish(map[string]string{"stats.go": "x"})
	g.tick()
	g.finish(map[string]string{"stats_test.go": "x"})
	g.tick() // check passes; final-review parks
	if r.Step != StepFinalReview || r.Parked == "" {
		t.Fatalf("run = %+v", r)
	}
	g.skip("H1")
	g.tick()

	if r.Step != StepDone || len(r.Notes) != 0 {
		t.Fatalf("run = %+v\nlog:\n%s", r, g.log.String())
	}
	if len(g.gh.prs) != 1 {
		t.Fatalf("prs = %q", g.gh.prs)
	}
	body := g.gh.prs[0]
	if !strings.Contains(body, "Claude reviewed the plan and the check. The owner skipped Claude at final-review") ||
		strings.Contains(body, "the finished work;") {
		t.Errorf("pr = %q", body)
	}
}

// A skip given where there is no gate does nothing, and is not kept for the
// next gate, which the owner has not seen.
func TestASkipOffAGateDoesNothing(t *testing.T) {
	g := newRig(t)
	r := g.planned("H1")
	g.skip("H1")
	g.tick()
	if r.Step != StepWork || r.Skip {
		t.Fatalf("run = %+v", r)
	}
	if labels := g.d.homes["H1"].labels; !slices.Equal(labels, []string{"run:work"}) {
		t.Errorf("labels = %q", labels)
	}
	if !strings.Contains(g.log.String(), "not one of Claude's gates; nothing skipped") {
		t.Errorf("log:\n%s", g.log.String())
	}
	g.finish(map[string]string{"stats.go": "x"})
	g.tick()
	g.finish(map[string]string{"stats_test.go": "x"})
	g.tick()
	if r.Step != StepDone || !slices.Contains(g.claude.asked, "final") || len(r.Skipped) != 0 {
		t.Errorf("run = %+v, claude asked %q", r, g.claude.asked)
	}
}

func TestReviewed(t *testing.T) {
	for _, c := range []struct {
		skipped []Step
		want    string
	}{
		{nil, ". Claude reviewed the plan, the check and the finished work; any blockers it found were fixed before this PR.\n"},
		{[]Step{StepPlanReview}, ". Claude reviewed the check and the finished work. The owner skipped Claude at plan-review, so Claude did not review what those gates check. Any blockers the final review found were fixed before this PR.\n"},
		{[]Step{StepPlanReview, StepVerifyReview, StepFinalReview}, ". The owner skipped Claude at plan-review, verify-review, final-review, so Claude did not review what those gates check.\n"},
		{[]Step{StepRevise}, ". Claude reviewed the plan, the check and the finished work. The owner skipped Claude at revise, so Claude did not review what those gates check. Any blockers the final review found were fixed before this PR.\n"},
	} {
		if got := reviewed(c.skipped); got != c.want {
			t.Errorf("reviewed(%v) =\n%q\nwant\n%q", c.skipped, got, c.want)
		}
	}
}

func TestASkippedGoalCheckIsMetAndSaysSo(t *testing.T) {
	g := newRig(t)
	g.claude.answers["breakdown"] = []string{oneBrief}
	g.claude.answers["check"] = []string{"LIMIT", "LIMIT"}
	g.askGoal("G1", "Add Median to stats")
	g.tick()
	goal := g.goal("G1")
	g.work(g.currentRun(goal))
	g.tick() // the check parks
	if goal.Step != GoalCheck || goal.Parked == "" {
		t.Fatalf("goal = %+v", goal)
	}
	g.skipGoal("G1")
	g.tick()

	if goal.Step != GoalDone || goal.Parked != "" || goal.Skip {
		t.Fatalf("goal = %+v\nlog:\n%s", goal, g.log.String())
	}
	if n := len(goal.Verdicts); n != 1 || !goal.Verdicts[0].Met || !goal.Verdicts[0].Skipped {
		t.Errorf("verdicts = %+v", goal.Verdicts)
	}
	if n := strings.Count(strings.Join(g.claude.asked, " "), "check"); n != 1 {
		t.Errorf("claude was asked for the check %d times", n)
	}
	final := g.gh.prs[len(g.gh.prs)-1]
	if !strings.Contains(final, "## Not checked by Claude") || strings.Contains(final, "Claude's verdict") {
		t.Errorf("final pr = %q", final)
	}
}

// The breakdown writes the briefs, so nothing can stand in for it.
func TestAGoalBreakdownCannotBeSkipped(t *testing.T) {
	g := newRig(t)
	g.claude.answers["breakdown"] = []string{"LIMIT", "LIMIT"}
	g.askGoal("G1", "Add Median to stats")
	g.tick()
	goal := g.goal("G1")
	g.skipGoal("G1")
	g.tick()
	if goal.Step != GoalBreakdown || goal.Skip || goal.Parked == "" {
		t.Fatalf("goal = %+v", goal)
	}
	if labels := g.d.homes["G1"].labels; !slices.Equal(labels, []string{"goal:breakdown"}) {
		t.Errorf("labels = %q", labels)
	}
	if !strings.Contains(g.log.String(), "which only the check allows; nothing skipped") {
		t.Errorf("log:\n%s", g.log.String())
	}
}
