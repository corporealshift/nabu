package runs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/corporealshift/rho/protocol"
)

// A step session takes the runner's effort for its step when its home has
// none, and nothing for a step the map leaves out
// (docs/specs/2026-10-16-session-effort-design.md).
func TestStepsTakeTheMapsEffort(t *testing.T) {
	g := newRig(t)
	g.rn.Cfg.Effort = map[Step]string{StepPlan: protocol.EffortMedium, StepVerify: protocol.EffortLow}
	g.planned("H1")
	// planned makes the plan, verify, tasks and first work sessions, in order.
	want := []string{"medium", "low", "", ""}
	if len(g.d.order) != len(want) {
		t.Fatalf("sessions: %d, want %d", len(g.d.order), len(want))
	}
	for i, id := range g.d.order {
		if got := g.d.sessions[id].effort; got != want[i] {
			t.Errorf("session %d effort = %q, want %q", i, got, want[i])
		}
	}
}

// A home's own effort wins over the map, for every step.
func TestAHomesEffortBeatsTheMap(t *testing.T) {
	g := newRig(t)
	g.rn.Cfg.Effort = map[Step]string{StepPlan: protocol.EffortMedium}
	g.ask("H1", "Add a Median function to stats")
	g.d.homes["H1"].effort = protocol.EffortHigh
	g.planned("H1")
	for _, id := range g.d.order {
		if got := g.d.sessions[id].effort; got != protocol.EffortHigh {
			t.Errorf("session %s effort = %q, want the home's high", id, got)
		}
	}
}

// A goal's run homes copy the goal home's effort, and so pass it on to their
// steps.
func TestAGoalsRunsCopyItsEffort(t *testing.T) {
	g := newRig(t)
	g.rn.Cfg.Effort = map[Step]string{StepPlan: protocol.EffortMedium}
	g.askGoal("G1", "Add Median and Mode to stats")
	g.d.homes["G1"].effort = protocol.EffortLow
	g.tick()
	first := g.currentRun(g.goal("G1"))
	if got := g.d.homes[first.Home].effort; got != protocol.EffortLow {
		t.Errorf("run home effort = %q, want low", got)
	}
	g.tick()
	if _, s := g.d.last(); s.effort != protocol.EffortLow {
		t.Errorf("the run's first step effort = %q, want low", s.effort)
	}
}

// A map naming a step that makes no session, or a level the daemon would
// refuse, stops the runner at load, naming the key.
func TestAnEffortMapIsCheckedAtLoad(t *testing.T) {
	for _, tc := range []struct {
		config, want string
	}{
		{`{"effort": {"plann": "low"}}`, `"plann"`},
		{`{"effort": {"pr": "low"}}`, `"pr"`},
		{`{"effort": {"plan": "extreme"}}`, "effort.plan"},
	} {
		root := t.TempDir()
		os.MkdirAll(filepath.Join(root, "runner"), 0o755)
		os.WriteFile(ConfigPath(root), []byte(tc.config), 0o644)
		_, err := LoadConfig(root)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one naming %s", tc.config, err, tc.want)
		}
	}

	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "runner"), 0o755)
	os.WriteFile(ConfigPath(root), []byte(`{"effort": {"plan": "medium", "work": "", "reflect": "low"}}`), 0o644)
	c, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if c.Effort[StepPlan] != "medium" || c.Effort[StepReflect] != "low" {
		t.Errorf("effort = %v", c.Effort)
	}
}
