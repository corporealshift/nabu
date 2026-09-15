package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// fillIndex writes n memories into the global store.
func fillIndex(t *testing.T, m *Module, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := m.GlobalStore().Save(Memory{
			Name: fmt.Sprintf("memory-%04d", i), Type: "user",
			Description: "a fact", Body: "the body",
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUnderTheCapNoConsolidation(t *testing.T) {
	m, h := curatorModule(t, `{}`, nil)
	fillIndex(t, m, 5)

	m.consolidate(context.Background(), newSession(t, "repo-abc123"))

	if h.model.calls != 0 {
		t.Errorf("made %d model calls under the cap", h.model.calls)
	}
}

func TestOverTheCapAsksWithNamesAndDescriptionsOnly(t *testing.T) {
	m, h := curatorModule(t, `{"merge":[],"forget":[]}`, nil)
	fillIndex(t, m, maxIndexLines+2)

	m.consolidate(context.Background(), newSession(t, "repo-abc123"))

	if h.model.calls != 1 {
		t.Fatalf("made %d model calls over the cap, want 1", h.model.calls)
	}
	prompt := h.model.lastReq.Messages[0].Content
	if !strings.Contains(prompt, "memory-0000") {
		t.Error("the prompt does not list the memories")
	}
	if strings.Contains(prompt, "the body") {
		t.Error("bodies were sent; names and descriptions keep the common case cheap")
	}
}

func TestMergeWritesTheSurvivorAndForgetsTheRest(t *testing.T) {
	reply := `{"merge":[{"into":"memory-0000","absorb":["memory-0001","memory-0002"],
	  "description":"one fact instead of three","body":"the merged body"}],"forget":[]}`
	m, h := curatorModule(t, reply, nil)
	fillIndex(t, m, maxIndexLines+2)
	before := len(m.mustLoadGlobal(t))

	m.consolidate(context.Background(), newSession(t, "repo-abc123"))

	after := m.mustLoadGlobal(t)
	if len(after) != before-2 {
		t.Errorf("got %d memories, want %d: the absorbed ones should be gone", len(after), before-2)
	}
	var survivor *Memory
	for i := range after {
		if after[i].Name == "memory-0000" {
			survivor = &after[i]
		}
	}
	if survivor == nil {
		t.Fatal("the merge target is gone")
	}
	if !strings.Contains(survivor.Body, "the merged body") {
		t.Errorf("the survivor was not rewritten: %q", survivor.Body)
	}
	// Every change is a tool call, so git records the pass.
	for _, c := range h.tools.calls {
		if !strings.HasPrefix(c, "memory.save ") && !strings.HasPrefix(c, "memory.forget ") {
			t.Errorf("unexpected call %q", c)
		}
	}
}

func TestMergeIntoAnUnknownNameIsDropped(t *testing.T) {
	reply := `{"merge":[{"into":"never-existed","absorb":["memory-0001"],
	  "description":"d","body":"b"}],"forget":[]}`
	m, _ := curatorModule(t, reply, nil)
	fillIndex(t, m, maxIndexLines+2)
	before := len(m.mustLoadGlobal(t))

	m.consolidate(context.Background(), newSession(t, "repo-abc123"))

	if after := len(m.mustLoadGlobal(t)); after != before {
		t.Errorf("a merge into a name that does not exist changed %d memories", before-after)
	}
}

func TestForgetOfAnUnknownNameIsNotAnError(t *testing.T) {
	m, _ := curatorModule(t, `{"merge":[],"forget":["never-existed"]}`, nil)
	fillIndex(t, m, maxIndexLines+2)
	before := len(m.mustLoadGlobal(t))

	m.consolidate(context.Background(), newSession(t, "repo-abc123"))

	if after := len(m.mustLoadGlobal(t)); after != before {
		t.Errorf("forgetting a name that does not exist changed the store")
	}
}

func TestConsolidationIsBounded(t *testing.T) {
	var merges, forgets []string
	for i := 0; i < 6; i++ {
		merges = append(merges, fmt.Sprintf(
			`{"into":"memory-%04d","absorb":["memory-%04d"],"description":"d","body":"b"}`, i*2, i*2+1))
		forgets = append(forgets, fmt.Sprintf(`"memory-01%02d"`, i))
	}
	reply := `{"merge":[` + strings.Join(merges, ",") + `],"forget":[` + strings.Join(forgets, ",") + `]}`
	m, _ := curatorModule(t, reply, nil)
	fillIndex(t, m, maxIndexLines+2)
	before := len(m.mustLoadGlobal(t))

	m.consolidate(context.Background(), newSession(t, "repo-abc123"))

	// Three merges absorb three, three deletions remove three.
	removed := before - len(m.mustLoadGlobal(t))
	if removed > maxConsolidationPerPass*2 {
		t.Errorf("removed %d memories in one pass, want at most %d",
			removed, maxConsolidationPerPass*2)
	}
}

func TestConsolidationCannotEmptyTheStore(t *testing.T) {
	m, _ := curatorModule(t, `{"merge":[],"forget":[]}`, nil)
	fillIndex(t, m, 3)
	// Force the attempt even though it is under the cap.
	var names []string
	for _, mem := range m.mustLoadGlobal(t) {
		names = append(names, `"`+mem.Name+`"`)
	}
	plan := parseConsolidation(`{"merge":[],"forget":[` + strings.Join(names, ",") + `]}`)
	if plan.wouldEmpty(3) {
		// The guard is the point: a plan that removes everything is refused.
		return
	}
	t.Error("a plan that removes every memory should be refused")
}

func TestImportedMemoriesAreNeverTouched(t *testing.T) {
	m, h := curatorModule(t, `{"merge":[],"forget":["from-claude"]}`, nil)
	fillIndex(t, m, maxIndexLines+2)

	// An imported memory is not nabu's to delete.
	m.imports = append(m.imports, NewStore(t.TempDir(), ScopeGlobal, true))
	m.consolidate(context.Background(), newSession(t, "repo-abc123"))

	for _, c := range h.tools.calls {
		if strings.Contains(c, "from-claude") {
			t.Errorf("tried to remove an imported memory: %q", c)
		}
	}
}

func TestConsolidationIsSkippedWhenTheCuratorIsOff(t *testing.T) {
	m, h := curatorModule(t, `{"merge":[],"forget":[]}`, module.Config{"curator": false})
	fillIndex(t, m, maxIndexLines+2)

	m.consolidate(context.Background(), newSession(t, "repo-abc123"))

	if h.model.calls != 0 {
		t.Errorf("a disabled curator consolidated")
	}
}

// A pass that pushes the index over its cap consolidates in the same breath.
func TestAPassConsolidatesWhenItGoesOverTheCap(t *testing.T) {
	m, h := curatorModule(t, `{"merge":[],"forget":[]}`, nil)
	fillIndex(t, m, maxIndexLines+2)
	s := newSession(t, "repo-abc123")
	s.events = []protocol.Event{userMsg("01A", "something happened")}

	m.curate(context.Background(), s)

	// One call for the proposals, one for consolidation.
	if h.model.calls != 2 {
		t.Errorf("made %d model calls, want the pass plus consolidation", h.model.calls)
	}
}
