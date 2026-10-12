package runs

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/corporealshift/nabu/clients/goclient"
	"github.com/corporealshift/nabu/protocol"
)

var update = flag.Bool("update", false, "rewrite testdata/status from the builders")

var t0 = time.Date(2026, 10, 11, 15, 53, 44, 0, time.UTC)

// recordedRun is a run at its second task of three, one of a goal's, after a
// failed attempt at the first.
func recordedRun() Run {
	return Run{
		Home: "01M4P8EH2VXBN4WJZ8R8PJ1HKX", Slug: "in-flight-notes-1hkx", Goal: "01M498J1N9D785RFV6AZBRZN7G",
		Step: StepWork, Task: 1, Attempt: 1, Session: "01M4PFQCAXFW19C4PHD50DSQSM",
		Since: t0.Add(130 * time.Minute), Fixes: 1, Revisions: 1, PR: 41, PRURL: "https://github.com/o/r/pull/41",
		History: []Record{
			{Step: StepSetup, OK: true, At: t0.Add(time.Second)},
			{Step: StepPlan, Session: "01M4P8ENR4XCTQT7HMWDVSCWG8", OK: true, At: t0.Add(93 * time.Minute)},
			{Step: StepPlanReview, OK: true, At: t0.Add(94 * time.Minute)},
			{Step: StepVerify, Session: "01M4PDT3JY7SDRXF9N2DP7923Y", OK: true, At: t0.Add(125 * time.Minute)},
			{Step: StepVerifyReview, OK: true, Result: "asked for a revision of verify.sh", At: t0.Add(126 * time.Minute)},
			{Step: StepWork, Task: 0, Session: "01M4PFKEYY6GEREN9YBJ11GC6D", OK: false,
				Why: "the session ended paused " + strings.Repeat("and more ", 60), Note: "a long note", At: t0.Add(128 * time.Minute)},
			{Step: StepWork, Task: 0, Session: "01M4PFQCAXFW19C4PHD50DSQSM", OK: true, At: t0.Add(129 * time.Minute)},
			{Step: StepCheck, OK: true, Result: "failed", At: t0.Add(130 * time.Minute)},
		},
		Started: t0, Updated: t0.Add(130 * time.Minute),
	}
}

func recordedGoal() Goal {
	return Goal{
		Home: "01M498J1N9D785RFV6AZBRZN7G", Text: "build the app based on the designs in the design directory",
		Step: GoalRuns, Round: 2, RoundsFrom: 1, Since: t0,
		Briefs: []GoalBrief{
			{Title: "Mission Control", Brief: "# Mission Control\n\nThe long text.", Round: 1, State: BriefDone,
				Run: "01M4A", PRURL: "https://github.com/o/r/pull/30"},
			{Title: "Settings", Brief: "# Settings", Round: 1, State: BriefFailed, Run: "01M4B",
				FailedAt: StepWork, Why: "the fix cap was reached"},
			{Title: "In-Flight notes", Brief: "# In-Flight notes", Round: 2, State: BriefRunning, Run: "01M4P8EH2VXBN4WJZ8R8PJ1HKX"},
		},
		Verdicts: []Verdict{{Round: 1, Met: false, Reason: "Settings is not built.", At: t0.Add(-time.Hour)}},
		Started:  t0.Add(-5 * time.Hour), Updated: t0,
	}
}

const recordedBrief = "# In-Flight notes: data, state and actions (no UI)\n\nAdd the data side.\n"

func TestNewRunStatus(t *testing.T) {
	s := NewRunStatus(recordedRun(), recordedBrief, []string{"Notes in Room", "Notes in state", "Actions"}, "build the app")
	if s.Version != StatusVersion || s.Title != "In-Flight notes: data, state and actions (no UI)" ||
		s.Step != StepWork || s.Task != 2 || len(s.Tasks) != 3 || s.Attempt != 1 ||
		s.Session != "01M4PFQCAXFW19C4PHD50DSQSM" || s.GoalTitle != "build the app" ||
		s.PR.Number != 41 || s.Fixes != 1 || s.Revisions != 1 || s.Brief != recordedBrief {
		t.Fatalf("status = %+v", s)
	}
	if len(s.History) != 8 {
		t.Fatalf("history = %+v", s.History)
	}
	failed := s.History[5]
	if failed.Task != 1 || failed.OK || len(failed.Why) > statusText || !strings.HasSuffix(failed.Why, "…") {
		t.Errorf("failed work row = %+v", failed)
	}
	if s.History[1].Task != 0 || s.History[4].Result != "asked for a revision of verify.sh" {
		t.Errorf("rows = %+v", s.History)
	}
	b, _ := json.Marshal(s)
	if bytes.Contains(b, []byte("a long note")) {
		t.Error("a note reached the status")
	}
}

// A run saved before Since was kept began its current step when the last one
// ended.
func TestSinceFromTheHistoryWhenUnknown(t *testing.T) {
	r := recordedRun()
	r.Since = time.Time{}
	s := NewRunStatus(r, recordedBrief, nil, "")
	if want := r.History[len(r.History)-1].At; !s.Since.Equal(want) {
		t.Errorf("since %s, want %s", s.Since, want)
	}
	r.History = nil
	if s := NewRunStatus(r, recordedBrief, nil, ""); !s.Since.Equal(r.Started) {
		t.Errorf("with no history, since %s, want the start %s", s.Since, r.Started)
	}
}

func TestNewGoalStatus(t *testing.T) {
	s := NewGoalStatus(recordedGoal(), 10)
	if s.Round != 2 || s.Rounds != 10 || len(s.Briefs) != 3 || len(s.Verdicts) != 1 || s.Step != GoalRuns {
		t.Fatalf("status = %+v", s)
	}
	if s.Briefs[1].FailedAt != StepWork || s.Briefs[2].Run != "01M4P8EH2VXBN4WJZ8R8PJ1HKX" {
		t.Errorf("briefs = %+v", s.Briefs)
	}
	b, _ := json.Marshal(s)
	if bytes.Contains(b, []byte("The long text")) {
		t.Error("a brief's text reached the goal's status")
	}
}

// A run whose history passes the limit loses its oldest rows, and keeps the
// newest.
func TestARunStatusIsTrimmedToFit(t *testing.T) {
	r := recordedRun()
	r.History = nil
	for i := range maxHistory {
		r.History = append(r.History, Record{Step: StepCheck, OK: false, Why: strings.Repeat("w", statusText),
			Result: strings.Repeat("r", statusText), At: t0.Add(time.Duration(i) * time.Minute)})
	}
	full, _ := json.Marshal(NewRunStatus(r, recordedBrief, nil, ""))
	if len(full) <= protocol.MaxStatusBytes {
		t.Fatalf("the test's run is only %d bytes", len(full))
	}
	b, err := encodeRunStatus(NewRunStatus(r, recordedBrief, nil, ""))
	if err != nil || len(b) > protocol.MaxStatusBytes {
		t.Fatalf("encoded %d bytes, err %v", len(b), err)
	}
	var got RunStatus
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	last := got.History[len(got.History)-1]
	if len(got.History) >= maxHistory || !last.At.Equal(r.History[maxHistory-1].At) {
		t.Errorf("kept %d rows, the last at %s", len(got.History), last.At)
	}
}

func TestAGoalStatusLosesOldReasonsToFit(t *testing.T) {
	g := recordedGoal()
	g.Verdicts = nil
	for i := range 400 {
		g.Verdicts = append(g.Verdicts, Verdict{Round: i + 1, Reason: strings.Repeat("x", statusText)})
	}
	b, err := encodeGoalStatus(NewGoalStatus(g, 400))
	if err != nil || len(b) > protocol.MaxStatusBytes {
		t.Fatalf("encoded %d bytes, err %v", len(b), err)
	}
	var got GoalStatus
	_ = json.Unmarshal(b, &got)
	if got.Verdicts[0].Reason != "" || got.Verdicts[len(got.Verdicts)-1].Reason == "" {
		t.Errorf("the oldest reasons should go first")
	}
}

// statuses decodes the run statuses set on a home, oldest first.
func statuses(t *testing.T, h *fakeHome) []RunStatus {
	t.Helper()
	var out []RunStatus
	for _, st := range h.statuses {
		if st.Kind != protocol.StatusRun {
			t.Fatalf("kind %q", st.Kind)
		}
		var s RunStatus
		if err := json.Unmarshal(st.Data, &s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func TestARunSetsItsStatusAsItGoes(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false}
	g.ask("H1", "Add a Median function to stats")
	g.tick()
	r := g.run("H1")
	steps := []map[string]string{
		{r.File(PlanFile): "# Plan\n"}, {r.File(VerifyFile): "go test ./...\n"}, {r.File(TasksFile): twoTasks},
		{"stats.go": "package stats\n"}, {"stats_test.go": "package stats\n"},
	}
	for _, files := range steps {
		g.now = g.now.Add(10 * time.Minute)
		g.finish(files)
		g.tick()
	}
	if r.Step != StepDone {
		t.Fatalf("run ended at %q (%s)\nlog:\n%s", r.Step, r.Why, g.log.String())
	}

	all := statuses(t, g.d.homes["H1"])
	for i := 1; i < len(all); i++ {
		a, _ := json.Marshal(all[i-1])
		b, _ := json.Marshal(all[i])
		if bytes.Equal(a, b) {
			t.Errorf("status %d was set twice", i)
		}
	}
	last := all[len(all)-1]
	if last.Step != StepDone || last.Title != "Add a Median function to stats" || last.PR.URL == "" {
		t.Errorf("last status = %+v", last)
	}
	var seen []Step
	for _, row := range last.History {
		seen = append(seen, row.Step)
	}
	for _, want := range []Step{StepPlan, StepPlanReview, StepVerify, StepVerifyReview, StepWork, StepFinalReview} {
		if !slices.Contains(seen, want) {
			t.Errorf("history %v has no %s", seen, want)
		}
	}

	var second *RunStatus
	for i := range all {
		if all[i].Step == StepWork && all[i].Task == 2 && all[i].Session != "" {
			second = &all[i]
			break
		}
	}
	if second == nil || len(second.Tasks) != 2 || second.Tasks[1] != "Test Median" {
		t.Fatalf("no status for the second task's session: %+v", all)
	}
	// Since is when the task began, not when its session did.
	var first *RunStatus
	for i := range all {
		if all[i].Step == StepWork && all[i].Task == 2 {
			first = &all[i]
			break
		}
	}
	if !first.Since.Equal(second.Since) || second.Since.Before(r.Started) {
		t.Errorf("since moved within a task: %s then %s", first.Since, second.Since)
	}

	before := len(g.d.homes["H1"].statuses)
	g.tick()
	if after := len(g.d.homes["H1"].statuses); after != before {
		t.Errorf("a poll with nothing new set %d statuses", after-before)
	}
}

func TestSinceMovesWithTheStepAndTheTask(t *testing.T) {
	g := newRig(t)
	r := &Run{Home: "H1", Step: StepWork, Task: 0}
	g.rn.runs = map[string]*Run{"H1": r}
	_ = g.rn.save(r)
	at := r.Since
	g.now = g.now.Add(time.Minute)
	r.Attempt = 1
	_ = g.rn.save(r)
	if !r.Since.Equal(at) {
		t.Errorf("a retry moved since")
	}
	r.Task = 1
	_ = g.rn.save(r)
	if !r.Since.Equal(g.now) {
		t.Errorf("a new task did not move since")
	}
	g.now = g.now.Add(time.Minute)
	r.Step = StepCheck
	_ = g.rn.save(r)
	if !r.Since.Equal(g.now) {
		t.Errorf("a new step did not move since")
	}
}

func TestAStatusThatCannotBeSetLeavesTheRunMoving(t *testing.T) {
	g := newRig(t)
	g.shell.results = []bool{false}
	g.d.statusErr = errors.New("daemon says no")
	r := g.planned("H1")
	g.finish(map[string]string{"stats.go": "package stats\n"})
	g.tick()
	g.finish(map[string]string{"stats_test.go": "package stats\n"})
	g.tick()
	if r.Step != StepDone {
		t.Fatalf("run ended at %q (%s)", r.Step, r.Why)
	}
	if !strings.Contains(g.log.String(), "setting its status: daemon says no") {
		t.Errorf("log:\n%s", g.log.String())
	}
}

// A home that is gone, archived once its run was done, is asked once and then
// left alone, not asked again at every poll.
func TestAGoneHomeIsNotAskedAgain(t *testing.T) {
	g := newRig(t)
	// As the daemon's answer reaches the runner, through the client.
	g.d.statusErr = fmt.Errorf("set status: %w", &goclient.RPCError{Code: protocol.CodeSessionNotFound, Message: "01M3ZTN8"})
	g.d.homes["H1"] = &fakeHome{workspace: t.TempDir()}
	g.rn.runs = map[string]*Run{"H1": {Home: "H1", Step: StepDone, Started: g.now}}
	g.rn.goals = map[string]*Goal{}
	g.tick()
	g.tick()
	if g.d.statusCalls != 1 || strings.Contains(g.log.String(), "setting its status") {
		t.Fatalf("asked %d times; log:\n%s", g.d.statusCalls, g.log.String())
	}
}

func TestAGoalSetsItsStatus(t *testing.T) {
	g := newRig(t)
	g.askGoal("G1", "Add Median and Mode to stats")
	g.tick()
	goal := g.goal("G1")
	g.work(g.currentRun(goal))
	g.tick()
	g.work(g.currentRun(goal))
	g.now = g.now.Add(time.Hour)
	g.tick()
	if goal.Step != GoalDone {
		t.Fatalf("goal ended at %q (%s)", goal.Step, goal.Why)
	}
	sts := g.d.homes["G1"].statuses
	if len(sts) == 0 || sts[0].Kind != protocol.StatusGoal {
		t.Fatalf("statuses = %+v", sts)
	}
	var s GoalStatus
	if err := json.Unmarshal(sts[len(sts)-1].Data, &s); err != nil {
		t.Fatal(err)
	}
	if s.Step != GoalDone || len(s.Briefs) != 2 || len(s.Verdicts) != 1 || !s.Verdicts[0].At.Equal(g.now) ||
		s.Rounds != g.rn.Cfg.GoalRounds || s.Briefs[0].Run == "" {
		t.Errorf("goal status = %+v", s)
	}
}

// The fixture is what the Android parser's tests decode. It is written from
// the builders, so the two cannot drift: run with -update after changing a
// shape, and the app's tests then say whether it still reads it.
func TestTheStatusFixtureIsCurrent(t *testing.T) {
	for name, v := range map[string]any{
		"run.json":  NewRunStatus(recordedRun(), recordedBrief, []string{"Notes in Room", "Notes in state", "Actions"}, "build the app based on the designs in the design directory"),
		"goal.json": NewGoalStatus(recordedGoal(), 10),
	} {
		want, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, '\n')
		path := filepath.Join("testdata", "status", name)
		if *update {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, want, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v (run go test ./clients/runs -run Fixture -update)", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is out of date: run go test ./clients/runs -run Fixture -update", path)
		}
	}
}
