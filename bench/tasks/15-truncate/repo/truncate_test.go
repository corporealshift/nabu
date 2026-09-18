package fixture

import "testing"

func TestTruncateCuts(t *testing.T) {
	if got := Truncate("abcdefgh", 5); got != "abcd…" {
		t.Errorf("got %q, want %q", got, "abcd…")
	}
}
