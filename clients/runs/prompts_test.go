package runs

import (
	"strings"
	"testing"
)

func TestSessionPrompts(t *testing.T) {
	r := Run{Slug: "add-median-abcd", Base: "main"}
	verify := r.File(VerifyFile)
	work, workGoal := WorkPrompt(r, 1, 4, Task{Title: "Add Median", Detail: "In stats.go, with tests."})
	fix, fixGoal := FixPrompt(r, "FAIL TestMedian", "Median must not sort its input in place.")
	tests := []struct {
		name, prompt string
		want         []string
		// forbids is whether the prompt forbids touching verify.sh; only the
		// verify step may write it.
		forbids bool
	}{
		{"brief", BriefPrompt(r, "user: add a median"), []string{r.File(BriefFile), "user: add a median", `"run: brief"`}, true},
		{"plan", PlanPrompt(r), []string{r.File(BriefFile), r.File(PlanFile), `"run: plan"`, expectation,
			`headed exactly "## Decisions"`, "the question, what the plan chooses, the alternative"}, true},
		{"tasks", TasksPrompt(r), []string{r.File(PlanFile), r.File(TasksFile), "3 to 8", "- [ ]", `"run: tasks"`,
			"Read the check, " + verify, "exactly one task", "under the exact name " + verify + " uses"}, true},
		{"verify", VerifyPrompt(r), []string{verify, "exits 0 only when the brief is done", "must fail now", `"run: verify"`,
			"the way CI is written", "did not run at all", "never the text of the code", expectation,
			"not one for each detail of how it is built", "rules out or says is not needed", "the cheapest test",
			`"## Decisions"`, "Do not add a test only to pin a decision"}, false},
		{"work", work, []string{"task 2 of 4", "Add Median", "In stats.go, with tests.", "only this task", "done when " + verify + " passes",
			"write each under exactly that name"}, true},
		{"fix", fix, []string{"FAIL TestMedian", r.File(RevisionFile), "must not sort its input in place"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, s := range tt.want {
				if !strings.Contains(tt.prompt, s) {
					t.Errorf("lacks %q", s)
				}
			}
			for _, s := range []string{"there is no one to ask", "web.search", "claude.ask", "Do not push"} {
				if !strings.Contains(tt.prompt, s) {
					t.Errorf("lacks %q", s)
				}
			}
			if got := strings.Contains(tt.prompt, "Never create, edit, rename or delete "+verify); got != tt.forbids {
				t.Errorf("forbids touching verify.sh: %v, want %v", got, tt.forbids)
			}
		})
	}
	// The steps that plan draft in their file, not their thinking
	// (docs/specs/2026-10-17-plan-in-the-file-design.md).
	for name, p := range map[string][2]string{
		"plan":   {PlanPrompt(r), r.File(PlanFile)},
		"tasks":  {TasksPrompt(r), r.File(TasksFile)},
		"verify": {VerifyPrompt(r), verify},
	} {
		if !strings.Contains(p[0], "Work it out in "+p[1]+", not in your thinking. Write a first draft as soon as you know enough to") {
			t.Errorf("%s is not told to draft in %s", name, p[1])
		}
	}
	if strings.Contains(BriefPrompt(r, "x"), "not in your thinking") || strings.Contains(work, "not in your thinking") {
		t.Error("only the planning steps are told to draft in their file")
	}

	// The check is written before the tasks, from the brief and the plan.
	// One named test per decision made the check ask for tests nobody planned
	// (docs/specs/2026-10-09-verify-before-tasks-design.md).
	if v := VerifyPrompt(r); strings.Contains(v, r.File(TasksFile)) || strings.Contains(v, "a named test of its own") {
		t.Error("the verify prompt still reads the tasks, or still asks for a test per decision")
	}
	if !strings.Contains(workGoal, `"Add Median"`) || !strings.Contains(workGoal, verify+" is untouched") {
		t.Errorf("work goal = %q", workGoal)
	}
	if !strings.Contains(fixGoal, r.File(RevisionFile)) {
		t.Errorf("fix goal = %q", fixGoal)
	}
	// Every session that carries out the plan may say it cannot work, and is
	// judged done when it has (docs/specs/2026-10-12-run-replan-design.md).
	ciFix, ciFixGoal := CIFixPrompt(r, "lint failed")
	for name, p := range map[string][2]string{"work": {work, workGoal}, "fix": {fix, fixGoal}, "ci-fix": {ciFix, ciFixGoal}} {
		if !strings.Contains(p[0], "Write "+r.File(ReplanFile)) || !strings.Contains(p[1], r.File(ReplanFile)+" explains why the plan cannot work") {
			t.Errorf("%s is not offered the claim:\nprompt: %s\ngoal: %s", name, p[0], p[1])
		}
		if strings.Contains(p[0], "says it can") {
			t.Errorf("%s carries advice nobody gave", name)
		}
	}
	advised := r
	advised.ReplanAdvice = "the fixture is in testFixtures"
	for name, p := range map[string]string{
		"work":   first(WorkPrompt(advised, 0, 1, Task{Title: "x"})),
		"fix":    first(FixPrompt(advised, "FAIL", "")),
		"ci-fix": first(CIFixPrompt(advised, "FAIL")),
	} {
		if !strings.Contains(p, "says it can. Their note:\n\nthe fixture is in testFixtures") {
			t.Errorf("%s lacks the reviewer's advice", name)
		}
	}
	for name, p := range map[string]string{"plan": PlanPrompt(r), "verify": VerifyPrompt(r), "tasks": TasksPrompt(r)} {
		if strings.Contains(p, r.File(FindingsFile)) {
			t.Errorf("%s names findings before any re-plan", name)
		}
	}
	replanned := r
	replanned.Replans = 1
	for name, p := range map[string]string{"plan": PlanPrompt(replanned), "verify": VerifyPrompt(replanned), "tasks": TasksPrompt(replanned)} {
		if !strings.Contains(p, r.File(FindingsFile)+" says what was found: read it first") {
			t.Errorf("%s after a re-plan does not name the findings", name)
		}
	}

	long, _ := FixPrompt(r, strings.Repeat("x", maxOutput*2), "")
	if len(long) > maxOutput+5000 || !strings.Contains(long, "(earlier output cut)") {
		t.Error("a long failure was not cut")
	}
}

func first(prompt, _ string) string { return prompt }

func TestClaudePrompts(t *testing.T) {
	r := Run{Slug: "add-median-abcd", Base: "main"}
	tests := []struct {
		name, prompt string
		want         []string
	}{
		{"plan-review", PlanReviewPrompt(r), []string{r.File(PlanFile), Begin(PlanFile), End(PlanFile), "NO CHANGES", expectation,
			"keep every entry", "Changed by review: the plan chose X", `"Added by review"`}},
		{"verify-review, failing before", VerifyReviewPrompt(r, false, "exit 1"), []string{"it fails", Begin(VerifyFile), "APPROVED", "Git Bash",
			"the way CI would", "checks behavior only", "Take out any such check", expectation,
			"rules out or says is not needed", "Take out any named test that goes beyond the brief", "needs no test of its own"}},
		{"verify-review, passing before", VerifyReviewPrompt(r, true, "ok"), []string{"PASSES, which proves nothing", "not by checking the source"}},
		{"revise", RevisePrompt(r, "FAIL"), []string{r.File(RevisionFile), "REFUSED", "Do not weaken it", "checks on the text of the source", expectation,
			"and no stricter"}},
		{"replan-review", ReplanReviewPrompt(r, `task 2 of tasks.md, "Add Median"`), []string{`task 2 of tasks.md, "Add Median"`,
			r.File(ReplanFile), r.File(BriefFile), r.File(PlanFile), r.File(TasksFile), "REPLAN on its own line", "REFUSED on its own line",
			"is not a plan that cannot work", "the brief itself"}},
		{"final-review", FinalReviewPrompt(r, []string{"stats.go", "stats_test.go"}), []string{
			"- stats.go\n- stats_test.go", "Report only blockers", "are NOT blockers", `"blockers"`, "no blockers", expectation,
			"say so in notes, not as a blocker"}},
	}
	if v := VerifyReviewPrompt(r, false, ""); strings.Contains(v, r.File(TasksFile)) || strings.Contains(v, "has a named test") {
		t.Error("the verify review still reads the tasks, or still requires a test per decision")
	}
	if f := FinalReviewPrompt(r, nil); strings.Contains(f, "pins each decision") {
		t.Error("the final review still says the check pins each decision")
	}
	// Seen live: a blocker said "Name it in verify.sh", and the work session
	// it became may not touch verify.sh on pain of being thrown away.
	if f := FinalReviewPrompt(r, nil); strings.Contains(f, "does not actually prove") ||
		!strings.Contains(f, "Never ask for a change to "+r.File(VerifyFile)) {
		t.Error("the final review may still ask for a change to verify.sh")
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.HasPrefix(tt.prompt, reviewer) {
				t.Error("does not say it is a read-only review")
			}
			for _, s := range tt.want {
				if !strings.Contains(tt.prompt, s) {
					t.Errorf("lacks %q", s)
				}
			}
		})
	}
}
