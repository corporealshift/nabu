package fixture

import "testing"

func TestLast(t *testing.T) {
	if got, ok := Last([]int{1, 2, 3}); !ok || got != 3 {
		t.Errorf("Last([1 2 3]) = %d, %v; want 3, true", got, ok)
	}
	if _, ok := Last(nil); ok {
		t.Error("Last(nil) should report false")
	}
}
