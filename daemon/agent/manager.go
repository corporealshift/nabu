package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/daemon/session"
	"github.com/corporealshift/nabu/daemon/tools"
	"github.com/corporealshift/nabu/daemon/workspace"
	"github.com/corporealshift/nabu/protocol"
)

// Asker answers questions that need a human: permission for a gated tool call
// and module ui.ask. P1b supplies the WebSocket implementation; a nil Asker
// denies permission and errors on ask.
type Asker interface {
	Permission(ctx context.Context, sessionID string, call protocol.ToolCallData, summary, risk string) (approved bool, reason string)
	Ask(ctx context.Context, sessionID, question string, choices []string) (string, error)
}

// DeltaSink receives streaming text. Deltas are ephemeral: never logged,
// never replayed (protocol spec §7.6).
type DeltaSink func(sessionID, turnID, text string)

// Deps are the collaborators a Manager needs.
type Deps struct {
	Store     *session.Store
	Providers *provider.Registry
	Modules   *module.Registry
	Builtins  *tools.Builtins
	Root      string // ~/.nabu
	Log       *slog.Logger
	Asker     Asker
	Deltas    DeltaSink
}

// Manager owns every live session: it creates them, runs their loops, and is
// the one implementation of the module Host facilities.
type Manager struct {
	deps Deps
	cfg  Config
	log  *slog.Logger

	toolsByName map[string]module.Tool
	toolSpecs   []provider.ToolSpec

	mu     sync.Mutex
	rt     map[string]*runState
	closed bool
	wg     sync.WaitGroup
}

// runState is a session's in-memory runtime.
type runState struct {
	handle  *sessionHandle
	running bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// New builds a Manager, initialises modules, and collects the tool registry.
// Built-in tools register through the same ToolProvider path as module tools.
func New(deps Deps, cfg Config) (*Manager, error) {
	if deps.Store == nil || deps.Providers == nil || deps.Modules == nil {
		return nil, fmt.Errorf("agent: Store, Providers and Modules are required")
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	if deps.Root == "" {
		deps.Root = "."
	}
	m := &Manager{deps: deps, cfg: cfg.withDefaults(), log: deps.Log, rt: map[string]*runState{}}
	if deps.Builtins != nil && *m.cfg.TasksEnabled {
		deps.Builtins.Tasks = m
	}
	deps.Modules.Init(
		func(name string) module.Host { return newHost(m, name, deps.Log) },
		func(name string) module.Config { return m.cfg.ModuleConfig(name) },
	)
	list, err := deps.Modules.Tools()
	if err != nil {
		return nil, err
	}
	m.toolsByName = make(map[string]module.Tool, len(list))
	for _, t := range list {
		m.toolsByName[t.Name] = t
		m.toolSpecs = append(m.toolSpecs, provider.ToolSpec{
			Name: t.Name, Description: t.Description, Parameters: t.Schema})
	}
	return m, nil
}

// toolNames lists registered tools, sorted, for error messages.
func (m *Manager) toolNames() []string {
	out := make([]string, 0, len(m.toolsByName))
	for n := range m.toolsByName {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------- lifecycle

// Create resolves the workspace, starts a session, and dispatches SessionStart.
func (m *Manager) Create(ctx context.Context, workspacePath string, opts protocol.Options) (*session.Session, error) {
	ws, err := workspace.Resolve(workspacePath)
	if err != nil {
		return nil, err
	}
	if opts.Model == "" {
		opts.Model = m.cfg.DefaultModel
	}
	if opts.PermissionMode == "" {
		opts.PermissionMode = protocol.PermissionAsk
	}
	s, err := m.deps.Store.Create(ws.Path, ws.Key, opts)
	if err != nil {
		return nil, err
	}
	h := m.attach(s, module.Workspace{Path: ws.Path, Key: ws.Key})
	m.appendContexts(h, m.deps.Modules.SessionStart(ctx, h))
	return s, nil
}

// Adopt registers an existing session (loaded from disk) with the Manager.
func (m *Manager) Adopt(s *session.Session) *sessionHandle {
	path, key := s.Workspace()
	return m.attach(s, module.Workspace{Path: path, Key: key})
}

func (m *Manager) attach(s *session.Session, ws module.Workspace) *sessionHandle {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rs, ok := m.rt[s.ID()]; ok {
		return rs.handle
	}
	h := &sessionHandle{core: m, s: s, ws: ws}
	m.rt[s.ID()] = &runState{handle: h}
	return h
}

// handle returns the runtime handle for a session, loading it if needed.
func (m *Manager) handle(id string) (*sessionHandle, error) {
	m.mu.Lock()
	if rs, ok := m.rt[id]; ok {
		m.mu.Unlock()
		return rs.handle, nil
	}
	m.mu.Unlock()
	s, err := m.deps.Store.Get(id)
	if err != nil {
		return nil, err
	}
	return m.Adopt(s), nil
}

// appendContexts logs every module-injected block as a context event, so the
// request stays a pure function of the log.
func (m *Manager) appendContexts(h *sessionHandle, blocks []module.SourcedBlock) {
	for _, b := range blocks {
		if strings.TrimSpace(b.Block.Content) == "" {
			continue
		}
		slot := b.Block.Slot
		if slot == "" {
			slot = "prefix"
		}
		if _, err := h.s.Append(protocol.EventContext, protocol.ContextData{
			Source: "module:" + b.Module, Slot: slot, Content: b.Block.Content}); err != nil {
			m.log.Error("context append failed", "session", h.ID(), "module", b.Module, "err", err)
		}
	}
}

// Prompt appends a user message and starts the loop if it is not running.
func (m *Manager) Prompt(ctx context.Context, id, content string) (protocol.Event, error) {
	h, err := m.handle(id)
	if err != nil {
		return protocol.Event{}, err
	}
	switch st := h.State().State; st {
	case protocol.StateCompleted, protocol.StateError, protocol.StatePaused:
		return protocol.Event{}, protocol.NewRPCError(protocol.CodeInvalidTransition,
			fmt.Sprintf("session is %s; resume it first", st))
	}
	e, err := h.s.Append(protocol.EventMessage, protocol.MessageData{Role: "user", Content: content})
	if err != nil {
		return protocol.Event{}, err
	}
	m.ensureRunning(h, "prompt")
	return e, nil
}

// SetGoal records a goal and starts the loop if the session is idle.
func (m *Manager) SetGoal(ctx context.Context, id, condition string) (protocol.Event, error) {
	h, err := m.handle(id)
	if err != nil {
		return protocol.Event{}, err
	}
	if strings.TrimSpace(condition) == "" {
		return protocol.Event{}, protocol.NewRPCError(protocol.CodeInvalidParams, "condition is required")
	}
	e, err := h.s.Append(protocol.EventGoal, protocol.GoalData{
		Condition: condition, State: "set", Source: "client"})
	if err != nil {
		return protocol.Event{}, err
	}
	if h.State().State == protocol.StateIdle {
		m.ensureRunning(h, "goal")
	}
	return e, nil
}

// ClearGoal deactivates the current goal.
func (m *Manager) ClearGoal(ctx context.Context, id string) (protocol.Event, error) {
	h, err := m.handle(id)
	if err != nil {
		return protocol.Event{}, err
	}
	st := h.State()
	if !st.GoalActive() {
		return protocol.Event{}, protocol.NewRPCError(protocol.CodeInvalidTransition, "no active goal")
	}
	return h.s.Append(protocol.EventGoal, protocol.GoalData{
		Condition: st.Goal.Condition, State: "cleared", Source: "client"})
}

// SetOption changes one session option.
func (m *Manager) SetOption(ctx context.Context, id, key string, value any) (protocol.Event, error) {
	h, err := m.handle(id)
	if err != nil {
		return protocol.Event{}, err
	}
	opts := h.State().Options
	var from any
	switch key {
	case "model":
		from = opts.Model
		if _, ok := value.(string); !ok {
			return protocol.Event{}, protocol.NewRPCError(protocol.CodeInvalidParams, "model must be a string")
		}
	case "compaction_enabled":
		from = opts.CompactionEnabled
		if _, ok := value.(bool); !ok {
			return protocol.Event{}, protocol.NewRPCError(protocol.CodeInvalidParams, "compaction_enabled must be a boolean")
		}
	case "permission_mode":
		from = string(opts.PermissionMode)
		s, ok := value.(string)
		if !ok || (s != "ask" && s != "auto" && s != "bypass") {
			return protocol.Event{}, protocol.NewRPCError(protocol.CodeInvalidParams,
				"permission_mode must be ask, auto or bypass")
		}
	default:
		return protocol.Event{}, protocol.NewRPCError(protocol.CodeInvalidParams, "unknown option "+key)
	}
	fb, _ := json.Marshal(from)
	tb, _ := json.Marshal(value)
	return h.s.Append(protocol.EventOptionsChange, protocol.OptionsChangeData{
		Key: key, From: fb, To: tb, Source: "client"})
}

// SetBudget records a new loop bound.
func (m *Manager) SetBudget(ctx context.Context, id string, b protocol.BudgetData) (protocol.Event, error) {
	h, err := m.handle(id)
	if err != nil {
		return protocol.Event{}, err
	}
	if b.Source == "" {
		b.Source = "client"
	}
	return h.s.Append(protocol.EventBudget, b)
}

// UpdateTasks implements tools.TaskStore: merge the snapshot and log it.
func (m *Manager) UpdateTasks(ctx context.Context, s module.Session, incoming []protocol.Task, source string) (protocol.TasksData, error) {
	h, ok := s.(*sessionHandle)
	if !ok {
		return protocol.TasksData{}, fmt.Errorf("foreign session handle")
	}
	log := h.s.Events()
	var prev protocol.TasksData
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Type == protocol.EventTasks {
			prev = *protocol.MustData[protocol.TasksData](log[i])
			break
		}
	}
	next, err := tools.Merge(prev, incoming, log, source)
	if err != nil {
		return protocol.TasksData{}, err
	}
	if _, err := h.s.Append(protocol.EventTasks, next); err != nil {
		return protocol.TasksData{}, err
	}
	return next, nil
}

// UpdateTasksByID is the client-facing entry point (nabu.session.update_tasks).
func (m *Manager) UpdateTasksByID(ctx context.Context, id string, incoming []protocol.Task) (protocol.TasksData, error) {
	h, err := m.handle(id)
	if err != nil {
		return protocol.TasksData{}, err
	}
	return m.UpdateTasks(ctx, h, incoming, "client")
}

// ensureRunning starts the loop goroutine unless one is already running.
func (m *Manager) ensureRunning(h *sessionHandle, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	rs := m.rt[h.ID()]
	if rs == nil || rs.running {
		return
	}
	from := h.State().State
	if _, err := h.s.Append(protocol.EventStateChange, protocol.StateChangeData{
		From: &from, To: protocol.StateRunning, Reason: reason}); err != nil {
		m.log.Error("state change failed", "session", h.ID(), "err", err)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	rs.running = true
	rs.cancel = cancel
	rs.done = make(chan struct{})
	done := rs.done
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		defer close(done)
		m.runLoop(ctx, h)
		m.mu.Lock()
		rs.running = false
		rs.cancel = nil
		m.mu.Unlock()
	}()
}

// stopRunning cancels an in-flight turn and waits for the goroutine to exit.
func (m *Manager) stopRunning(id string) {
	m.mu.Lock()
	rs := m.rt[id]
	var cancel context.CancelFunc
	var done chan struct{}
	if rs != nil && rs.running {
		cancel, done = rs.cancel, rs.done
	}
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// WaitIdle blocks until the session's loop is not running.
func (m *Manager) WaitIdle(id string) {
	m.mu.Lock()
	rs := m.rt[id]
	var done chan struct{}
	if rs != nil && rs.running {
		done = rs.done
	}
	m.mu.Unlock()
	if done != nil {
		<-done
	}
}

// Interrupt cancels the current turn and returns the session to idle.
func (m *Manager) Interrupt(ctx context.Context, id string) error {
	h, err := m.handle(id)
	if err != nil {
		return err
	}
	m.stopRunning(id)
	from := h.State().State
	if from == protocol.StateIdle {
		return nil
	}
	_, err = h.s.Append(protocol.EventStateChange, protocol.StateChangeData{
		From: &from, To: protocol.StateIdle, Reason: "interrupted"})
	return err
}

// Stop ends a session: completed plus a report.
func (m *Manager) Stop(ctx context.Context, id string) error {
	h, err := m.handle(id)
	if err != nil {
		return err
	}
	m.stopRunning(id)
	if st := h.State().State; st == protocol.StateCompleted || st == protocol.StateError {
		return nil
	}
	return m.finish(ctx, h, protocol.StateCompleted, "stopped")
}

// Resume restarts a paused session, optionally raising the budget.
func (m *Manager) Resume(ctx context.Context, id string, budget *protocol.BudgetData) error {
	h, err := m.handle(id)
	if err != nil {
		return err
	}
	if h.State().State != protocol.StatePaused {
		return protocol.NewRPCError(protocol.CodeInvalidTransition, "session is not paused")
	}
	if budget != nil {
		if _, err := m.SetBudget(ctx, id, *budget); err != nil {
			return err
		}
	}
	m.deps.Modules.SessionResume(ctx, h)
	m.ensureRunning(h, "resumed")
	return nil
}

// Shutdown cancels every running turn and pauses those sessions so a restart
// can resume them (spec §8 graceful restart).
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	ids := make([]string, 0, len(m.rt))
	for id, rs := range m.rt {
		if rs.running {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.stopRunning(id)
		h, err := m.handle(id)
		if err != nil {
			continue
		}
		if h.State().State != protocol.StateRunning {
			continue
		}
		h.s.Append(protocol.EventNotice, protocol.NoticeData{
			Source: "daemon", Level: "warn", Message: "daemon is shutting down; session paused"})
		from := protocol.StateRunning
		h.s.Append(protocol.EventStateChange, protocol.StateChangeData{
			From: &from, To: protocol.StatePaused, Reason: "daemon_shutdown"})
	}
	m.wg.Wait()
	return nil
}

// finish moves a session to a terminal or paused state and emits the report.
func (m *Manager) finish(ctx context.Context, h *sessionHandle, to protocol.SessionState, reason string) error {
	m.deps.Modules.SessionEnd(ctx, h)
	from := h.State().State
	if from != to {
		if _, err := h.s.Append(protocol.EventStateChange, protocol.StateChangeData{
			From: &from, To: to, Reason: reason}); err != nil {
			return err
		}
	}
	return m.emitReport(ctx, h, to)
}

// emitReport builds the run report: core fills the log-derived fields,
// Reporter modules fill workspace observations (spec §13).
func (m *Manager) emitReport(ctx context.Context, h *sessionHandle, exit protocol.SessionState) error {
	st := h.State()
	r := protocol.ReportData{
		ExitStatus: exit,
		Tasks: protocol.ReportTasks{
			Total: len(st.Tasks), Done: st.DoneTasks(), Open: []protocol.ReportTaskRef{}},
		Checks: []protocol.ReportCheck{},
	}
	for _, t := range st.OpenTasks() {
		r.Tasks.Open = append(r.Tasks.Open, protocol.ReportTaskRef{ID: t.ID, Title: t.Title, Status: t.Status})
	}
	if st.Goal != nil {
		r.Goal = &protocol.ReportGoal{Condition: st.Goal.Condition, State: st.Goal.State, Reason: st.Goal.Reason}
	}
	for _, e := range h.s.Events() {
		if e.Type != protocol.EventCheck {
			continue
		}
		c := protocol.MustData[protocol.CheckData](e)
		r.Checks = append(r.Checks, protocol.ReportCheck{Name: c.Name, Status: c.Status, Summary: c.Summary})
	}
	f := m.deps.Modules.Report(ctx, h)
	r.FilesTouched, r.Commits, r.TreeDirty = f.FilesTouched, f.Commits, f.TreeDirty
	r.Checks = append(r.Checks, f.Checks...)
	_, err := h.s.Append(protocol.EventReport, r)
	return err
}

// ---------------------------------------------------------------- core impl

// invokeTool logs the call, gates it, runs it, logs the result, and notifies
// observers. Every tool call in nabu goes through here, model or module.
func (m *Manager) invokeTool(ctx context.Context, h *sessionHandle, call protocol.ToolCallData) protocol.ToolResultData {
	if len(call.Arguments) == 0 {
		call.Arguments = json.RawMessage(`{}`)
	}
	if _, err := h.s.Append(protocol.EventToolCall, call); err != nil {
		m.log.Error("tool call append failed", "session", h.ID(), "err", err)
		return protocol.ToolResultData{CallID: call.CallID, Tool: call.Tool, Content: err.Error(), Status: "error"}
	}
	res := m.executeTool(ctx, h, call)
	if _, err := h.s.Append(protocol.EventToolResult, res); err != nil {
		m.log.Error("tool result append failed", "session", h.ID(), "err", err)
	}
	m.deps.Modules.ToolResult(ctx, h, call, res)
	return res
}

func (m *Manager) executeTool(ctx context.Context, h *sessionHandle, call protocol.ToolCallData) protocol.ToolResultData {
	fail := func(msg string) protocol.ToolResultData {
		return protocol.ToolResultData{CallID: call.CallID, Tool: call.Tool, Content: msg, Status: "error"}
	}
	tool, ok := m.toolsByName[call.Tool]
	if !ok {
		return fail(fmt.Sprintf("unknown tool %q; available: %s", call.Tool, strings.Join(m.toolNames(), ", ")))
	}
	v := m.deps.Modules.GateTool(ctx, h, call)
	switch v.Decision {
	case module.Deny:
		return fail("denied: " + v.Reason)
	case module.Ask:
		if h.State().Options.PermissionMode != protocol.PermissionBypass {
			if m.deps.Asker == nil {
				return fail("denied: this call needs approval and no client is attached")
			}
			summary := v.Summary
			if summary == "" {
				summary = call.Tool
			}
			approved, reason := m.deps.Asker.Permission(ctx, h.ID(), call, summary, v.Risk)
			if !approved {
				if reason == "" {
					reason = "not approved"
				}
				return fail("denied by the user: " + reason)
			}
		}
	}
	out, err := tool.Run(ctx, h, call.Arguments)
	if err != nil {
		msg := err.Error()
		if strings.TrimSpace(out) != "" {
			msg = out
		}
		return fail(msg)
	}
	return protocol.ToolResultData{CallID: call.CallID, Tool: call.Tool, Content: out, Status: "ok"}
}

// complete runs a module's model call through the provider layer, so
// max_in_flight and usage accounting hold for judges and curators too.
func (m *Manager) complete(ctx context.Context, h *sessionHandle, req module.CompletionRequest) (module.CompletionResponse, error) {
	modelStr := req.Model
	if modelStr == "" {
		modelStr = h.State().Options.Model
	}
	p, name, _, err := m.deps.Providers.Resolve(modelStr)
	if err != nil {
		return module.CompletionResponse{}, err
	}
	var msgs []provider.Message
	if req.System != "" {
		msgs = append(msgs, provider.Message{Role: "system", Content: req.System})
	}
	for _, mm := range req.Messages {
		msgs = append(msgs, provider.Message{Role: mm.Role, Content: mm.Content})
	}
	resp, err := p.Complete(ctx, provider.Request{
		Model: name, Messages: msgs, MaxTokens: req.MaxTokens}, nil)
	if err != nil {
		return module.CompletionResponse{}, err
	}
	return module.CompletionResponse{Content: resp.Content, Usage: resp.Usage}, nil
}

func (m *Manager) ask(ctx context.Context, h *sessionHandle, question string, choices []string) (string, error) {
	if m.deps.Asker == nil {
		return "", fmt.Errorf("no client is attached to answer")
	}
	return m.deps.Asker.Ask(ctx, h.ID(), question, choices)
}

func (m *Manager) dataDir(name string) (string, error) {
	d := filepath.Join(m.deps.Root, "modules", name)
	return d, os.MkdirAll(d, 0o755)
}
