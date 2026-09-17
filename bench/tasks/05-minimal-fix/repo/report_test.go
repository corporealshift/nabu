package fixture

import "testing"

func TestDetail(t *testing.T) {
	if Detail("run", 1, 2) == "" {
		t.Error("empty")
	}
}
