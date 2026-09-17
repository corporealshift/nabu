package fixture

import "testing"

func TestMergeOverlapping(t *testing.T) {
	got := Merge([]Range{{1, 5}, {3, 8}})
	if len(got) != 1 || got[0] != (Range{1, 8}) {
		t.Errorf("Merge = %v, want [{1 8}]", got)
	}
}
