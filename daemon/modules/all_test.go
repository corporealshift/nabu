package modules

import (
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
)

// Guard is always compiled in: spec 14.7 makes it the answer to the
// "unguarded first run" problem, so forgetting to register it would silently
// leave every session approving everything.
func TestGuardIsRegistered(t *testing.T) {
	var found bool
	for _, m := range All {
		if m.Name() == "guard" {
			found = true
			if _, ok := m.(module.ToolGate); !ok {
				t.Error("the registered guard does not implement ToolGate, so nothing would gate a tool call")
			}
		}
	}
	if !found {
		t.Fatal("guard is not in the registration list")
	}
}

// Two modules sharing a name would make config sections ambiguous.
func TestRegisteredNamesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range All {
		name := m.Name()
		if name == "" {
			t.Error("a registered module has no name")
		}
		if seen[name] {
			t.Errorf("two modules are registered as %q", name)
		}
		seen[name] = true
	}
}
