package verify

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
)

// The workspace overlay is never applied, so one global command ran in every
// repository or none. A per-workspace gate lets breezeway build its Android app
// at every stop without nabu's own sessions doing it too.
func TestAWorkspaceCommandReplacesTheGlobalOne(t *testing.T) {
	android, other, off := t.TempDir(), t.TempDir(), t.TempDir()
	m := newVerify(t, module.Config{
		"command":            "exit 0",
		"require_clean_tree": false,
		"commands": map[string]any{
			// Written with the other separator and case, as a person would.
			strings.ToUpper(filepath.ToSlash(android)): "echo building the app; exit 4",
			off: "",
		},
	})

	v := m.BeforeStop(context.Background(), &fakeSession{workspace: android}, module.StopInfo{})
	if v.Allow || !strings.Contains(v.Reason, "echo building the app") || !strings.Contains(v.Reason, "building the app") {
		t.Errorf("the workspace's own gate should run and veto, got allow=%v %q", v.Allow, v.Reason)
	}
	if v := m.BeforeStop(context.Background(), &fakeSession{workspace: other}, module.StopInfo{}); !v.Allow {
		t.Errorf("another workspace should get the global gate, got %q", v.Reason)
	}
	s := &fakeSession{workspace: off}
	if v := m.BeforeStop(context.Background(), s, module.StopInfo{}); !v.Allow {
		t.Errorf("an empty workspace command turns the gate off, got %q", v.Reason)
	}
	if got := s.checkEvents(); len(got) != 0 {
		t.Errorf("a gate that is off should record nothing, got %+v", got)
	}
}

func TestWorkspaceCommandsWithoutAGlobalOne(t *testing.T) {
	ws := t.TempDir()
	m := newVerify(t, module.Config{"require_clean_tree": false,
		"commands": map[string]any{ws: "exit 2"}})
	if v := m.BeforeStop(context.Background(), &fakeSession{workspace: ws}, module.StopInfo{}); v.Allow {
		t.Error("the workspace gate should run with no global command")
	}
	if v := m.BeforeStop(context.Background(), &fakeSession{workspace: t.TempDir()}, module.StopInfo{}); !v.Allow {
		t.Errorf("no gate anywhere else, got %q", v.Reason)
	}
}

func TestWorkspaceCommandsConfigErrors(t *testing.T) {
	for _, tc := range []struct {
		raw  any
		want string
	}{
		{"cargo test", "must be an object"},
		{map[string]any{"C:/x": 3.0}, "must be a string"},
	} {
		err := (&Module{}).Init(nil, module.Config{"commands": tc.raw})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("commands=%v: want %q, got %v", tc.raw, tc.want, err)
		}
	}
}
