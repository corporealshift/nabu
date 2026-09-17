package fixture

import (
	"strings"
	"testing"
)

func TestWrap(t *testing.T) {
	tests := []struct {
		text  string
		width int
		want  []string
	}{
		{"", 10, []string{""}},
		{"short", 10, []string{"short"}},
		{"the quick brown fox", 10, []string{"the quick", "brown fox"}},
		{"supercalifragilistic", 10, []string{"supercalif", "ragilistic"}},
	}

	for _, tt := range tests {
		got := Wrap(tt.text, tt.width)
		if strings.Join(got, "|") != strings.Join(tt.want, "|") {
			t.Errorf("Wrap(%q, %d) = %q, want %q", tt.text, tt.width, got, tt.want)
		}
	}
}
