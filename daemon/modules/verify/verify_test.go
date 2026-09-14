package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// fakeSession is the slice of module.Session these hooks use, plus a record of
// what was appended so tests can assert on check and goal events.
type fakeSession struct {
	workspace string
	state     protocol.State
	events    []protocol.Event
	appended  []protocol.Event
	appendErr error
}

func (f *fakeSession) ID() string                  { return "01ARZ3NDEKTSV4RRFFQ69G5FAV" }
func (f *fakeSession) Workspace() module.Workspace { return module.Workspace{Path: f.workspace} }
func (f *fakeSession) State() protocol.State       { return f.state }

func (f *fakeSession) Events(*string) ([]protocol.Event, error) {
	return f.events, nil
}

func (f *fakeSession) Append(t protocol.EventType, data any) (protocol.Event, error) {
	if f.appendErr != nil {
		return protocol.Event{}, f.appendErr
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return protocol.Event{}, err
	}
	ev := protocol.Event{ID: fmt.Sprintf("e%d", len(f.appended)+1), Type: t, Data: raw}
	f.appended = append(f.appended, ev)
	f.events = append(f.events, ev)
	return ev, nil
}

// checkEvent returns the nth appended check event.
func (f *fakeSession) checkEvents() []protocol.CheckData {
	var out []protocol.CheckData
	for _, e := range f.appended {
		if e.Type != protocol.EventCheck {
			continue
		}
		var d protocol.CheckData
		if json.Unmarshal(e.Data, &d) == nil {
			out = append(out, d)
		}
	}
	return out
}

func (f *fakeSession) goalEvents() []protocol.GoalData {
	var out []protocol.GoalData
	for _, e := range f.appended {
		if e.Type != protocol.EventGoal {
			continue
		}
		var d protocol.GoalData
		if json.Unmarshal(e.Data, &d) == nil {
			out = append(out, d)
		}
	}
	return out
}

// testHost supplies a scripted model for the judge and nothing else.
type testHost struct{ model module.Model }

func (h testHost) Model() module.Model            { return h.model }
func (h testHost) Tools() module.ToolCaller       { return nil }
func (h testHost) UI() module.UI                  { return nil }
func (h testHost) DataDir(string) (string, error) { return "", nil }
func (h testHost) Log() *slog.Logger              { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// scriptedModel returns a canned answer, or an error.
type scriptedModel struct {
	content string
	err     error
	calls   int
}

func (m *scriptedModel) Complete(context.Context, module.Session, module.CompletionRequest) (module.CompletionResponse, error) {
	m.calls++
	if m.err != nil {
		return module.CompletionResponse{}, m.err
	}
	return module.CompletionResponse{Content: m.content}, nil
}

func taskCall(tasks ...protocol.Task) protocol.ToolCallData {
	raw, err := json.Marshal(map[string]any{"tasks": tasks})
	if err != nil {
		panic(err)
	}
	return protocol.ToolCallData{CallID: "c1", Tool: "task.update", Arguments: raw, Source: "model"}
}

func newVerify(t *testing.T, cfg module.Config) *Module {
	t.Helper()
	m := &Module{}
	if err := m.Init(nil, cfg); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNameAndDefaults(t *testing.T) {
	m := newVerify(t, module.Config{})
	if got := m.Name(); got != "verify" {
		t.Errorf("Name: got %q, want %q", got, "verify")
	}
	if !m.RequireCleanTree {
		t.Error("require_clean_tree should default on")
	}
	if m.CommandTimeout != DefaultCommandTimeout {
		t.Errorf("command timeout: got %s, want %s", m.CommandTimeout, DefaultCommandTimeout)
	}
	if m.JudgeTurns != DefaultJudgeTurns {
		t.Errorf("judge turns: got %d, want %d", m.JudgeTurns, DefaultJudgeTurns)
	}
}

// Spec 10.2: the rule is on whenever a goal is set, and off for casual
// interactive use unless configured on.
func TestRequireDoneWhenFollowsTheGoal(t *testing.T) {
	m := newVerify(t, module.Config{})
	newTask := protocol.Task{ID: "t1", Title: "do a thing", Status: protocol.TaskPending}

	noGoal := &fakeSession{workspace: t.TempDir()}
	if v := m.GateTool(context.Background(), noGoal, taskCall(newTask)); v.Decision != module.Allow {
		t.Errorf("without a goal the rule is off, got %v", v.Decision)
	}

	withGoal := &fakeSession{workspace: t.TempDir(), state: protocol.State{
		Goal: &protocol.GoalData{Condition: "the gate is green", State: "set"},
	}}
	v := m.GateTool(context.Background(), withGoal, taskCall(newTask))
	if v.Decision != module.Deny {
		t.Fatalf("with a goal set, a task without done_when must be denied, got %v", v.Decision)
	}
	if !strings.Contains(v.Reason, "done_when") {
		t.Errorf("the reason should name done_when, got %q", v.Reason)
	}
}

func TestRequireDoneWhenCanBeForcedOnAndOff(t *testing.T) {
	newTask := protocol.Task{ID: "t1", Title: "x", Status: protocol.TaskPending}
	plain := &fakeSession{workspace: t.TempDir()}

	on := newVerify(t, module.Config{"require_done_when": true})
	if v := on.GateTool(context.Background(), plain, taskCall(newTask)); v.Decision != module.Deny {
		t.Errorf("forced on: got %v, want Deny", v.Decision)
	}

	goalSet := &fakeSession{workspace: t.TempDir(), state: protocol.State{
		Goal: &protocol.GoalData{Condition: "c", State: "set"},
	}}
	off := newVerify(t, module.Config{"require_done_when": false})
	if v := off.GateTool(context.Background(), goalSet, taskCall(newTask)); v.Decision != module.Allow {
		t.Errorf("forced off: got %v, want Allow", v.Decision)
	}
}

// A task that already exists is not re-judged: the rule is about introducing
// work without a way to prove it finished.
func TestRequireDoneWhenOnlyAppliesToNewTasks(t *testing.T) {
	m := newVerify(t, module.Config{"require_done_when": true})
	existing := protocol.Task{ID: "t1", Title: "x", Status: protocol.TaskPending}
	s := &fakeSession{workspace: t.TempDir(), state: protocol.State{Tasks: []protocol.Task{existing}}}

	moved := existing
	moved.Status = protocol.TaskInProgress
	if v := m.GateTool(context.Background(), s, taskCall(moved)); v.Decision != module.Allow {
		t.Errorf("an existing task should pass, got %v (%s)", v.Decision, v.Reason)
	}
}

func TestGateIgnoresOtherTools(t *testing.T) {
	m := newVerify(t, module.Config{"require_done_when": true})
	s := &fakeSession{workspace: t.TempDir()}
	call := protocol.ToolCallData{CallID: "c1", Tool: "bash", Arguments: json.RawMessage(`{"command":"ls"}`)}
	if v := m.GateTool(context.Background(), s, call); v.Decision != module.Allow {
		t.Errorf("verify should only speak about task.update, got %v", v.Decision)
	}
}

// Spec 10.2: a task carrying a check may only reach done if the command passes.
func TestMechanicalCheckGatesDone(t *testing.T) {
	m := newVerify(t, module.Config{})
	ws := t.TempDir()

	pass := protocol.Task{ID: "t1", Title: "x", Status: protocol.TaskDone,
		DoneWhen: "it works", Check: "exit 0"}
	s := &fakeSession{workspace: ws}
	if v := m.GateTool(context.Background(), s, taskCall(pass)); v.Decision != module.Allow {
		t.Fatalf("a passing check should allow, got %v (%s)", v.Decision, v.Reason)
	}
	checks := s.checkEvents()
	if len(checks) != 1 || checks[0].Status != "pass" {
		t.Fatalf("a passing check should be recorded, got %+v", checks)
	}
	if checks[0].TaskID != "t1" {
		t.Errorf("the check should name its task, got %q", checks[0].TaskID)
	}

	fail := protocol.Task{ID: "t2", Title: "y", Status: protocol.TaskDone,
		DoneWhen: "it works", Check: "exit 7"}
	s2 := &fakeSession{workspace: ws}
	v := m.GateTool(context.Background(), s2, taskCall(fail))
	if v.Decision != module.Deny {
		t.Fatalf("a failing check must deny, got %v", v.Decision)
	}
	if !strings.Contains(v.Reason, "t2") {
		t.Errorf("the reason should name the task, got %q", v.Reason)
	}
	failed := s2.checkEvents()
	if len(failed) != 1 || failed[0].Status != "fail" {
		t.Fatalf("a failing check should be recorded, got %+v", failed)
	}
}

// A task already done is not a transition, so its command does not run again.
func TestMechanicalCheckSkipsATaskAlreadyDone(t *testing.T) {
	m := newVerify(t, module.Config{})
	done := protocol.Task{ID: "t1", Title: "x", Status: protocol.TaskDone,
		DoneWhen: "w", Check: "exit 7"}
	s := &fakeSession{workspace: t.TempDir(), state: protocol.State{Tasks: []protocol.Task{done}}}

	if v := m.GateTool(context.Background(), s, taskCall(done)); v.Decision != module.Allow {
		t.Errorf("a task already done should not re-run its check, got %v", v.Decision)
	}
	if len(s.checkEvents()) != 0 {
		t.Error("no check event should be appended when nothing transitioned")
	}
}

func TestCommandTimeoutIsAFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("bash"); err != nil {
			t.Skip("no POSIX shell to sleep with")
		}
	}
	m := newVerify(t, module.Config{"command_timeout": 1})
	task := protocol.Task{ID: "t1", Title: "x", Status: protocol.TaskDone,
		DoneWhen: "w", Check: "sleep 20"}
	s := &fakeSession{workspace: t.TempDir()}

	start := time.Now()
	v := m.GateTool(context.Background(), s, taskCall(task))
	if v.Decision != module.Deny {
		t.Fatalf("a timeout must deny, got %v", v.Decision)
	}
	if !strings.Contains(v.Reason, "timed out") {
		t.Errorf("the reason should say it timed out, got %q", v.Reason)
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Errorf("the timeout was not honoured: %s", elapsed)
	}
}

// ---------------------------------------------------------------------------
// Stop gate

func TestOpenTasksVeto(t *testing.T) {
	m := newVerify(t, module.Config{})
	s := &fakeSession{workspace: t.TempDir()}
	info := module.StopInfo{Tasks: []protocol.Task{
		{ID: "t1", Title: "finished", Status: protocol.TaskDone},
		{ID: "t2", Title: "still going", Status: protocol.TaskInProgress},
	}}
	v := m.BeforeStop(context.Background(), s, info)
	if v.Allow {
		t.Fatal("an in-progress task must veto a stop")
	}
	if !strings.Contains(v.Reason, "t2") {
		t.Errorf("the veto should name the task, got %q", v.Reason)
	}
	if strings.Contains(v.Reason, "t1") {
		t.Error("a finished task should not be listed as outstanding")
	}
}

// Spec 10.2: blocked tasks must carry a note; they are reported, not vetoed.
func TestBlockedTaskNeedsANote(t *testing.T) {
	m := newVerify(t, module.Config{})
	s := &fakeSession{workspace: t.TempDir()}

	documented := module.StopInfo{Tasks: []protocol.Task{
		{ID: "t1", Title: "waiting", Status: protocol.TaskBlocked, Note: "needs the API key"},
	}}
	if v := m.BeforeStop(context.Background(), s, documented); !v.Allow {
		t.Errorf("a documented block should not veto, got %q", v.Reason)
	}

	bare := module.StopInfo{Tasks: []protocol.Task{
		{ID: "t1", Title: "waiting", Status: protocol.TaskBlocked},
	}}
	v := m.BeforeStop(context.Background(), s, bare)
	if v.Allow {
		t.Fatal("a blocked task with no note must veto")
	}
	if !strings.Contains(v.Reason, "no note") {
		t.Errorf("the veto should say the note is missing, got %q", v.Reason)
	}
}

func TestFailedCheckVetoes(t *testing.T) {
	m := newVerify(t, module.Config{})
	s := &fakeSession{workspace: t.TempDir()}
	_, _ = s.Append(protocol.EventCheck, protocol.CheckData{
		Name: "task:t1", Kind: "command", Status: "fail", Summary: "exit 1"})

	v := m.BeforeStop(context.Background(), s, module.StopInfo{})
	if v.Allow {
		t.Fatal("a failing check must veto")
	}
	if !strings.Contains(v.Reason, "task:t1") {
		t.Errorf("the veto should name the check, got %q", v.Reason)
	}
}

// A later pass clears an earlier failure: the check is a current fact.
func TestAPassingRerunClearsAFailure(t *testing.T) {
	m := newVerify(t, module.Config{})
	s := &fakeSession{workspace: t.TempDir()}
	_, _ = s.Append(protocol.EventCheck, protocol.CheckData{Name: "task:t1", Status: "fail", Summary: "exit 1"})
	_, _ = s.Append(protocol.EventCheck, protocol.CheckData{Name: "task:t1", Status: "pass", Summary: "exit 0"})

	if v := m.BeforeStop(context.Background(), s, module.StopInfo{}); !v.Allow {
		t.Errorf("a later pass should clear the failure, got %q", v.Reason)
	}
}

func TestWorkspaceGateVetoesOnFailure(t *testing.T) {
	ws := t.TempDir()

	failing := newVerify(t, module.Config{"command": "exit 3", "require_clean_tree": false})
	s := &fakeSession{workspace: ws}
	v := failing.BeforeStop(context.Background(), s, module.StopInfo{})
	if v.Allow {
		t.Fatal("a failing gate must veto")
	}
	if !strings.Contains(v.Reason, "gate failed") {
		t.Errorf("the veto should say the gate failed, got %q", v.Reason)
	}
	checks := s.checkEvents()
	if len(checks) != 1 || checks[0].Name != "verify.command" || checks[0].Status != "fail" {
		t.Fatalf("the gate result should be recorded, got %+v", checks)
	}

	passing := newVerify(t, module.Config{"command": "exit 0", "require_clean_tree": false})
	s2 := &fakeSession{workspace: ws}
	if v := passing.BeforeStop(context.Background(), s2, module.StopInfo{}); !v.Allow {
		t.Errorf("a passing gate should allow, got %q", v.Reason)
	}
	if got := s2.checkEvents(); len(got) != 1 || got[0].Status != "pass" {
		t.Errorf("a passing gate should be recorded, got %+v", got)
	}
}

// This is the failure the whole module exists for: tests pass, fix uncommitted.
func TestDirtyTreeVetoes(t *testing.T) {
	ws := gitRepo(t)
	if err := os.WriteFile(filepath.Join(ws, "new.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newVerify(t, module.Config{})
	s := &fakeSession{workspace: ws}
	v := m.BeforeStop(context.Background(), s, module.StopInfo{})
	if v.Allow {
		t.Fatal("a dirty tree must veto")
	}
	if !strings.Contains(v.Reason, "uncommitted") {
		t.Errorf("the veto should mention uncommitted changes, got %q", v.Reason)
	}

	off := newVerify(t, module.Config{"require_clean_tree": false})
	if v := off.BeforeStop(context.Background(), s, module.StopInfo{}); !v.Allow {
		t.Errorf("with the check off a dirty tree should allow, got %q", v.Reason)
	}
}

func TestCleanTreeAllows(t *testing.T) {
	ws := gitRepo(t)
	m := newVerify(t, module.Config{})
	s := &fakeSession{workspace: ws}
	if v := m.BeforeStop(context.Background(), s, module.StopInfo{}); !v.Allow {
		t.Errorf("a clean tree should allow, got %q", v.Reason)
	}
}

// A directory that is not a repository cannot be dirty, and that is not an error.
func TestNonRepoTreeAllows(t *testing.T) {
	m := newVerify(t, module.Config{})
	s := &fakeSession{workspace: t.TempDir()}
	if v := m.BeforeStop(context.Background(), s, module.StopInfo{}); !v.Allow {
		t.Errorf("a non-repository should not veto, got %q", v.Reason)
	}
}

// ---------------------------------------------------------------------------
// The judge

func judgeModule(t *testing.T, content string, err error) (*Module, *scriptedModel) {
	t.Helper()
	sm := &scriptedModel{content: content, err: err}
	m := newVerify(t, module.Config{"require_clean_tree": false})
	m.host = testHost{model: sm}
	return m, sm
}

func goalInfo() module.StopInfo {
	return module.StopInfo{Goal: &protocol.GoalData{Condition: "the gate is green", State: "set"}}
}

func goalSession(t *testing.T) *fakeSession {
	t.Helper()
	return &fakeSession{workspace: t.TempDir(), state: protocol.State{
		Goal: &protocol.GoalData{Condition: "the gate is green", State: "set"},
	}}
}

func TestJudgeMetAllows(t *testing.T) {
	m, sm := judgeModule(t, "VERDICT: met", nil)
	s := goalSession(t)

	if v := m.BeforeStop(context.Background(), s, goalInfo()); !v.Allow {
		t.Fatalf("a met goal should allow, got %q", v.Reason)
	}
	if sm.calls != 1 {
		t.Errorf("judge calls: got %d, want 1", sm.calls)
	}
	goals := s.goalEvents()
	if len(goals) != 1 || goals[0].State != "met" {
		t.Fatalf("a met goal should be recorded, got %+v", goals)
	}
	if goals[0].Source != "module:verify" {
		t.Errorf("source: got %q", goals[0].Source)
	}
}

func TestJudgeUnmetVetoes(t *testing.T) {
	m, _ := judgeModule(t, "VERDICT: unmet - the tests do not run", nil)
	s := goalSession(t)

	v := m.BeforeStop(context.Background(), s, goalInfo())
	if v.Allow {
		t.Fatal("an unmet goal must veto")
	}
	if !strings.Contains(v.Reason, "tests do not run") {
		t.Errorf("the veto should carry the judge's reason, got %q", v.Reason)
	}
	if len(s.goalEvents()) != 0 {
		t.Error("an unmet goal should not append a goal event; it vetoes instead")
	}
}

func TestJudgeImpossibleAllows(t *testing.T) {
	m, _ := judgeModule(t, "VERDICT: impossible - the API does not exist", nil)
	s := goalSession(t)

	if v := m.BeforeStop(context.Background(), s, goalInfo()); !v.Allow {
		t.Fatalf("an impossible goal should allow a stop, got %q", v.Reason)
	}
	goals := s.goalEvents()
	if len(goals) != 1 || goals[0].State != "impossible" {
		t.Fatalf("goal events: %+v", goals)
	}
	if !strings.Contains(goals[0].Reason, "API does not exist") {
		t.Errorf("the reason should be kept, got %q", goals[0].Reason)
	}
}

// A judge that fails open would make this whole mechanism theatre.
func TestJudgeFailureVetoes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		err     error
		want    string
	}{
		{"call fails", "", fmt.Errorf("connection refused"), "judge call failed"},
		{"unparseable answer", "I think it looks good to me!", nil, "required form"},
		{"empty answer", "", nil, "required form"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := judgeModule(t, tc.content, tc.err)
			s := goalSession(t)
			v := m.BeforeStop(context.Background(), s, goalInfo())
			if v.Allow {
				t.Fatal("a judge that cannot answer must veto, never allow")
			}
			if !strings.Contains(v.Reason, tc.want) {
				t.Errorf("reason %q should mention %q", v.Reason, tc.want)
			}
		})
	}
}

// With no model there is no judge, and a stop that was never judged must not
// be allowed on the strength of a goal nobody checked.
func TestNoModelVetoesAGoal(t *testing.T) {
	m := newVerify(t, module.Config{"require_clean_tree": false})
	s := goalSession(t)
	v := m.BeforeStop(context.Background(), s, goalInfo())
	if v.Allow {
		t.Fatal("without a model the goal cannot be judged, so a stop must veto")
	}
}

// Deterministic checks are free; the judge is a model call. Spec 10.3 requires
// the cheap ones first.
func TestJudgeIsNotCalledWhenADeterministicCheckFails(t *testing.T) {
	m, sm := judgeModule(t, "VERDICT: met", nil)
	s := goalSession(t)
	info := goalInfo()
	info.Tasks = []protocol.Task{{ID: "t1", Title: "open", Status: protocol.TaskPending}}

	if v := m.BeforeStop(context.Background(), s, info); v.Allow {
		t.Fatal("an open task must veto")
	}
	if sm.calls != 0 {
		t.Errorf("the judge should not be paid for when a free check already failed, calls=%d", sm.calls)
	}
}

func TestNoGoalMeansNoJudge(t *testing.T) {
	m, sm := judgeModule(t, "VERDICT: met", nil)
	s := &fakeSession{workspace: t.TempDir()}
	if v := m.BeforeStop(context.Background(), s, module.StopInfo{}); !v.Allow {
		t.Fatalf("with no goal the stop should be allowed, got %q", v.Reason)
	}
	if sm.calls != 0 {
		t.Errorf("no goal means no judge call, got %d", sm.calls)
	}
}

func TestParseVerdict(t *testing.T) {
	for _, tc := range []struct {
		in         string
		wantVerd   string
		wantReason string
		ok         bool
	}{
		{"VERDICT: met", "met", "", true},
		{"VERDICT: met\n", "met", "", true},
		{"verdict: met", "met", "", true},
		{"VERDICT: unmet - tests fail", "unmet", "tests fail", true},
		{"VERDICT: unmet — tests fail", "unmet", "tests fail", true},
		{"VERDICT: impossible - no such API", "impossible", "no such API", true},
		{"Some preamble.\nVERDICT: met\ntrailing", "met", "", true},
		{"VERDICT: unmet", "unmet", "", true},
		{"looks good", "", "", false},
		{"", "", "", false},
	} {
		v, reason, ok := parseVerdict(tc.in)
		if ok != tc.ok {
			t.Errorf("parseVerdict(%q): ok = %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if v != tc.wantVerd {
			t.Errorf("parseVerdict(%q): verdict = %q, want %q", tc.in, v, tc.wantVerd)
		}
		if reason != tc.wantReason {
			t.Errorf("parseVerdict(%q): reason = %q, want %q", tc.in, reason, tc.wantReason)
		}
	}
}

// "met" is a substring of "unmet": an unmet verdict must never read as met.
func TestUnmetIsNotReadAsMet(t *testing.T) {
	v, _, ok := parseVerdict("VERDICT: unmet - nope")
	if !ok || v != "unmet" {
		t.Fatalf("got %q ok=%v, want unmet", v, ok)
	}
}

// ---------------------------------------------------------------------------
// Reporter

func TestReportCarriesChecksAndTreeState(t *testing.T) {
	ws := gitRepo(t)
	m := newVerify(t, module.Config{})
	s := &fakeSession{workspace: ws}
	_, _ = s.Append(protocol.EventCheck, protocol.CheckData{
		Name: "verify.command", Status: "pass", Summary: "exit 0"})

	fields, err := m.Report(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields.Checks) != 1 || fields.Checks[0].Name != "verify.command" {
		t.Fatalf("checks: %+v", fields.Checks)
	}
	if fields.TreeDirty == nil {
		t.Fatal("verify owns tree_dirty and should report it")
	}
	if *fields.TreeDirty {
		t.Error("a fresh repository should be clean")
	}
}

func TestReportInANonRepoOmitsTreeState(t *testing.T) {
	m := newVerify(t, module.Config{})
	fields, err := m.Report(context.Background(), &fakeSession{workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if fields.TreeDirty != nil {
		t.Error("a non-repository cannot answer the question, so the field should be absent")
	}
}

func TestImplementsItsHooks(t *testing.T) {
	var m any = &Module{}
	for name, ok := range map[string]bool{
		"Module":   func() bool { _, ok := m.(module.Module); return ok }(),
		"ToolGate": func() bool { _, ok := m.(module.ToolGate); return ok }(),
		"StopGate": func() bool { _, ok := m.(module.StopGate); return ok }(),
		"Reporter": func() bool { _, ok := m.(module.Reporter); return ok }(),
	} {
		if !ok {
			t.Errorf("verify does not implement %s", name)
		}
	}
}

func TestConfigErrors(t *testing.T) {
	m := &Module{}
	err := m.Init(nil, module.Config{"require_done_when": "yes"})
	if err == nil || !strings.Contains(err.Error(), "must be a boolean") {
		t.Fatalf("want a boolean error, got %v", err)
	}
}

// gitRepo makes a temp repository with one commit, skipping if git is absent.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "README.md"},
		{"commit", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}
