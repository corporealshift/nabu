package module

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

// Options configure a Registry.
type Options struct {
	// HookTimeout bounds every hook call. Zero means 30 seconds.
	HookTimeout time.Duration
	// GateTimeout bounds ToolGate and StopGate calls, which may run commands.
	// Zero means 10 minutes.
	GateTimeout time.Duration
	// CompactionTimeout bounds BeforeCompaction and AfterCompaction. A hook
	// there may make a model call of its own (the memory curator does), and a
	// local model reading a long session takes minutes, not the seconds
	// HookTimeout allows. Zero means 10 minutes, the provider's own per-attempt
	// limit: a hook allowed less than one model call cannot finish one.
	CompactionTimeout time.Duration
	Log               *slog.Logger
}

// Registry holds the compiled-in modules, dispatches hooks in registration
// order, and isolates failures: a hook that panics or exceeds its timeout
// disables that module for the rest of the session and appends a notice.
type Registry struct {
	modules []Module
	opts    Options
	mu      sync.Mutex
	// disabled[sessionID][moduleName]
	disabled map[string]map[string]bool
	// dead modules failed Init and are skipped everywhere.
	dead map[string]error
}

// NewRegistry builds a registry. Modules are dispatched in the given order.
func NewRegistry(mods []Module, opts Options) *Registry {
	if opts.HookTimeout == 0 {
		opts.HookTimeout = 30 * time.Second
	}
	if opts.GateTimeout == 0 {
		opts.GateTimeout = 10 * time.Minute
	}
	if opts.CompactionTimeout == 0 {
		opts.CompactionTimeout = 10 * time.Minute
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	return &Registry{modules: mods, opts: opts, disabled: map[string]map[string]bool{}, dead: map[string]error{}}
}

// Init initialises every module. hostFor returns the Host for a module: each
// gets its own so its tool calls carry source module:<name>. configFor returns
// its config section; a module whose config says enabled=false is not
// initialised. Init failures are recorded, not fatal: the daemon runs without
// that module.
func (r *Registry) Init(hostFor func(name string) Host, configFor func(name string) Config) {
	for _, m := range r.modules {
		cfg := configFor(m.Name())
		if !cfg.Enabled() {
			r.dead[m.Name()] = fmt.Errorf("disabled by config")
			continue
		}
		func() {
			defer func() {
				if p := recover(); p != nil {
					r.dead[m.Name()] = fmt.Errorf("panic in Init: %v", p)
				}
			}()
			if err := m.Init(hostFor(m.Name()), cfg); err != nil {
				r.dead[m.Name()] = err
			}
		}()
		if err, bad := r.dead[m.Name()]; bad {
			r.opts.Log.Error("module init failed", "module", m.Name(), "err", err)
		}
	}
}

// Modules returns the active modules in dispatch order.
func (r *Registry) Modules() []Module {
	var out []Module
	for _, m := range r.modules {
		if _, bad := r.dead[m.Name()]; !bad {
			out = append(out, m)
		}
	}
	return out
}

// Dead returns the modules that failed Init or are disabled by config.
func (r *Registry) Dead() map[string]error { return r.dead }

// Tools collects tools from every active ToolProvider, in order. Duplicate
// names are an error: two modules may not both claim a tool.
func (r *Registry) Tools() ([]Tool, error) {
	seen := map[string]string{}
	var out []Tool
	for _, m := range r.Modules() {
		tp, ok := m.(ToolProvider)
		if !ok {
			continue
		}
		for _, t := range tp.Tools() {
			if prev, dup := seen[t.Name]; dup {
				return nil, fmt.Errorf("tool %q provided by both %s and %s", t.Name, prev, m.Name())
			}
			seen[t.Name] = m.Name()
			out = append(out, t)
		}
	}
	return out, nil
}

// call runs fn under recover() and a timeout. On panic or timeout the module
// is disabled for the session and a notice is appended. It returns false if
// the hook did not complete normally.
func (r *Registry) call(ctx context.Context, s Session, m Module, hook string, timeout time.Duration, fn func(context.Context)) bool {
	if r.isDisabled(s.ID(), m.Name()) {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	done := make(chan any, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				done <- fmt.Errorf("panic: %v\n%s", p, debug.Stack())
				return
			}
			done <- nil
		}()
		fn(cctx)
	}()
	select {
	case res := <-done:
		if err, failed := res.(error); failed {
			r.disable(s, m.Name(), hook, err)
			return false
		}
		return true
	case <-cctx.Done():
		if ctx.Err() != nil {
			// The caller cancelled; not the module's fault.
			return false
		}
		r.disable(s, m.Name(), hook, fmt.Errorf("timed out after %s", timeout))
		return false
	}
}

func (r *Registry) isDisabled(sessionID, name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.disabled[sessionID][name]
}

func (r *Registry) disable(s Session, name, hook string, err error) {
	r.mu.Lock()
	if r.disabled[s.ID()] == nil {
		r.disabled[s.ID()] = map[string]bool{}
	}
	r.disabled[s.ID()][name] = true
	r.mu.Unlock()
	msg := fmt.Sprintf("module %s disabled for this session: %s failed: %v", name, hook, err)
	r.opts.Log.Error("module disabled", "module", name, "hook", hook, "session", s.ID(), "err", err)
	_, _ = s.Append(protocol.EventNotice, protocol.NoticeData{Source: "daemon", Level: "error", Message: msg})
}

// ForgetSession drops per-session disable state once a session ends.
func (r *Registry) ForgetSession(sessionID string) {
	r.mu.Lock()
	delete(r.disabled, sessionID)
	r.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Dispatchers. Each returns aggregated results in registration order.

// SessionStart dispatches to every SessionStarter and returns their blocks
// tagged with the module name.
func (r *Registry) SessionStart(ctx context.Context, s Session) []SourcedBlock {
	var out []SourcedBlock
	for _, m := range r.Modules() {
		h, ok := m.(SessionStarter)
		if !ok {
			continue
		}
		var blocks []ContextBlock
		var err error
		if r.call(ctx, s, m, "SessionStart", r.opts.HookTimeout, func(c context.Context) { blocks, err = h.SessionStart(c, s) }) {
			if err != nil {
				r.disable(s, m.Name(), "SessionStart", err)
				continue
			}
			out = appendSourced(out, m.Name(), blocks)
		}
	}
	return out
}

// BeforeRequest dispatches to every RequestHook. Prefix blocks are dropped
// with a notice, per spec: the prefix may not change between compactions.
func (r *Registry) BeforeRequest(ctx context.Context, s Session) []SourcedBlock {
	var out []SourcedBlock
	for _, m := range r.Modules() {
		h, ok := m.(RequestHook)
		if !ok {
			continue
		}
		var blocks []ContextBlock
		var err error
		if r.call(ctx, s, m, "BeforeRequest", r.opts.HookTimeout, func(c context.Context) { blocks, err = h.BeforeRequest(c, s) }) {
			if err != nil {
				r.disable(s, m.Name(), "BeforeRequest", err)
				continue
			}
			for _, b := range blocks {
				if b.Slot != "suffix" {
					_, _ = s.Append(protocol.EventNotice, protocol.NoticeData{Source: "daemon", Level: "warn",
						Message: fmt.Sprintf("module %s returned a %s block from BeforeRequest; only suffix is allowed, dropped", m.Name(), b.Slot)})
					continue
				}
				out = append(out, SourcedBlock{Module: m.Name(), Block: b})
			}
		}
	}
	return out
}

// GateTool asks every ToolGate in order. The first Deny wins; otherwise an
// Ask from any gate yields a single Ask; otherwise Allow.
func (r *Registry) GateTool(ctx context.Context, s Session, call protocol.ToolCallData) Verdict {
	final := Verdict{Decision: Allow}
	for _, m := range r.Modules() {
		g, ok := m.(ToolGate)
		if !ok {
			continue
		}
		var v Verdict
		if !r.call(ctx, s, m, "GateTool", r.opts.GateTimeout, func(c context.Context) { v = g.GateTool(c, s, call) }) {
			continue
		}
		switch v.Decision {
		case Deny:
			return v
		case Ask:
			if final.Decision == Allow {
				final = v
			}
		}
	}
	return final
}

// BeforeStop asks every StopGate and returns every veto, in order.
func (r *Registry) BeforeStop(ctx context.Context, s Session, info StopInfo) []protocol.StopVetoData {
	var vetoes []protocol.StopVetoData
	for _, m := range r.Modules() {
		g, ok := m.(StopGate)
		if !ok {
			continue
		}
		var v StopVerdict
		if !r.call(ctx, s, m, "BeforeStop", r.opts.GateTimeout, func(c context.Context) { v = g.BeforeStop(c, s, info) }) {
			continue
		}
		if !v.Allow {
			reason := v.Reason
			if reason == "" {
				reason = "no reason given"
			}
			vetoes = append(vetoes, protocol.StopVetoData{Module: m.Name(), Reason: reason})
		}
	}
	return vetoes
}

// ToolResult notifies every ToolObserver.
func (r *Registry) ToolResult(ctx context.Context, s Session, call protocol.ToolCallData, res protocol.ToolResultData) {
	for _, m := range r.Modules() {
		if o, ok := m.(ToolObserver); ok {
			r.call(ctx, s, m, "ToolResult", r.opts.HookTimeout, func(c context.Context) { o.ToolResult(c, s, call, res) })
		}
	}
}

// TurnEnd notifies every TurnObserver.
func (r *Registry) TurnEnd(ctx context.Context, s Session) {
	for _, m := range r.Modules() {
		if o, ok := m.(TurnObserver); ok {
			r.call(ctx, s, m, "TurnEnd", r.opts.HookTimeout, func(c context.Context) { o.TurnEnd(c, s) })
		}
	}
}

// BeforeCompaction collects preserve strings from every CompactionHook.
func (r *Registry) BeforeCompaction(ctx context.Context, s Session, rg Range) []string {
	var out []string
	for _, m := range r.Modules() {
		if h, ok := m.(CompactionHook); ok {
			var p []string
			if r.call(ctx, s, m, "BeforeCompaction", r.opts.CompactionTimeout, func(c context.Context) { p = h.BeforeCompaction(c, s, rg) }) {
				out = append(out, p...)
			}
		}
	}
	return out
}

// AfterCompaction collects re-injected blocks from every CompactionHook.
func (r *Registry) AfterCompaction(ctx context.Context, s Session) []SourcedBlock {
	var out []SourcedBlock
	for _, m := range r.Modules() {
		h, ok := m.(CompactionHook)
		if !ok {
			continue
		}
		var blocks []ContextBlock
		var err error
		if r.call(ctx, s, m, "AfterCompaction", r.opts.CompactionTimeout, func(c context.Context) { blocks, err = h.AfterCompaction(c, s) }) {
			if err != nil {
				r.disable(s, m.Name(), "AfterCompaction", err)
				continue
			}
			out = appendSourced(out, m.Name(), blocks)
		}
	}
	return out
}

// SessionResume notifies every ResumeHook.
func (r *Registry) SessionResume(ctx context.Context, s Session) {
	for _, m := range r.Modules() {
		if h, ok := m.(ResumeHook); ok {
			var err error
			if r.call(ctx, s, m, "SessionResume", r.opts.HookTimeout, func(c context.Context) { err = h.SessionResume(c, s) }) && err != nil {
				r.disable(s, m.Name(), "SessionResume", err)
			}
		}
	}
}

// SessionEnd notifies every SessionEnder.
func (r *Registry) SessionEnd(ctx context.Context, s Session) {
	for _, m := range r.Modules() {
		if h, ok := m.(SessionEnder); ok {
			r.call(ctx, s, m, "SessionEnd", r.opts.HookTimeout, func(c context.Context) { h.SessionEnd(c, s) })
		}
	}
}

// Report merges every Reporter's fields. Later modules append to lists; a
// TreeDirty from any reporter wins over nil.
func (r *Registry) Report(ctx context.Context, s Session) ReportFields {
	var out ReportFields
	for _, m := range r.Modules() {
		rep, ok := m.(Reporter)
		if !ok {
			continue
		}
		var f ReportFields
		var err error
		if r.call(ctx, s, m, "Report", r.opts.GateTimeout, func(c context.Context) { f, err = rep.Report(c, s) }) {
			if err != nil {
				r.disable(s, m.Name(), "Report", err)
				continue
			}
			out.FilesTouched = append(out.FilesTouched, f.FilesTouched...)
			out.Commits = append(out.Commits, f.Commits...)
			out.Checks = append(out.Checks, f.Checks...)
			if f.TreeDirty != nil {
				out.TreeDirty = f.TreeDirty
			}
		}
	}
	return out
}

// SourcedBlock is a ContextBlock tagged with the module that produced it, so
// the host can stamp the context event's source.
type SourcedBlock struct {
	Module string
	Block  ContextBlock
}

func appendSourced(out []SourcedBlock, name string, blocks []ContextBlock) []SourcedBlock {
	for _, b := range blocks {
		out = append(out, SourcedBlock{Module: name, Block: b})
	}
	return out
}
