package runs

import (
	"slices"
	"strings"
	"testing"

	"github.com/corporealshift/rho/protocol"
)

// A session may say the plan cannot work, and Claude judges it
// (docs/specs/2026-10-12-run-replan-design.md).

func TestAnAgreedClaimPlansTheRunAgain(t *testing.T) {
	g := newRig(t)
	g.claude.answers["replan"] = []string{"I read stats.go and the build.\n\nREPLAN\nThere is no stats package; the functions live in mathx."}
	r := g.planned("H1")
	g.finish(map[string]string{"stats.go": "package stats\n", r.File(ReplanFile): "stats does not exist; I tried creating it and the build rejects it."})
	g.tick() // replan-review agrees; the plan session starts

	if r.Step != StepPlan || r.Replans != 1 {
		t.Fatalf("run = %+v\nlog:\n%s", r, g.log.String())
	}
	if !slices.Contains(g.claude.asked, "replan") {
		t.Fatalf("claude was asked %q", g.claude.asked)
	}
	if p := g.claude.prompts["replan"][0]; !strings.Contains(p, `task 1 of `+r.File(TasksFile)+`, "Add Median"`) || !strings.Contains(p, r.File(ReplanFile)) {
		t.Errorf("the review does not say what the session was on:\n%s", p)
	}
	findings, ok := g.rn.read(r, FindingsFile)
	if !ok || !strings.Contains(findings, "the functions live in mathx") || !strings.Contains(findings, "at task 1") {
		t.Errorf("findings.md = %q", findings)
	}
	if _, ok := g.rn.read(r, ReplanFile); ok {
		t.Error("the request was left in the worktree")
	}
	msgs := g.git.messages()
	if i := slices.Index(msgs, "run: what the plan could not do"); i < 0 || msgs[i+1] != "run: re-plan request answered" {
		t.Errorf("commits = %q", msgs)
	}
	if !slices.Contains(msgs, "session: "+r.File(ReplanFile)+",stats.go") {
		t.Errorf("the session's own work was not kept: %q", msgs)
	}
	var results []string
	for _, rec := range r.History {
		results = append(results, rec.Result)
	}
	if !slices.Contains(results, "said the plan cannot work") || !slices.Contains(results, "re-planned") {
		t.Errorf("history results = %q", results)
	}

	// The planner reads what was found, and plans only what remains.
	_, s := g.d.last()
	for _, want := range []string{r.File(FindingsFile), "planned again", "git log origin/main..HEAD", "plan only what remains"} {
		if !strings.Contains(s.prompts[0], want) {
			t.Errorf("the plan prompt lacks %q", want)
		}
	}
	g.finish(map[string]string{r.File(PlanFile): "# Plan, in mathx\n"})
	g.tick()
	_, s = g.d.last()
	if r.Step != StepVerify || !strings.Contains(s.prompts[0], r.File(FindingsFile)) {
		t.Fatalf("verify after a re-plan: step %q, prompt:\n%s", r.Step, s.prompts[0])
	}
	g.finish(map[string]string{r.File(VerifyFile): "go test ./mathx/...\n"})
	g.tick()
	_, s = g.d.last()
	if r.Step != StepTasks || !strings.Contains(s.prompts[0], r.File(FindingsFile)) || !strings.Contains(s.prompts[0], "all unticked") {
		t.Fatalf("tasks after a re-plan: step %q, prompt:\n%s", r.Step, s.prompts[0])
	}
	g.finish(map[string]string{r.File(TasksFile): "- [ ] Add Median to mathx\n  In mathx/median.go.\n"})
	g.tick()
	if r.Step != StepWork {
		t.Fatalf("after planning again the run is at %q", r.Step)
	}
	g.finish(map[string]string{"mathx/median.go": "package mathx\n"})
	g.tick()
	if r.Step != StepDone {
		t.Errorf("run ended at %q (%s)\nlog:\n%s", r.Step, r.Why, g.log.String())
	}
}

func TestARefusedClaimAdvisesTheSameTask(t *testing.T) {
	g := newRig(t)
	g.claude.answers["replan"] = []string{"REFUSED\nstats.go exists under internal/stats; add Median there."}
	r := g.planned("H1")
	g.finish(map[string]string{r.File(ReplanFile): "there is no stats package"})
	g.tick() // replan-review refuses; task 1 starts again

	if r.Step != StepWork || r.Refusals != 1 || r.Replans != 0 {
		t.Fatalf("run = %+v\nlog:\n%s", r, g.log.String())
	}
	_, s := g.d.last()
	if !strings.Contains(s.prompts[0], "task 1 of 2") || !strings.Contains(s.prompts[0], "add Median there.") {
		t.Errorf("the advice did not reach task 1's next session:\n%s", s.prompts[0])
	}
	if _, ok := g.rn.read(r, ReplanFile); ok {
		t.Error("the request was left in the worktree")
	}
	if _, ok := g.rn.read(r, FindingsFile); ok {
		t.Error("a refused claim was written to findings.md")
	}
	g.finish(map[string]string{"internal/stats/median.go": "x"})
	g.tick()
	if r.ReplanAdvice != "" || r.Task != 1 {
		t.Errorf("after the task the advice is spent and the next task is up: %+v", r)
	}
	_, s = g.d.last()
	if strings.Contains(s.prompts[0], "add Median there.") {
		t.Error("the advice reached the next task")
	}
}

func TestAFixMayClaimThePlanCannotWork(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false, false}
	g.claude.answers["replan"] = []string{"REPLAN: the check needs an emulator, and there is none."}
	r := g.planned("H1")
	g.finish(map[string]string{"stats.go": "x"})
	g.tick()
	g.finish(map[string]string{"stats_test.go": "x"})
	g.tick() // check fails; fix
	if r.Step != StepFix {
		t.Fatalf("run at %q", r.Step)
	}
	_, s := g.d.last()
	if !strings.Contains(s.prompts[0], r.File(ReplanFile)) || !strings.Contains(s.goals[0], r.File(ReplanFile)) {
		t.Errorf("the fix session is not offered the claim:\nprompt: %s\ngoal: %s", s.prompts[0], s.goals[0])
	}
	g.finish(map[string]string{r.File(ReplanFile): "no emulator"})
	g.tick()
	if r.Step != StepPlan || r.Fixes != 0 {
		t.Fatalf("run = %+v", r)
	}
	if findings, _ := g.rn.read(r, FindingsFile); !strings.Contains(findings, "at fix") {
		t.Errorf("findings.md = %q", findings)
	}
}

func TestAReplanMustRewriteThePlan(t *testing.T) {
	g := newRig(t)
	g.claude.answers["replan"] = []string{"REPLAN\nno stats package"}
	r := g.planned("H1")
	g.finish(map[string]string{r.File(ReplanFile): "no stats package"})
	g.tick()
	if r.Step != StepPlan {
		t.Fatalf("run at %q", r.Step)
	}
	g.finish(nil) // ends its turn, leaving the old plan
	g.tick()
	_, s := g.d.last()
	if len(s.prompts) != 2 || !strings.Contains(s.prompts[1], "without rewriting "+r.File(PlanFile)) {
		t.Fatalf("the session was not told to rewrite the plan: %q", s.prompts)
	}
	g.finish(nil)
	g.tick()
	if r.Step != StepPlan || r.Attempt != 1 {
		t.Errorf("a plan session that kept the old plan should be tried again: %+v", r)
	}
}

func TestAClaimTheReviewCannotReadIsTriedAgain(t *testing.T) {
	g := newRig(t)
	g.claude.answers["replan"] = []string{"I am not sure.", "REFUSED\ngo on"}
	r := g.planned("H1")
	g.finish(map[string]string{r.File(ReplanFile): "x"})
	g.tick()
	if r.Step != StepReplanReview || r.Attempt != 1 {
		t.Fatalf("run = %+v", r)
	}
	if _, ok := g.rn.read(r, ReplanFile); !ok {
		t.Fatal("the request went before Claude answered it")
	}
	g.tick()
	if r.Step != StepWork || r.ReplanAdvice != "go on" {
		t.Errorf("run = %+v", r)
	}
}

// ask logs a claude.ask call and its answer in a session, as the host logs
// the claude module's own asks: adjacent, with the call's source.
func ask(s *fakeSession, id, source, status, answer string) {
	s.add(protocol.EventToolCall, protocol.ToolCallData{CallID: id, Tool: "claude.ask", Source: source})
	s.add(protocol.EventToolResult, protocol.ToolResultData{CallID: id, Tool: "claude.ask", Status: status, Content: answer})
}

func TestClaudesVerdictStopsTheSessionAndPlansAgain(t *testing.T) {
	g := newRig(t)
	r := g.planned("H1")
	id, s := g.d.last()
	g.tick() // running, nothing to see yet
	if r.Step != StepWork || s.state != protocol.StateRunning {
		t.Fatalf("run at %q, session %q", r.Step, s.state)
	}
	g.git.commit("session: stats.go", "stats.go")
	g.git.dirty["scratch.go"] = true
	ask(s, "a1", claudeSource, "ok", "**PLAN CANNOT WORK**\nRoom's in-memory database needs Robolectric, which this module does not have.")
	s.say("assistant", "Let me try once more.")

	g.tick() // the verdict is seen; the session is stopped
	if s.state != protocol.StateCompleted {
		t.Fatalf("session %s was not stopped: %q", id, s.state)
	}
	g.tick() // and acted on
	if r.Step != StepPlan || r.Replans != 1 || r.Finding != "" {
		t.Fatalf("run = %+v\nlog:\n%s", r, g.log.String())
	}
	findings, _ := g.rn.read(r, FindingsFile)
	if !strings.Contains(findings, "needs Robolectric") || strings.Contains(findings, planVerdict) {
		t.Errorf("findings.md = %q", findings)
	}
	if !slices.Contains(g.git.messages(), "session: stats.go") || len(g.git.dirty) != 0 {
		t.Errorf("committed work should stay and the rest go: %q, dirty %v", g.git.messages(), g.git.dirty)
	}
	if rec := r.History[len(r.History)-1]; rec.Step != StepWork || rec.Result != "Claude judged the plan cannot work" || rec.Session != id {
		t.Errorf("history = %+v", rec)
	}
	if slices.Contains(g.claude.asked, "replan") {
		t.Error("Claude's own verdict was sent to Claude for review")
	}
}

func TestOnlyTheModulesVerdictCounts(t *testing.T) {
	g := newRig(t)
	r := g.planned("H1")
	_, s := g.d.last()
	g.tick()
	ask(s, "m1", "model", "ok", "PLAN CANNOT WORK\nas you suspected")
	ask(s, "a1", claudeSource, "error", "PLAN CANNOT WORK")
	ask(s, "a2", claudeSource, "ok", "The plan is fine. PLAN CANNOT WORK would be wrong here.")
	g.tick()
	if r.Step != StepWork || r.Finding != "" || s.state != protocol.StateRunning {
		t.Fatalf("run = %+v, session %q", r, s.state)
	}
	last := s.log[len(s.log)-1].ID
	if r.Seen != last {
		t.Errorf("seen = %q, want %q", r.Seen, last)
	}
	saved, _, _ := Load(g.rn.Root, "H1")
	if saved.Seen != last {
		t.Errorf("the cursor was not saved: %q", saved.Seen)
	}
	g.tick()
	if got := g.d.reads[len(g.d.reads)-1]; got != last {
		t.Errorf("the next read started at %q, want %q", got, last)
	}
}

func TestVerdictIn(t *testing.T) {
	s := &fakeSession{}
	s.say("user", "do task 1")
	s.add(protocol.EventToolCall, protocol.ToolCallData{CallID: "a1", Tool: "claude.ask", Source: claudeSource})
	if finding, seen := verdictIn(s.log); finding != "" || seen != "e1" {
		t.Errorf("a call waiting for its result: %q, %q", finding, seen)
	}
	s.add(protocol.EventToolResult, protocol.ToolResultData{CallID: "a1", Tool: "claude.ask", Status: "ok", Content: "PLAN CANNOT WORK: no emulator"})
	if finding, seen := verdictIn(s.log[1:]); finding != "no emulator" || seen != "e3" {
		t.Errorf("read again with its result: %q, %q", finding, seen)
	}
	if finding, seen := verdictIn(nil); finding != "" || seen != "" {
		t.Errorf("nothing new: %q, %q", finding, seen)
	}
	only := &fakeSession{}
	only.add(protocol.EventToolCall, protocol.ToolCallData{CallID: "a1", Tool: "claude.ask", Source: claudeSource})
	if _, seen := verdictIn(only.log); seen != "" {
		t.Errorf("a lone waiting call moved the cursor to %q", seen)
	}
}
