package agent

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/modules/ask"
	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/protocol"
)

// A module can offer a tool to some sessions only. The ask module offers ask
// to sessions a person can answer, and not to one labeled unattended: there it
// is neither in the request nor callable.
func TestAToolOfferedToSomeSessionsOnly(t *testing.T) {
	h := newHarness(t, []module.Module{&ask.Module{}}, nil)
	attended := h.create(t)
	unattended, err := h.m.Create(context.Background(), h.dir, CreateOptions{Labels: []string{protocol.LabelUnattended}})
	if err != nil {
		t.Fatal(err)
	}
	offered := func(id string) []string {
		hd, err := h.m.handle(id)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, s := range h.m.toolsFor(provider.Config{}, hd) {
			names = append(names, s.Name)
		}
		return names
	}
	if got := offered(attended.ID()); !slices.Contains(got, "ask") || !slices.Contains(got, "bash") {
		t.Errorf("an attended session is offered %q", got)
	}
	if got := offered(unattended.ID()); slices.Contains(got, "ask") || !slices.Contains(got, "bash") {
		t.Errorf("an unattended session is offered %q", got)
	}

	hd, _ := h.m.handle(unattended.ID())
	res := h.m.executeTool(context.Background(), hd, protocol.ToolCallData{CallID: "c1", Tool: "ask",
		Arguments: json.RawMessage(`{"question":"which?"}`)})
	if res.Status != "error" || res.Kind != protocol.ToolErrorNotFound {
		t.Errorf("calling ask unoffered: %+v", res)
	}
}
