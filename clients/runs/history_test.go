package runs

import (
	"strings"
	"testing"
)

// The run's reflection reads its history: every step, the session that did
// it, and what came of it (docs/specs/2026-10-11-run-reflection-design.md).
func TestARunKeepsItsHistory(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false, false, true} // before work, then a failing check, then a passing one
	g.claude.answers["final"] = []string{"```json\n{\"blockers\":[{\"title\":\"Median of an even count\",\"detail\":\"averages the middle two\"}],\"notes\":[]}\n```"}
	r := g.planned("H1")
	g.finish(map[string]string{"stats.go": "x"})
	g.tick()
	g.finish(map[string]string{"stats_test.go": "x"})
	g.tick() // the check fails: fix
	g.finish(map[string]string{"stats.go": "y"})
	g.tick() // the check passes, the final review blocks: work on task 3
	g.finish(map[string]string{"stats.go": "z"})
	g.tick() // the check passes, pr, ci

	var steps []string
	for _, h := range r.History {
		steps = append(steps, string(h.Step))
	}
	want := "setup plan plan-review verify verify-review tasks work work check fix check final-review work check pr ci"
	if got := strings.Join(steps, " "); !strings.HasPrefix(got, want) {
		t.Fatalf("history =\n %s\nwant it to start\n %s", got, want)
	}
	h := r.History
	if h[1].Session != "S1" || !h[1].OK || h[2].Session != "" {
		t.Errorf("plan records: %+v, %+v", h[1], h[2])
	}
	if h[6].Task != 0 || h[7].Task != 1 || h[12].Task != 2 || h[7].Session != "S5" {
		t.Errorf("work records: %+v, %+v, %+v", h[6], h[7], h[12])
	}
	if !strings.Contains(h[8].Note, "FAIL TestMedian") || h[10].Note != "" {
		t.Errorf("check notes: %q, %q", h[8].Note, h[10].Note)
	}
	if !strings.Contains(h[11].Note, "Median of an even count") {
		t.Errorf("final review note: %q", h[11].Note)
	}
	if h[8].Result != "failed" || h[10].Result != "passed" || h[11].Result != "1 blockers" || h[15].Result != CIPass {
		t.Errorf("results: check %q, check %q, final review %q, ci %q", h[8].Result, h[10].Result, h[11].Result, h[15].Result)
	}
}

func TestAFailedTryIsInTheHistory(t *testing.T) {
	g := newRig(t)
	r := g.planned("H1")
	g.finish(map[string]string{"stats.go": "x", r.File(VerifyFile): "exit 0\n"})
	g.tick()
	last := r.History[len(r.History)-1]
	if last.Step != StepWork || last.OK || last.Session != "S4" || !strings.Contains(last.Why, "changed verify.sh") {
		t.Errorf("the thrown-away try: %+v", last)
	}
}

func TestANoteIsCutFromTheFront(t *testing.T) {
	long := strings.Repeat("early line\n", 1000) + "FAIL TestMedian at the end"
	got := Record{Note: long}.cut().Note
	if len(got) > maxNote+50 || !strings.HasSuffix(got, "FAIL TestMedian at the end") || !strings.HasPrefix(got, "(earlier output cut)") {
		t.Errorf("cut note is %d bytes: %q…", len(got), got[:60])
	}
}
