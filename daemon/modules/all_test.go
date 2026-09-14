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

// P1c registers all four policy modules. A module compiled in but left out of
// this list is silently inert, which is how guard nearly shipped unused.
func TestAllPolicyModulesAreRegistered(t *testing.T) {
	want := []string{"skills", "guard", "verify", "report"}
	got := map[string]bool{}
	for _, m := range All {
		got[m.Name()] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("%q is not registered", name)
		}
	}
}

// The first Deny on a tool call wins, so broad safety policy must be asked
// before narrow verification.
func TestGuardIsAskedBeforeVerify(t *testing.T) {
	guardAt, verifyAt := -1, -1
	for i, m := range All {
		switch m.Name() {
		case "guard":
			guardAt = i
		case "verify":
			verifyAt = i
		}
	}
	if guardAt < 0 || verifyAt < 0 {
		t.Skip("both gates must be registered for order to matter")
	}
	if guardAt > verifyAt {
		t.Errorf("guard is at %d and verify at %d: guard must come first", guardAt, verifyAt)
	}
}

// Each hook must actually be implemented, or the registry silently skips it.
func TestModulesImplementTheirHooks(t *testing.T) {
	for _, m := range All {
		switch m.Name() {
		case "guard":
			if _, ok := m.(module.ToolGate); !ok {
				t.Error("guard must be a ToolGate")
			}
		case "skills":
			if _, ok := m.(module.SessionStarter); !ok {
				t.Error("skills must be a SessionStarter")
			}
			if _, ok := m.(module.ToolProvider); !ok {
				t.Error("skills must be a ToolProvider")
			}
		case "verify":
			if _, ok := m.(module.StopGate); !ok {
				t.Error("verify must be a StopGate")
			}
			if _, ok := m.(module.Reporter); !ok {
				t.Error("verify must be a Reporter")
			}
		case "report":
			if _, ok := m.(module.Reporter); !ok {
				t.Error("report must be a Reporter")
			}
		}
	}
}
