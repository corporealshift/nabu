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
		{"tasks", TasksPrompt(r), []string{r.File(PlanFile), r.File(TasksFile), "3 to 8", "- [ ]", `"run: tasks"`}, true},
		{"verify", VerifyPrompt(r), []string{verify, "exits 0 only when the brief is done", "must fail now", `"run: verify"`,
			"the way CI is written", "did not run at all", "never the text of the code", expectation,
			`each entry in the plan's "## Decisions" section a named test`}, false},
		{"work", work, []string{"task 2 of 4", "Add Median", "In stats.go, with tests.", "only this task", "done when " + verify + " passes"}, true},
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
	if !strings.Contains(workGoal, `"Add Median"`) || !strings.Contains(workGoal, verify+" is untouched") {
		t.Errorf("work goal = %q", workGoal)
	}
	if !strings.Contains(fixGoal, r.File(RevisionFile)) {
		t.Errorf("fix goal = %q", fixGoal)
	}
	long, _ := FixPrompt(r, strings.Repeat("x", maxOutput*2), "")
	if len(long) > maxOutput+5000 || !strings.Contains(long, "(earlier output cut)") {
		t.Error("a long failure was not cut")
	}
}

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
			`every entry in the plan's "## Decisions" section has a named test`}},
		{"verify-review, passing before", VerifyReviewPrompt(r, true, "ok"), []string{"PASSES, which proves nothing", "not by checking the source"}},
		{"revise", RevisePrompt(r, "FAIL"), []string{r.File(RevisionFile), "REFUSED", "Do not weaken it", "checks on the text of the source", expectation}},
		{"final-review", FinalReviewPrompt(r, []string{"stats.go", "stats_test.go"}), []string{
			"- stats.go\n- stats_test.go", "Report only blockers", "are NOT blockers", `"blockers"`, "no blockers", expectation,
			"say so in notes, not as a blocker"}},
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
