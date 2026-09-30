package protocol

import (
	"strings"
	"testing"
)

func TestValidLabels(t *testing.T) {
	many := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "l"
		}
		return out
	}
	tests := []struct {
		name   string
		labels []string
		ok     bool
	}{
		{"none", nil, true},
		{"run labels", []string{"run:requested", "run:attempt:3/10", "issue:kyle/bw/12"}, true},
		{"a hash", []string{"issue:kyle/bw#12"}, false},
		{"every allowed mark", []string{"a:b_c.d/e-f", "0123456789"}, true},
		{"sixteen", many(16), true},
		{"seventeen", many(17), false},
		{"sixty-four characters", []string{strings.Repeat("a", 64)}, true},
		{"sixty-five characters", []string{strings.Repeat("a", 65)}, false},
		{"empty label", []string{""}, false},
		{"a capital", []string{"Run:plan"}, false},
		{"a space", []string{"run plan"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidLabels(tt.labels); (err == nil) != tt.ok {
				t.Errorf("ValidLabels(%q) = %v, want ok %v", tt.labels, err, tt.ok)
			}
		})
	}
}
