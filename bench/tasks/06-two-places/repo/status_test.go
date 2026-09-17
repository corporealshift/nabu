package fixture

import "testing"

func TestEveryStatusHasALabel(t *testing.T) {
	for _, s := range AllStatuses {
		if Label[s] == "" {
			t.Errorf("status %q has no label", s)
		}
	}
}
