package runs

import (
	"context"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/protocol"
)

// A run's steps copy their home's context size
// (docs/specs/2026-10-08-context-size-design.md).
func TestARunsStepsCopyItsHomesContext(t *testing.T) {
	for _, size := range []string{protocol.ContextLarge, protocol.ContextNormal, ""} {
		t.Run("home "+size, func(t *testing.T) {
			g := newRig(t)
			g.ask("H1", "Add a Median function to stats")
			g.d.homes["H1"].context = size
			g.planned("H1")
			if len(g.d.order) == 0 {
				t.Fatal("no sessions were made")
			}
			for _, id := range g.d.order {
				if got := g.d.sessions[id].context; got != size {
					t.Errorf("session %s context = %q, want the home's %q", id, got, size)
				}
			}
		})
	}
}

// A goal's run homes copy the goal home's size, and so pass it on to their
// steps.
func TestAGoalsRunsCopyItsContext(t *testing.T) {
	g := newRig(t)
	g.askGoal("G1", "Add Median and Mode to stats")
	g.d.homes["G1"].context = protocol.ContextLarge
	g.tick()
	first := g.currentRun(g.goal("G1"))
	if got := g.d.homes[first.Home].context; got != protocol.ContextLarge {
		t.Errorf("run home context = %q, want large", got)
	}
	g.tick()
	_, s := g.d.last()
	if s.context != protocol.ContextLarge {
		t.Errorf("the run's first step context = %q, want large", s.context)
	}
}

// A home that cannot be read gives the daemon's default, and says why.
func TestAnUnreadableHomeGivesTheDefault(t *testing.T) {
	g := newRig(t)
	if got := g.rn.contextOf(context.Background(), g.d, "missing"); got != "" {
		t.Errorf("context = %q, want empty", got)
	}
	if !strings.Contains(g.log.String(), "context size unknown") {
		t.Errorf("nothing logged:\n%s", g.log.String())
	}
}
