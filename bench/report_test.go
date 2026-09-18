package bench

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func results(runs ...Run) Results {
	r := Results{
		Started: "2026-09-16T23:00:00Z",
		Repeat:  1,
		Judge:   "claude/sonnet",
		Harness: []string{"nabu", "pi", "claude"},
		Runs:    runs,
	}
	r.Suspects = suspectTasks(r.Runs, "claude")
	r.Tiers = map[string]string{}
	for _, run := range runs {
		r.Tiers[run.Task] = "basic"
	}
	return r
}

// The reason tiers exist: a suite everyone passes cannot show movement, so
// the hard tier is reported on its own rather than averaged into the floor.
func TestTiersAreReportedSeparately(t *testing.T) {
	r := results(
		pass("easy", "pi", 20), pass("easy", "claude", 5),
		fail("tricky", "pi"), pass("tricky", "claude", 8),
	)
	r.Tiers = map[string]string{"easy": "basic", "tricky": "hard"}

	var buf bytes.Buffer
	Report(&buf, r)
	out := buf.String()

	if !strings.Contains(out, "PASS RATE basic") || !strings.Contains(out, "PASS RATE hard") {
		tf(t, out, "both tiers should have their own line")
	}
	// pi passed the floor and failed the discriminating task. That difference
	// is the whole point and has to survive into the report.
	if !strings.Contains(out, "0/1") {
		tf(t, out, "the hard tier should show pi failing")
	}
}

func tf(t *testing.T, out, msg string) {
	t.Helper()
	t.Errorf("%s:\n%s", msg, out)
}

func pass(task, harness string, secs int) Run {
	return Run{Task: task, Harness: harness, Outcome: Passed,
		Cost: Cost{Duration: time.Duration(secs) * time.Second}}
}

func fail(task, harness string) Run {
	return Run{Task: task, Harness: harness, Outcome: Failed}
}

func TestReportShowsEveryHarnessPerTask(t *testing.T) {
	var buf bytes.Buffer
	Report(&buf, results(
		pass("alpha", "nabu", 30), pass("alpha", "pi", 20), pass("alpha", "claude", 5),
		fail("beta", "nabu"), pass("beta", "pi", 25), pass("beta", "claude", 6),
	))
	out := buf.String()

	for _, want := range []string{"alpha", "beta", "nabu", "pi", "claude", "PASS RATE"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report never mentions %q:\n%s", want, out)
		}
	}
}

// The headline must not count tasks the reference could not do.
func TestSuspectTasksAreExcludedFromTheRate(t *testing.T) {
	var buf bytes.Buffer
	Report(&buf, results(
		pass("good", "nabu", 10), pass("good", "claude", 5),
		fail("broken", "nabu"), fail("broken", "claude"),
	))
	out := buf.String()

	if !strings.Contains(out, "suspect") {
		t.Errorf("a task the reference failed should be called out:\n%s", out)
	}
	// nabu did one real task out of one; the broken one is not counted.
	if !strings.Contains(out, "1/1") {
		t.Errorf("the pass rate should exclude the suspect task:\n%s", out)
	}
}

func TestSelfJudgedCraftIsLabelled(t *testing.T) {
	run := pass("alpha", "claude", 5)
	run.Craft = Craft{Scored: true, Minimal: 4, Tested: 4, Focused: 4, SelfJudged: true}
	other := pass("alpha", "nabu", 30)
	other.Craft = Craft{Scored: true, Minimal: 3, Tested: 3, Focused: 3}

	var buf bytes.Buffer
	Report(&buf, results(run, other))

	if !strings.Contains(buf.String(), "(self)") {
		t.Errorf("claude judging claude should be labelled:\n%s", buf.String())
	}
}

// Anything that is not a plain pass or fail is usually a setup fault, and
// should not be silently averaged into a score.
func TestProblemsAreNamed(t *testing.T) {
	var buf bytes.Buffer
	Report(&buf, results(
		Run{Task: "alpha", Harness: "pi", Outcome: TimedOut},
		Run{Task: "beta", Harness: "nabu", Outcome: Violated, Broke: []string{"a_test.go"}},
		Run{Task: "alpha", Harness: "claude", Outcome: Passed},
		Run{Task: "beta", Harness: "claude", Outcome: Passed},
	))
	out := buf.String()

	if !strings.Contains(out, "timed_out") || !strings.Contains(out, "a_test.go") {
		t.Errorf("problems should be named:\n%s", out)
	}
}

func TestCompareShowsMovement(t *testing.T) {
	old := results(fail("alpha", "nabu"), pass("alpha", "claude", 5))
	now := results(pass("alpha", "nabu", 30), pass("alpha", "claude", 5))

	var buf bytes.Buffer
	Compare(&buf, old, now)
	out := buf.String()

	if !strings.Contains(out, "nabu · alpha") {
		t.Errorf("the moved row is missing:\n%s", out)
	}
	if !strings.Contains(out, "better") {
		t.Errorf("improvement should be called out:\n%s", out)
	}
}

func TestCompareSaysWhenNothingMoved(t *testing.T) {
	same := results(pass("alpha", "nabu", 30), pass("alpha", "claude", 5))

	var buf bytes.Buffer
	Compare(&buf, same, same)

	if !strings.Contains(buf.String(), "nothing moved") {
		t.Errorf("got:\n%s", buf.String())
	}
}

// Comparing across a model change compares stacks, not harnesses.
func TestCompareWarnsWhenTheModelsChanged(t *testing.T) {
	old := results(pass("alpha", "nabu", 30))
	old.Models = Models{Local: "qwen3.6-35b", Claude: "sonnet"}
	now := results(pass("alpha", "nabu", 30))
	now.Models = Models{Local: "qwen4-70b", Claude: "sonnet"}

	var buf bytes.Buffer
	Compare(&buf, old, now)

	if !strings.Contains(buf.String(), "models differ") {
		t.Errorf("a model change should be flagged:\n%s", buf.String())
	}
}

func TestTallyAggregatesRepeats(t *testing.T) {
	r := results(
		pass("alpha", "nabu", 10), fail("alpha", "nabu"), pass("alpha", "nabu", 30),
		pass("alpha", "claude", 5),
	)

	var nabu Tally
	for _, t := range r.Tally() {
		if t.Harness == "nabu" {
			nabu = t
		}
	}

	if nabu.Attempts != 3 || nabu.Passed != 2 {
		t.Errorf("tally = %d/%d, want 2/3", nabu.Passed, nabu.Attempts)
	}
	if nabu.Median != 10*time.Second {
		t.Errorf("median = %v, want the middle of 0s, 10s, 30s", nabu.Median)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := results(pass("alpha", "nabu", 30))

	path, err := want.Save(dir)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadResults(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(got.Runs) != 1 || got.Runs[0].Task != "alpha" {
		t.Errorf("round trip lost the runs: %+v", got.Runs)
	}
	if got.Judge != want.Judge {
		t.Errorf("judge = %q, want %q", got.Judge, want.Judge)
	}
	// A duration that changes unit on the way to disk makes every saved
	// comparison wrong by a factor of a million.
	if got.Runs[0].Cost.Duration != 30*time.Second {
		t.Errorf("duration survived as %v, want 30s", got.Runs[0].Cost.Duration)
	}
}
