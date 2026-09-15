package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/daemon/session"
	"github.com/corporealshift/nabu/daemon/tools"
	"github.com/corporealshift/nabu/protocol"
)

// harness wires a Manager over a temp dir and a fake provider.
type harness struct {
	m     *Manager
	fake  *provider.Fake
	store *session.Store
	dir   string
}

func newHarness(t *testing.T, mods []module.Module, script []provider.Response) *harness {
	t.Helper()
	dir := t.TempDir()
	store, err := session.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	fake := &provider.Fake{Script: script}
	pr := provider.NewRegistry()
	pr.Add(provider.Config{Name: "fake", ContextWindow: 8000}, fake, true)
	builtins := &tools.Builtins{}
	mr := module.NewRegistry(append([]module.Module{builtins}, mods...), module.Options{Log: testLogger()})
	m, err := New(Deps{Store: store, Providers: pr, Modules: mr, Builtins: builtins, Root: dir, Log: testLogger()},
		Config{DefaultModel: "fake/m"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Shutdown(context.Background()) })
	return &harness{m: m, fake: fake, store: store, dir: dir}
}

func (h *harness) create(t *testing.T) *session.Session {
	t.Helper()
	s, err := h.m.Create(context.Background(), h.dir, CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// ctxModule injects a prefix block at session start.
type ctxModule struct{ text string }

func (ctxModule) Name() string                          { return "ctxmod" }
func (ctxModule) Init(module.Host, module.Config) error { return nil }
func (c ctxModule) SessionStart(context.Context, module.Session) ([]module.ContextBlock, error) {
	return []module.ContextBlock{{Slot: "prefix", Content: c.text}}, nil
}

func TestCreateResolvesWorkspaceAndRunsSessionStart(t *testing.T) {
	h := newHarness(t, []module.Module{ctxModule{"# Skills"}}, nil)
	s := h.create(t)
	ev := s.Events()
	if len(ev) != 2 || ev[1].Type != protocol.EventContext {
		t.Fatalf("events: %+v", ev)
	}
	c := protocol.MustData[protocol.ContextData](ev[1])
	if c.Source != "module:ctxmod" || c.Slot != "prefix" || c.Content != "# Skills" {
		t.Fatalf("context: %+v", c)
	}
	sd := protocol.MustData[protocol.SessionData](ev[0])
	if sd.WorkspaceKey == "" || sd.Options.Model != "fake/m" || sd.Options.PermissionMode != protocol.PermissionAuto {
		t.Fatalf("session data: %+v", sd)
	}
}

func TestInvokeToolLogsCallAndResult(t *testing.T) {
	h := newHarness(t, nil, nil)
	s := h.create(t)
	handle, err := h.m.handle(s.ID())
	if err != nil {
		t.Fatal(err)
	}
	res := h.m.invokeTool(context.Background(), handle, protocol.ToolCallData{
		CallID: "c1", Tool: "write", Arguments: json.RawMessage(`{"path":"x.txt","content":"hi"}`), Source: "model"})
	if res.Status != "ok" || !strings.Contains(res.Content, "x.txt") {
		t.Fatalf("result: %+v", res)
	}
	ev := s.Events()
	if ev[len(ev)-2].Type != protocol.EventToolCall || ev[len(ev)-1].Type != protocol.EventToolResult {
		t.Fatalf("must log call then result: %+v", ev)
	}
}

func TestUnknownToolIsAnErrorResultNotACrash(t *testing.T) {
	h := newHarness(t, nil, nil)
	s := h.create(t)
	handle, _ := h.m.handle(s.ID())
	res := h.m.invokeTool(context.Background(), handle, protocol.ToolCallData{
		CallID: "c1", Tool: "nope", Arguments: json.RawMessage(`{}`), Source: "model"})
	if res.Status != "error" || !strings.Contains(res.Content, "unknown tool") {
		t.Fatalf("result: %+v", res)
	}
}

// denier denies every bash call.
type denier struct{}

func (denier) Name() string                          { return "denier" }
func (denier) Init(module.Host, module.Config) error { return nil }
func (denier) GateTool(_ context.Context, _ module.Session, c protocol.ToolCallData) module.Verdict {
	if c.Tool == "bash" {
		return module.Verdict{Decision: module.Deny, Reason: "no shell today"}
	}
	return module.Verdict{Decision: module.Allow}
}

func TestDeniedToolBecomesAnErrorResult(t *testing.T) {
	h := newHarness(t, []module.Module{denier{}}, nil)
	s := h.create(t)
	handle, _ := h.m.handle(s.ID())
	res := h.m.invokeTool(context.Background(), handle, protocol.ToolCallData{
		CallID: "c1", Tool: "bash", Arguments: json.RawMessage(`{"command":"ls"}`), Source: "model"})
	if res.Status != "error" || !strings.Contains(res.Content, "no shell today") {
		t.Fatalf("result: %+v", res)
	}
	ev := s.Events()
	if ev[len(ev)-1].Type != protocol.EventToolResult {
		t.Fatal("denied calls must still be logged")
	}
}

// fakeAsker approves or denies on demand.
type fakeAsker struct {
	approve  bool
	asked    int
	question string
}

func (a *fakeAsker) Permission(_ context.Context, _ string, _ protocol.ToolCallData, _, _ string) (bool, string) {
	a.asked++
	if a.approve {
		return true, ""
	}
	return false, "user said no"
}

func (a *fakeAsker) Ask(_ context.Context, _, q string, _ []string) (string, error) {
	a.question = q
	return "an answer", nil
}

// asksModule requests confirmation for every call.
type asksModule struct{}

func (asksModule) Name() string                          { return "asks" }
func (asksModule) Init(module.Host, module.Config) error { return nil }
func (asksModule) GateTool(context.Context, module.Session, protocol.ToolCallData) module.Verdict {
	return module.Verdict{Decision: module.Ask, Summary: "write a file", Risk: "medium"}
}

func TestAskGoesToTheAskerAndBypassSkipsIt(t *testing.T) {
	asker := &fakeAsker{approve: false}
	h := newHarness(t, []module.Module{asksModule{}}, nil)
	h.m.deps.Asker = asker
	s := h.create(t)
	handle, _ := h.m.handle(s.ID())
	call := protocol.ToolCallData{
		CallID: "c1", Tool: "write", Arguments: json.RawMessage(`{"path":"a.txt","content":"x"}`), Source: "model"}
	res := h.m.invokeTool(context.Background(), handle, call)
	if res.Status != "error" || !strings.Contains(res.Content, "user said no") || asker.asked != 1 {
		t.Fatalf("deny path: %+v asked=%d", res, asker.asked)
	}
	if _, err := h.m.SetOption(context.Background(), s.ID(), "permission_mode", "bypass"); err != nil {
		t.Fatal(err)
	}
	call.CallID = "c2"
	res = h.m.invokeTool(context.Background(), handle, call)
	if res.Status != "ok" || asker.asked != 1 {
		t.Fatalf("bypass path: %+v asked=%d", res, asker.asked)
	}
}

func TestModuleToolCallIsAttributedToTheModule(t *testing.T) {
	h := newHarness(t, nil, nil)
	s := h.create(t)
	handle, _ := h.m.handle(s.ID())
	host := newHost(h.m, "verify", nil)
	if _, err := host.Tools().Call(context.Background(), handle, "write",
		json.RawMessage(`{"path":"m.txt","content":"x"}`)); err != nil {
		t.Fatal(err)
	}
	ev := s.Events()
	call := protocol.MustData[protocol.ToolCallData](ev[len(ev)-2])
	if call.Source != "module:verify" {
		t.Fatalf("source: %q", call.Source)
	}
}

func TestUpdateTasksAppendsSnapshot(t *testing.T) {
	h := newHarness(t, nil, nil)
	s := h.create(t)
	handle, _ := h.m.handle(s.ID())
	d, err := h.m.UpdateTasks(context.Background(), handle, []protocol.Task{
		{Title: "One", Status: protocol.TaskPending, DoneWhen: "x"},
	}, "model")
	if err != nil || d.Revision != 1 || d.Tasks[0].ID != "t1" {
		t.Fatalf("first: %+v %v", d, err)
	}
	d, err = h.m.UpdateTasks(context.Background(), handle, []protocol.Task{
		{ID: "t1", Title: "One", Status: protocol.TaskDone},
	}, "client")
	if err != nil || d.Revision != 2 || d.Source != "client" {
		t.Fatalf("second: %+v %v", d, err)
	}
	if got := s.State().Tasks; len(got) != 1 || got[0].Status != protocol.TaskDone {
		t.Fatalf("projection: %+v", got)
	}
}

func TestGoalAndBudgetMethods(t *testing.T) {
	h := newHarness(t, nil, nil)
	s := h.create(t)
	if _, err := h.m.SetGoal(context.Background(), s.ID(), "tests pass"); err != nil {
		t.Fatal(err)
	}
	if g := s.State().Goal; g == nil || g.State != "set" || g.Source != "client" {
		t.Fatalf("goal: %+v", g)
	}
	if _, err := h.m.SetBudget(context.Background(), s.ID(), protocol.BudgetData{MaxTurns: 10, Source: "client"}); err != nil {
		t.Fatal(err)
	}
	if s.State().Budget.MaxTurns != 10 {
		t.Fatal("budget not recorded")
	}
	if _, err := h.m.ClearGoal(context.Background(), s.ID()); err != nil {
		t.Fatal(err)
	}
	if s.State().GoalActive() {
		t.Fatal("goal must be inactive after clear")
	}
	if _, err := h.m.ClearGoal(context.Background(), s.ID()); err == nil {
		t.Fatal("clearing twice must be an invalid transition")
	}
}

func TestPromptOnTerminalSessionIsRejected(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{{Content: "done"}})
	s := h.create(t)
	if _, err := h.m.Prompt(context.Background(), s.ID(), "hi"); err != nil {
		t.Fatal(err)
	}
	h.m.WaitIdle(s.ID())
	if err := h.m.Stop(context.Background(), s.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.Prompt(context.Background(), s.ID(), "again"); err == nil {
		t.Fatal("prompting a completed session must fail")
	}
	if st := s.State(); st.State != protocol.StateCompleted {
		t.Fatalf("state: %s", st.State)
	}
	r := lastReport(t, s.Events())
	if r.ExitStatus != protocol.StateCompleted || r.Checks == nil || r.Tasks.Open == nil {
		t.Fatalf("report: %+v", r)
	}
}

func TestCreateOptionsDefaults(t *testing.T) {
	h := newHarness(t, nil, nil)
	// Compaction is on unless explicitly disabled: a zero CreateOptions must
	// not silently turn it off.
	s := h.create(t)
	if opts := s.State().Options; !opts.CompactionEnabled {
		t.Fatalf("compaction must default on: %+v", opts)
	}
	off := false
	s2, err := h.m.Create(context.Background(), h.dir, CreateOptions{
		CompactionEnabled: &off, Model: "fake/other", PermissionMode: protocol.PermissionBypass})
	if err != nil {
		t.Fatal(err)
	}
	opts := s2.State().Options
	if opts.CompactionEnabled || opts.Model != "fake/other" || opts.PermissionMode != protocol.PermissionBypass {
		t.Fatalf("explicit options must be honoured: %+v", opts)
	}
}

// Spec 8: "bypass approves everything and appends a notice when set." The
// notice is what makes an unguarded session visible in the log afterwards.
func TestSettingBypassAppendsANotice(t *testing.T) {
	h := newHarness(t, nil, nil)
	s := h.create(t)

	if _, err := h.m.SetOption(context.Background(), s.ID(), "permission_mode", "bypass"); err != nil {
		t.Fatal(err)
	}

	var notice *protocol.NoticeData
	for _, ev := range s.Events() {
		if ev.Type != protocol.EventNotice {
			continue
		}
		var d protocol.NoticeData
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			t.Fatal(err)
		}
		notice = &d
	}
	if notice == nil {
		t.Fatal("setting bypass must append a notice")
	}
	if !strings.Contains(strings.ToLower(notice.Message), "bypass") {
		t.Errorf("the notice should name the mode, got %q", notice.Message)
	}
}

// Switching to a guarded mode is unremarkable and should not add a notice.
func TestSettingAskAppendsNoNotice(t *testing.T) {
	h := newHarness(t, nil, nil)
	s := h.create(t)
	before := countNotices(s.Events())

	if _, err := h.m.SetOption(context.Background(), s.ID(), "permission_mode", "ask"); err != nil {
		t.Fatal(err)
	}
	if got := countNotices(s.Events()); got != before {
		t.Errorf("notices: got %d, want %d", got, before)
	}
}

func countNotices(events []protocol.Event) int {
	n := 0
	for _, ev := range events {
		if ev.Type == protocol.EventNotice {
			n++
		}
	}
	return n
}

// Spec 8 amended 2026-09-14: a session with no explicit mode runs in auto.
// Guard's rules already stop the dangerous calls, so ask on top of that
// prompted for every read and every edit.
func TestDefaultPermissionModeIsAuto(t *testing.T) {
	h := newHarness(t, nil, nil)
	s := h.create(t)
	if got := s.State().Options.PermissionMode; got != protocol.PermissionAuto {
		t.Errorf("default permission mode: got %q, want %q", got, protocol.PermissionAuto)
	}
}

// A module's data directory is a top-level directory of the nabu root, named
// for the module. Nabu's data is organised by what it is, not hidden one level
// down under an implementation detail.
func TestDataDirIsTopLevel(t *testing.T) {
	h := newHarness(t, nil, nil)

	dir, err := h.m.dataDir("memory")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(h.dir, "memory"); dir != want {
		t.Errorf("dataDir = %q, want %q", dir, want)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("dataDir did not create it: %v", err)
	}
}

// The session store owns its directory, so a module may not be handed it.
func TestDataDirRefusesAReservedName(t *testing.T) {
	h := newHarness(t, nil, nil)

	if _, err := h.m.dataDir("sessions"); err == nil {
		t.Error("a module was handed the session store's directory")
	}
}

// lastReport finds the run report, which sits immediately before the terminal
// state change rather than at the end of the log.
func lastReport(t *testing.T, ev []protocol.Event) *protocol.ReportData {
	t.Helper()
	for i := len(ev) - 1; i >= 0; i-- {
		if ev[i].Type == protocol.EventReport {
			if i != len(ev)-1 && ev[len(ev)-1].Type != protocol.EventStateChange {
				t.Errorf("the report is not immediately before the final state change")
			}
			return protocol.MustData[protocol.ReportData](ev[i])
		}
	}
	t.Fatal("the session produced no report")
	return nil
}
