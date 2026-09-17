package fixture

import (
	"strings"
	"testing"
)

func TestDedupeRemovesAdjacentRepeats(t *testing.T) {
	got := Dedupe([]string{"a", "a", "b"})
	if strings.Join(got, ",") != "a,b" {
		t.Errorf("got %q, want a,b", got)
	}
}
