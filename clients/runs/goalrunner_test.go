package runs

import (
	"slices"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/protocol"
)

// askGoal is the Goal action on a home: the text as its description, and
// the label.
func (g *rig) askGoal(id, text string) {
	h := g.d.homes[id]
	if h == nil {
		h = &fakeHome{workspace: g.t.TempDir()}
		g.d.homes[id] = h
	}
	if text != "" {
		h.description = text
	}
	h.labels = withPrefix(h.labels, goalPrefix, LabelGoalRequested)
}

func (g *rig) goal(id string) *Goal { return g.rn.goals[id] }

// work takes the run of a goal that has just started through planning, its
// two tasks, and its PR, as its sessions would.
func (g *rig) work(r *Run) {
	g.t.Helper()
	g.finish(map[string]string{r.File(PlanFile): "# Plan\n"})
	g.tick()
	g.finish(map[string]string{r.File(VerifyFile): "go test ./...\n"})
	g.tick()
	g.finish(map[string]string{r.File(TasksFile): twoTasks})
	g.tick()
	g.finish(map[string]string{"stats.go": "x"})
	g.tick()
	g.finish(map[string]string{"stats_test.go": "x"})
	g.tick()
	if r.Step != StepDone {
		g.t.Fatalf("run %s ended at %q (%s)\nlog:\n%s", r.Home, r.Step, r.Why, g.log.String())
	}
}

// currentRun is the run of the goal's current brief.
func (g *rig) currentRun(goal *Goal) *Run {
	g.t.Helper()
	i := goal.Current()
	if i < 0 || goal.Briefs[i].Run == "" {
		g.t.Fatalf("no run under way: %+v", goal)
	}
	return g.run(goal.Briefs[i].Run)
}

// childrenOf is the run homes made under a goal.
func (g *rig) childrenOf(goal string) []string {
	var out []string
	for id, h := range g.d.homes {
		if h.parent == goal {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

func TestAWholeGoal(t *testing.T) {
	g := newRig(t)
	g.askGoal("G1", "Add Median and Mode to stats")
	g.tick()
	goal := g.goal("G1")
	if goal == nil || goal.Step != GoalRuns || len(goal.Briefs) != 2 || goal.Round != 1 {
		t.Fatalf("goal = %+v\nlog:\n%s", goal, g.log.String())
	}

	first := g.currentRun(goal)
	if first.Goal != "G1" || first.Base != goal.Branch || !strings.HasPrefix(first.Brief, "# Median\n\nAdd a Median function") {
		t.Errorf("first run = %+v", first)
	}
	g.work(first)
	g.tick() // the goal sees the first run done and starts the second
	second := g.currentRun(goal)
	if second == first {
		t.Fatal("the second brief did not get its own run")
	}
	g.work(second)
	g.tick() // the goal sees it done, is checked, and opens its PR

	if goal.Step != GoalDone {
		t.Fatalf("goal ended at %q (%s)\nlog:\n%s", goal.Step, goal.Why, g.log.String())
	}
	if want := []string{"main", goal.Branch, goal.Branch}; !slices.Equal(g.git.from, want) {
		t.Errorf("worktrees from %q, want %q", g.git.from, want)
	}
	if len(g.gh.prs) != 3 {
		t.Fatalf("prs = %q", g.gh.prs)
	}
	for _, pr := range g.gh.prs[:2] {
		if !strings.HasPrefix(pr, goal.Branch+"|") || !strings.Contains(pr, "||") {
			t.Errorf("a run's PR is not against the goal branch, unlabeled: %q", pr)
		}
	}
	if final := g.gh.prs[2]; !strings.HasPrefix(final, "main|"+goal.Branch+"|Add Median and Mode to stats|nabu|") ||
		!strings.Contains(final, "both are there, tested") || !strings.Contains(final, "stats has Median and Mode") {
		t.Errorf("final pr = %q", final)
	}
	if len(g.gh.merged) != 2 {
		t.Errorf("merged %v", g.gh.merged)
	}
	if want := []string{"breakdown", "plan", "verify", "final", "plan", "verify", "final", "check"}; !slices.Equal(g.claude.asked, want) {
		t.Errorf("claude was asked %q", g.claude.asked)
	}

	if kids := g.childrenOf("G1"); len(kids) != 2 {
		t.Errorf("run homes under the goal: %q", kids)
	}
	for id, s := range g.d.sessions {
		if s.parent != first.Home && s.parent != second.Home {
			t.Errorf("session %s hangs off %q, not a run", id, s.parent)
		}
	}

	h := g.d.homes["G1"]
	if !slices.Equal(h.labels, []string{"goal:done", "goal:round:1"}) {
		t.Errorf("goal labels = %q", h.labels)
	}
	status := map[string]protocol.TaskStatus{}
	for _, task := range h.tasks {
		status[task.ID] = task.Status
	}
	if want := map[string]protocol.TaskStatus{"b1": protocol.TaskDone, "b2": protocol.TaskDone, "c1": protocol.TaskDone, "pr": protocol.TaskDone}; !mapsEqual(status, want) {
		t.Errorf("goal tasks = %v", status)
	}
	msgs := strings.Join(g.git.history, "\n")
	for _, m := range []string{"goal: round 1 breakdown", "goal: round 1 check"} {
		if !strings.Contains(msgs, m) {
			t.Errorf("no commit %q in\n%s", m, msgs)
		}
	}
	if !slices.Contains(g.git.pushed, goal.Branch) {
		t.Errorf("the goal branch was never pushed: %q", g.git.pushed)
	}
}

func mapsEqual[K comparable, V comparable](a, b map[K]V) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// failRun ends a run as failed, as its own retries running out would.
func failRun(r *Run, at Step, why string) {
	r.Step, r.FailedAt, r.Why, r.Waiting, r.Session = StepFailed, at, why, false, ""
}

func TestAFailedRunIsReplannedThenBlocksThenResumes(t *testing.T) {
	g := newRig(t)
	g.claude.answers["check"] = []string{"```json\n{\"met\":false,\"reason\":\"Median is missing\",\"briefs\":[{\"title\":\"Median, smaller\",\"brief\":\"Add Median for ints only.\"}]}\n```"}
	g.askGoal("G1", "Add Median and Mode to stats")
	g.tick()
	goal := g.goal("G1")

	failRun(g.currentRun(goal), StepPlan, "no plan.md")
	g.tick()
	if goal.Step != GoalRuns || goal.Round != 2 || goal.Failures != 1 {
		t.Fatalf("after one failure the goal = %+v", goal)
	}
	if goal.Briefs[0].State != BriefFailed || goal.Briefs[1].State != BriefDropped || goal.Briefs[2].Title != "Median, smaller" {
		t.Errorf("briefs = %+v", goal.Briefs)
	}
	check := g.claude.prompts["check"][0]
	for _, s := range []string{"the run failed at its plan step: no plan.md", "**Mode**: not started", "Do not give the same brief again"} {
		if !strings.Contains(check, s) {
			t.Errorf("the check was not told %q:\n%s", s, check)
		}
	}

	failRun(g.currentRun(goal), StepFix, "still fails")
	g.tick()
	if goal.Step != GoalBlocked || goal.BlockedAt != GoalRuns || !strings.Contains(goal.Why, "still fails") {
		t.Fatalf("after two failures the goal = %+v", goal)
	}
	h := g.d.homes["G1"]
	if !slices.Contains(h.labels, "goal:blocked") {
		t.Errorf("labels = %q", h.labels)
	}
	if last := h.tasks[len(h.tasks)-1]; last.ID != "blocked" || !strings.Contains(last.Note, "still fails") {
		t.Errorf("the phone does not say why: %+v", last)
	}

	// Kyle edits the goal and asks again. The check is met this time.
	g.askGoal("G1", "Add Median and Mode to stats. Median may take ints only.")
	g.tick()
	if goal.Step != GoalDone || goal.Text != "Add Median and Mode to stats. Median may take ints only." {
		t.Fatalf("after the resume the goal = %+v\nlog:\n%s", goal, g.log.String())
	}
	if !slices.Contains(g.git.history, "goal: updated by the owner") {
		t.Errorf("the new text was not committed: %q", g.git.history)
	}
	if last := g.claude.prompts["check"][1]; !strings.Contains(last, "Median may take ints only") {
		t.Errorf("the check after the resume did not see the new text")
	}
}

func TestTheFinalPRFailingCIGoesBackToCheck(t *testing.T) {
	g := newRig(t)
	pass := poll{state: "OPEN", checks: []Check{{Name: "build", State: "SUCCESS"}}}
	g.gh.polls = []poll{pass, pass, failing}
	// The first check is met; the one after CI fails asks for a fix.
	g.claude.answers["check"] = []string{
		"```json\n{\"met\":true,\"reason\":\"done\"}\n```",
		"```json\n{\"met\":false,\"reason\":\"lint fails\",\"briefs\":[{\"title\":\"Lint\",\"brief\":\"Make lint pass.\"}]}\n```",
	}
	g.askGoal("G1", "Add Median and Mode to stats")
	g.tick()
	goal := g.goal("G1")
	g.work(g.currentRun(goal))
	g.tick()
	g.work(g.currentRun(goal))
	g.tick()

	if goal.Step != GoalRuns || goal.Round != 2 || goal.Briefs[2].Title != "Lint" {
		t.Fatalf("goal = %+v\nlog:\n%s", goal, g.log.String())
	}
	if got := g.claude.prompts["check"]; len(got) != 2 || !strings.Contains(got[1], "gofmt -l found stats.go") {
		t.Errorf("the second check was not shown the failure: %q", got)
	}
}

func TestARestartMidRoundCarriesOn(t *testing.T) {
	g := newRig(t)
	g.askGoal("G1", "Add Median and Mode to stats")
	g.tick()
	g.rn = g.runner(g.rn.Root)
	g.tick()
	g.tick()
	if kids := g.childrenOf("G1"); len(kids) != 1 {
		t.Errorf("after a restart the goal has run homes %q", kids)
	}
	goal := g.goal("G1")
	if goal.Step != GoalRuns || g.currentRun(goal).Step != StepPlan {
		t.Errorf("goal = %+v", goal)
	}
}

func TestAGoalNeedsTextAndClaude(t *testing.T) {
	g := newRig(t)
	g.askGoal("G1", "")
	g.claude.missing = true
	g.askGoal("G2", "Add Median and Mode to stats")
	g.tick()
	for _, id := range []string{"G1", "G2"} {
		if g.goal(id) != nil || !slices.Equal(g.d.homes[id].labels, []string{"goal:blocked"}) {
			t.Errorf("%s: goal %+v, labels %q", id, g.goal(id), g.d.homes[id].labels)
		}
	}
}

func TestBreakdownFailuresBlockAfterThreeTries(t *testing.T) {
	g := newRig(t)
	g.claude.answers["breakdown"] = []string{"ERROR", "no json here", "ERROR"}
	g.askGoal("G1", "Add Median and Mode to stats")
	g.tick()
	g.tick()
	goal := g.goal("G1")
	if goal.Step != GoalBreakdown || goal.Attempt != 2 {
		t.Fatalf("goal = %+v", goal)
	}
	g.tick()
	if goal.Step != GoalBlocked || goal.BlockedAt != GoalBreakdown {
		t.Errorf("goal = %+v", goal)
	}
}
