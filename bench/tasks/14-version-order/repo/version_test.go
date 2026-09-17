package fixture

import "testing"

func TestLessByPatch(t *testing.T) {
	if !Less("1.2.3", "1.2.4") {
		t.Error("1.2.3 should come before 1.2.4")
	}
}
