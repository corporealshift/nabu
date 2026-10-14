package tui

import (
	"reflect"
	"strings"
	"testing"
)

func TestSkipLabels(t *testing.T) {
	tests := []struct {
		have, want []string
		why        string
	}{
		{have: []string{"mine", "run:plan-review"}, want: []string{"mine", "run:skip"}},
		{have: []string{"run:final-review"}, want: []string{"run:skip"}},
		{have: []string{"run:check", "run:attempt:2/10"}, why: "the run is at check, which is not one of Claude's checks"},
		{have: []string{"goal:check", "goal:round:3"}, want: []string{"goal:skip"}},
		{have: []string{"goal:breakdown"}, why: "only its check can be skipped"},
		{have: []string{"mine"}, why: "for a run's or a goal's home"},
	}
	for _, tt := range tests {
		got, why := skipLabels(tt.have)
		if !reflect.DeepEqual(got, tt.want) || !strings.Contains(why, tt.why) || (tt.why == "") != (why == "") {
			t.Errorf("skipLabels(%q) = %q, %q; want %q, %q", tt.have, got, why, tt.want, tt.why)
		}
	}
}

func TestSkipCommand(t *testing.T) {
	m := newModel("S1", nil)
	m.labels = []string{"mine", "run:verify-review"}
	if _, a := submit(t, m, "/skip"); a == nil || a.kind != actSkip || !reflect.DeepEqual(a.labels, []string{"mine", "run:skip"}) {
		t.Fatalf("action = %+v", a)
	}
	m.labels = []string{"run:work"}
	if _, a := submit(t, m, "/skip"); a != nil {
		t.Errorf("/skip off a gate = %+v", a)
	}
	if !strings.Contains(helpText, "/skip") {
		t.Error("/help does not list /skip")
	}
}
