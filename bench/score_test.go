package bench

import (
	"strings"
	"testing"
)

func TestViolationsFindsForbiddenEdits(t *testing.T) {
	tests := []struct {
		name      string
		changed   []string
		unchanged []string
		want      string
	}{
		{"nothing forbidden", []string{"a.go"}, nil, ""},
		{"kept the rule", []string{"a.go"}, []string{"a_test.go"}, ""},
		{"broke the rule", []string{"a.go", "a_test.go"}, []string{"a_test.go"}, "a_test.go"},
		{"broke several", []string{"b_test.go", "a_test.go"}, []string{"a_test.go", "b_test.go"}, "a_test.go,b_test.go"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Join(violations(tt.changed, tt.unchanged), ",")
			if got != tt.want {
				t.Errorf("violations = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOutcome(t *testing.T) {
	tests := []struct {
		name     string
		attempt  Attempt
		ranAtAll bool
		verified bool
		runnable bool
		broke    []string
		want     Outcome
	}{
		{"did the task", Attempt{}, true, true, true, nil, Passed},
		{"tried and failed", Attempt{}, true, false, true, nil, Failed},
		{"ran out of time", Attempt{TimedOut: true}, true, false, true, nil, TimedOut},
		{"could not be run", Attempt{}, false, false, true, nil, Errored},
		{"the fixture is broken", Attempt{}, true, false, false, nil, Unusable},
		{"edited the test", Attempt{}, true, true, true, []string{"a_test.go"}, Violated},
		{
			"edited the test and still failed",
			Attempt{}, true, false, true, []string{"a_test.go"}, Violated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := outcome(tt.attempt, tt.ranAtAll, tt.verified, tt.runnable, tt.broke)
			if got != tt.want {
				t.Errorf("outcome = %q, want %q", got, tt.want)
			}
		})
	}
}

// A timeout is not a failing change: the harness never finished having an
// opinion, and scoring it as failed would flatter a harness that gives up fast.
func TestATimeoutOutranksVerification(t *testing.T) {
	if got := outcome(Attempt{TimedOut: true}, true, true, true, nil); got != TimedOut {
		t.Errorf("outcome = %q, want %q", got, TimedOut)
	}
}

func TestParseCraftReadsTheScores(t *testing.T) {
	got := parseCraft(`{"result":"{\"minimal\":4,\"tested\":2,\"focused\":5,\"reason\":\"tight fix, no tests\"}"}`)

	if !got.Scored {
		t.Fatal("should have been scored")
	}
	if got.Minimal != 4 || got.Tested != 2 || got.Focused != 5 {
		t.Errorf("scores = %d/%d/%d, want 4/2/5", got.Minimal, got.Tested, got.Focused)
	}
	if got.Reason == "" {
		t.Error("the reason was dropped")
	}
}

func TestParseCraftReadsABareReply(t *testing.T) {
	got := parseCraft(`{"minimal":3,"tested":3,"focused":3,"reason":"ordinary"}`)
	if !got.Scored || got.Minimal != 3 {
		t.Errorf("got %+v", got)
	}
}

// Craft is the soft half of the score. It must never take the run down with it.
func TestAnUnreadableJudgeLeavesCraftUnscored(t *testing.T) {
	for _, out := range []string{"", "I think it was fine, honestly", `{"something":"else"}`} {
		got := parseCraft(out)
		if got.Scored {
			t.Errorf("%q should not have produced a score", out)
		}
		if got.Reason == "" {
			t.Errorf("%q should say why it was not scored", out)
		}
	}
}

func TestCraftScoresAreClamped(t *testing.T) {
	got := parseCraft(`{"minimal":9,"tested":-3,"focused":5}`)
	if got.Minimal != 5 {
		t.Errorf("minimal = %d, want clamped to 5", got.Minimal)
	}
	if got.Tested != 1 {
		t.Errorf("tested = %d, want clamped to 1", got.Tested)
	}
}

func TestClipKeepsTheTop(t *testing.T) {
	long := strings.Repeat("x", 100)
	got := clip(long, 10)
	if !strings.HasPrefix(got, "xxxxxxxxxx") || !strings.Contains(got, "truncated") {
		t.Errorf("got %q", got)
	}
	if clip("short", 10) != "short" {
		t.Error("a short diff should be left alone")
	}
}
