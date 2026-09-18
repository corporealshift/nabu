package fixture

import (
	"sort"
	"strings"
	"testing"
)

func TestLessContract(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"1.2.3", "1.2.4", true},
		{"1.2.4", "1.2.3", false},
		{"1.2.3", "1.3.0", true},
		{"1.9.0", "2.0.0", true},
		// Strings say 1.10.0 < 1.9.0. Numbers say otherwise, and numbers are right.
		{"1.9.0", "1.10.0", true},
		{"1.10.0", "1.9.0", false},
		{"2.0.0", "10.0.0", true},
		// A pre-release comes before the release it leads to.
		{"1.0.0-rc1", "1.0.0", true},
		{"1.0.0", "1.0.0-rc1", false},
		{"1.0.0-alpha", "1.0.0-beta", true},
		{"1.0.0-rc1", "1.0.1", true},
		// Equal is not less, either way round.
		{"1.2.3", "1.2.3", false},
	}

	for _, tt := range tests {
		if got := Less(tt.a, tt.b); got != tt.want {
			t.Errorf("Less(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

// The real use: handing Less to sort and getting a release order out.
func TestSortsIntoReleaseOrder(t *testing.T) {
	got := []string{"1.10.0", "1.0.0", "1.2.0", "1.0.0-rc1", "1.9.0", "2.0.0"}
	sort.Slice(got, func(i, j int) bool { return Less(got[i], got[j]) })

	want := "1.0.0-rc1,1.0.0,1.2.0,1.9.0,1.10.0,2.0.0"
	if strings.Join(got, ",") != want {
		t.Errorf("sorted to %s, want %s", strings.Join(got, ","), want)
	}
}
