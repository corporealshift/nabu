package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/session"
	"github.com/corporealshift/nabu/protocol"
)

// core is what the handle and host need from the Manager. The Manager
// implements it; tests use a fake.
type core interface {
	invokeTool(ctx context.Context, h *sessionHandle, call protocol.ToolCallData) protocol.ToolResultData
	complete(ctx context.Context, h *sessionHandle, req module.CompletionRequest) (module.CompletionResponse, error)
	ask(ctx context.Context, h *sessionHandle, question string, choices []string) (string, error)
	dataDir(module string) (string, error)
}

// sessionHandle is the module.Session a module receives. It restricts what a
// module may append: check, notice, and goal verdicts (met|unmet|impossible).
type sessionHandle struct {
	core core
	s    *session.Session
	ws   module.Workspace
}

func (h *sessionHandle) ID() string                  { return h.s.ID() }
func (h *sessionHandle) Workspace() module.Workspace { return h.ws }
func (h *sessionHandle) State() protocol.State       { return h.s.State() }

func (h *sessionHandle) Events(after *string) ([]protocol.Event, error) {
	ev, _, err := h.s.EventsAfter(after)
	return ev, err
}

func (h *sessionHandle) Append(t protocol.EventType, data any) (protocol.Event, error) {
	switch t {
	case protocol.EventCheck, protocol.EventNotice:
	case protocol.EventGoal:
		b, err := json.Marshal(data)
		if err != nil {
			return protocol.Event{}, err
		}
		var g protocol.GoalData
		if err := json.Unmarshal(b, &g); err != nil {
			return protocol.Event{}, err
		}
		switch g.State {
		case "met", "unmet", "impossible":
		default:
			return protocol.Event{}, fmt.Errorf(
				"modules may only append goal verdicts (met|unmet|impossible), not %q", g.State)
		}
	default:
		return protocol.Event{}, fmt.Errorf("modules may not append %s events", t)
	}
	return h.s.Append(t, data)
}

// host is the per-module module.Host.
type host struct {
	core core
	name string
	log  *slog.Logger
}

func newHost(c core, name string, log *slog.Logger) *host {
	if log == nil {
		log = slog.Default()
	}
	return &host{core: c, name: name, log: log.With("module", name)}
}

func (h *host) Model() module.Model                 { return modelAPI{h} }
func (h *host) Tools() module.ToolCaller            { return toolCaller{h} }
func (h *host) UI() module.UI                       { return uiAPI{h} }
func (h *host) Log() *slog.Logger                   { return h.log }
func (h *host) DataDir(name string) (string, error) { return h.core.dataDir(name) }

type modelAPI struct{ h *host }

func (m modelAPI) Complete(ctx context.Context, s module.Session, req module.CompletionRequest) (module.CompletionResponse, error) {
	sh, ok := s.(*sessionHandle)
	if !ok {
		return module.CompletionResponse{}, fmt.Errorf("module %s: foreign session handle", m.h.name)
	}
	return m.h.core.complete(ctx, sh, req)
}

type toolCaller struct{ h *host }

func (t toolCaller) Call(ctx context.Context, s module.Session, tool string, args json.RawMessage) (protocol.ToolResultData, error) {
	sh, ok := s.(*sessionHandle)
	if !ok {
		return protocol.ToolResultData{}, fmt.Errorf("module %s: foreign session handle", t.h.name)
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	call := protocol.ToolCallData{
		CallID:    "mod_" + protocol.NewULID(),
		Tool:      tool,
		Arguments: args,
		Source:    "module:" + t.h.name,
	}
	res := t.h.core.invokeTool(ctx, sh, call)
	if res.Status == "error" {
		return res, fmt.Errorf("%s: %s", tool, res.Content)
	}
	return res, nil
}

type uiAPI struct{ h *host }

func (u uiAPI) Ask(ctx context.Context, s module.Session, question string, choices []string) (string, error) {
	sh, ok := s.(*sessionHandle)
	if !ok {
		return "", fmt.Errorf("module %s: foreign session handle", u.h.name)
	}
	return u.h.core.ask(ctx, sh, question, choices)
}
