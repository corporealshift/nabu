package runs

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/protocol"
)

func event(typ protocol.EventType, data any) protocol.Event {
	b, _ := json.Marshal(data)
	return protocol.Event{Type: typ, Data: b}
}

func assistantTurn(text string) protocol.Event {
	return event(protocol.EventMessage, protocol.MessageData{Role: "assistant", Content: text})
}

// askClaude is a claude.ask call and its answer, made by nabu when source is
// a module, else by the model.
func askClaude(id, source, prompt, answer string) []protocol.Event {
	args, _ := json.Marshal(map[string]string{"prompt": prompt})
	return []protocol.Event{
		event(protocol.EventToolCall, protocol.ToolCallData{CallID: id, Tool: "claude.ask", Arguments: args, Source: source}),
		event(protocol.EventToolResult, protocol.ToolResultData{CallID: id, Tool: "claude.ask", Content: answer, Status: "ok"}),
	}
}

func reflectedRun() (Run, map[string][]protocol.Event) {
	r := Run{Slug: "add-median-abcd", Brief: "Add a Median function to stats.", PRURL: "https://github.com/kyle/x/pull/7",
		History: []Record{
			{Step: StepSetup, OK: true},
			{Step: StepPlan, Session: "S1", OK: true},
			{Step: StepWork, Task: 0, Session: "S2", Why: "the session committed nothing (session S2)"},
			{Step: StepWork, Task: 0, Session: "S3", OK: true},
			{Step: StepCheck, OK: true, Result: "failed", Note: "FAIL TestMedian: got 2, want 2.5"},
			{Step: StepFinalReview, OK: true, Result: "1 blockers", Note: "- Median of an even count: averages the middle two"},
		}}
	logs := map[string][]protocol.Event{
		"S1": {assistantTurn("planning"), assistantTurn("done")},
		"S2": {assistantTurn("a"), assistantTurn("b"), assistantTurn("c")},
		"S3": append(append([]protocol.Event{assistantTurn("trying")},
			askClaude("c1", "module:claude", "nabu is asking on behalf of a smaller local model that has spent 50 turns", "Sort a copy, not the input: sort.Float64s(slices.Clone(xs)).")...),
			askClaude("c2", "", "Which rounding does the brief want?", "Round half to even, as the plan says.")...),
	}
	return r, logs
}

func TestTheDigestTellsTheWholeRun(t *testing.T) {
	r, logs := reflectedRun()
	d := Digest(r, "- [x] Add Median\n  In stats.go.\n", logs)
	for _, want := range []string{
		"Add a Median function to stats.",
		"done: https://github.com/kyle/x/pull/7",
		"Add Median", // the task list, and the task each work step did
		"plan: session S1, 2 turns",
		"work, task 1 (Add Median): session S2, 3 turns: thrown away: the session committed nothing",
		"check: failed", "FAIL TestMedian: got 2, want 2.5",
		"final-review: 1 blockers", "Median of an even count",
		// Each ask, who asked, after how many turns, and the answer in full.
		"nabu asked for the model after 1 turns", "Sort a copy, not the input: sort.Float64s(slices.Clone(xs)).",
		"the model asked after 1 turns", "Which rounding does the brief want?", "Round half to even, as the plan says.",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("the digest lacks %q:\n%s", want, d)
		}
	}
	// nabu's own ask is the same framing every time: only its answer counts.
	if strings.Contains(d, "nabu is asking on behalf") {
		t.Errorf("the digest carries nabu's framing of its own ask:\n%s", d)
	}
}

func TestAFailedRunSaysWhereItFailed(t *testing.T) {
	r, logs := reflectedRun()
	r.PRURL, r.FailedAt, r.Why = "", StepFix, "verify.sh still fails after 10 fixes"
	if d := Digest(r, "", logs); !strings.Contains(d, "failed at fix: verify.sh still fails after 10 fixes") {
		t.Errorf("the digest does not say how the run ended:\n%s", d)
	}
}

func TestTheDigestIsBounded(t *testing.T) {
	r, logs := reflectedRun()
	for i := range 300 {
		r.History = append(r.History, Record{Step: StepCheck, OK: true, Result: "failed", Note: strings.Repeat("x", maxNote)})
		logs["S3"] = append(logs["S3"], askClaude("k"+string(rune('a'+i%26)), "", "q", strings.Repeat("y", 5000))...)
	}
	d := Digest(r, strings.Repeat("- [ ] t\n", 5000), logs)
	if len(d) > maxDigest+100 {
		t.Errorf("digest is %d bytes, bound %d", len(d), maxDigest)
	}
	// The asks come first: they are what got the model unstuck.
	if !strings.Contains(d, "Sort a copy, not the input") {
		t.Error("the bound cut Claude's answers")
	}
}

func TestReflectPrompt(t *testing.T) {
	p := ReflectPrompt(Run{Slug: "add-median-abcd"}, "THE DIGEST")
	for _, want := range []string{
		"THE DIGEST",
		"what worked", "what to do, not what to avoid",
		"at most 5", "memory.save", "same subject", "Saving nothing is a fine answer",
		"Do not change, create or commit any file",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("lacks %q", want)
		}
	}
}

// reflectingRun takes a run with two tasks to its reflection, and leaves it
// there.
func (g *rig) reflectingRun(id string) *Run {
	g.t.Helper()
	g.holdReflect = true
	g.shell.results = []bool{false} // verify-review's run before any work fails, as it should
	r := g.planned(id)
	g.finish(map[string]string{"stats.go": "package stats\n"})
	g.tick()
	g.finish(map[string]string{"stats_test.go": "package stats\n"})
	g.tick()
	if r.Step != StepReflect {
		g.t.Fatalf("run at %q, want reflect\nlog:\n%s", r.Step, g.log.String())
	}
	return r
}

func TestARunReflectsBeforeItEnds(t *testing.T) {
	g := newRig(t)
	r := g.reflectingRun("H1")
	id, s := g.d.last()
	if s.turns != DefaultReflectTurns || len(s.goals) != 0 {
		t.Errorf("reflection session: %d turns, goals %q", s.turns, s.goals)
	}
	for _, want := range []string{"How the run went", "Add a Median function to stats", "Test Median", "check: passed", "memory.save"} {
		if !strings.Contains(s.prompts[0], want) {
			t.Errorf("the reflection's prompt lacks %q:\n%s", want, s.prompts[0])
		}
	}
	start := r.Start
	// A reflection that writes a file anyway leaves nothing behind.
	g.finish(map[string]string{"notes.md": "what I learned"})
	g.tick()
	if r.Step != StepDone {
		t.Fatalf("run at %q after its reflection\nlog:\n%s", r.Step, g.log.String())
	}
	if len(g.git.resets) == 0 || g.git.resets[len(g.git.resets)-1] != start {
		t.Errorf("resets = %q, want the worktree back at %s", g.git.resets, start)
	}
	if last := r.History[len(r.History)-1]; last.Step != StepReflect || last.Session != id {
		t.Errorf("the reflection is not in the history: %+v", last)
	}
}

func TestAReflectionThatFailsStillEndsTheRun(t *testing.T) {
	g := newRig(t)
	r := g.reflectingRun("H1")
	_, s := g.d.last()
	s.state = protocol.StateBlocked
	g.tick()
	if r.Step != StepDone || r.FailedAt != "" {
		t.Errorf("run = %q, failed at %q\nlog:\n%s", r.Step, r.FailedAt, g.log.String())
	}
	if n := len(g.d.order); n != 6 {
		t.Errorf("%d sessions: a reflection is never retried", n)
	}
}

func TestAFailedRunReflectsAndStaysFailed(t *testing.T) {
	g := newRig(t)
	g.holdReflect = true
	r := g.planned("H1")
	for range sessionTries {
		g.finish(map[string]string{"stats.go": "x", r.File(VerifyFile): "exit 0\n"})
		g.tick()
	}
	if r.Step != StepReflect || r.FailedAt != StepWork {
		t.Fatalf("run = %q, failed at %q", r.Step, r.FailedAt)
	}
	if !strings.Contains(g.log.String(), "failed at work") {
		t.Errorf("the failure is not logged when it happens:\n%s", g.log.String())
	}
	_, s := g.d.last()
	if !strings.Contains(s.prompts[0], "failed at work") || !strings.Contains(s.prompts[0], "thrown away") {
		t.Errorf("the digest does not say how the run failed:\n%s", s.prompts[0])
	}
	g.finish(nil)
	g.tick()
	if r.Step != StepFailed || r.FailedAt != StepWork || !strings.Contains(r.Why, "verify.sh") {
		t.Errorf("run = %+v", r)
	}
}

func TestReflectTurnsDefault(t *testing.T) {
	c, err := LoadConfig(t.TempDir())
	if err != nil || c.ReflectTurns != DefaultReflectTurns {
		t.Errorf("ReflectTurns = %d, %v", c.ReflectTurns, err)
	}
}
