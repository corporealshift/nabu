package runs

import (
	"slices"
	"testing"

	"github.com/corporealshift/rho/protocol"
)

func TestStepSessionOptions(t *testing.T) {
	opts := stepOptions("01HOME", "large")
	labels, _ := opts["labels"].([]string)
	// memory:no-curator: each step is one try of one task; the run reflects
	// as a whole when it ends (docs/specs/2026-10-11-run-reflection-design.md).
	for _, want := range []string{NoPushLabel, protocol.LabelUnattended, NoCuratorLabel} {
		if !slices.Contains(labels, want) {
			t.Errorf("labels %q lack %q", labels, want)
		}
	}
	if opts["parent"] != "01HOME" || opts["context"] != "large" || opts["permission_mode"] != string(protocol.PermissionAuto) {
		t.Errorf("options = %v", opts)
	}
	if _, ok := stepOptions("01HOME", "")["context"]; ok {
		t.Error("an unknown context size is sent rather than left to the daemon")
	}
}
