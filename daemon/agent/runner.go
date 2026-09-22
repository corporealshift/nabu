package agent

import (
	"context"
	"fmt"

	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/protocol"
)

// runLoop drives a session's turns until it stops, pauses, blocks, or fails.
// Every transition it makes is an appended event; it never mutates state in
// memory. It is the only caller of the provider for a session's own turns.
func (m *Manager) runLoop(ctx context.Context, h *sessionHandle) {
	for {
		if ctx.Err() != nil {
			return // Interrupt/Shutdown appends the state change
		}
		cont, err := m.turn(ctx, h)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			h.s.Append(protocol.EventNotice, protocol.NoticeData{
				Source: "daemon", Level: "error", Message: err.Error()})
			m.finish(ctx, h, protocol.StateError, "provider error")
			return
		}
		if !cont {
			return
		}
	}
}

// turn performs one model round trip plus any tool calls it requested.
// It returns true when the loop should continue.
func (m *Manager) turn(ctx context.Context, h *sessionHandle) (bool, error) {
	log := h.s.Events()
	st := protocol.Project(log)

	if over, why := budgetExceeded(st); over {
		h.s.Append(protocol.EventNotice, protocol.NoticeData{
			Source: "daemon", Level: "warn", Message: why})
		return false, m.finish(ctx, h, protocol.StatePaused, "budget")
	}

	p, modelName, pcfg, err := m.deps.Providers.Resolve(st.Options.Model)
	if err != nil {
		return false, err
	}

	// Suffix blocks modules want in front of the model this turn.
	m.appendContexts(h, m.deps.Modules.BeforeRequest(ctx, h))
	log = h.s.Events()

	req := buildRequest(log, m.cfg.SystemPrompt, modelName, m.toolsFor(pcfg), m.cfg.MaxTokens)
	turnID := protocol.NewULID()
	onDelta := func(text string) {
		if m.deps.Deltas != nil {
			m.deps.Deltas(h.ID(), turnID, text)
		}
	}
	onThinking := func(text string) {
		if m.deps.Thinking != nil {
			m.deps.Thinking(h.ID(), turnID, text)
		}
	}
	resp, err := p.Complete(ctx, req, onDelta, onThinking)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, fmt.Errorf("model call failed: %w", err)
	}

	// Thinking is appended first, so a reader that stops at the message has
	// already seen the reasoning behind it (spec 3.1).
	if resp.Reasoning != "" {
		thought := protocol.ThinkingData{Content: resp.Reasoning, Source: "model"}
		if _, err := h.s.Append(protocol.EventThinking, thought); err != nil {
			return false, err
		}
	}

	// The provider rebuilt a call the server had reported as thinking. Saying
	// so keeps the log honest — the alternative is quietly rewriting what the
	// model produced — and names a fault that lives upstream of nabu.
	if resp.Recovered > 0 {
		h.s.Append(protocol.EventNotice, protocol.NoticeData{Source: "daemon", Level: "warn",
			Message: fmt.Sprintf("the model wrote %s into its reasoning instead of calling %s; "+
				"recovered and ran anyway", plural(resp.Recovered, "tool call"), thatOrThose(resp.Recovered))})
	}

	msg := protocol.MessageData{Role: "assistant", Content: resp.Content}
	if resp.Usage != (protocol.Usage{}) {
		u := resp.Usage
		msg.Usage = &u
	}
	if _, err := h.s.Append(protocol.EventMessage, msg); err != nil {
		return false, err
	}
	m.deps.Modules.TurnEnd(ctx, h)

	if len(resp.ToolCalls) > 0 {
		for _, tc := range resp.ToolCalls {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			m.invokeTool(ctx, h, protocol.ToolCallData{
				CallID: tc.ID, Tool: tc.Name, Arguments: tc.Arguments, Source: "model"})
		}
		return m.afterTurn(ctx, h, pcfg)
	}

	// No tool calls: the model believes it is finished. Nothing stops until
	// every stop gate agrees (spec §10.1).
	return m.askStopGate(ctx, h)
}

// afterTurn runs between turns: compaction happens here so the next request
// is built from an already-shrunk log.
func (m *Manager) afterTurn(ctx context.Context, h *sessionHandle, pcfg provider.Config) (bool, error) {
	if err := m.maybeCompact(ctx, h, pcfg); err != nil {
		return false, err
	}
	// maybeCompact may have ended the session (compaction disabled at the limit).
	if st := h.State().State; st != protocol.StateRunning {
		return false, nil
	}
	return true, nil
}

// toIdle returns the session to idle.
func (m *Manager) toIdle(ctx context.Context, h *sessionHandle, reason string) error {
	from := h.State().State
	if from == protocol.StateIdle {
		return nil
	}
	_, err := h.s.Append(protocol.EventStateChange, protocol.StateChangeData{
		From: &from, To: protocol.StateIdle, Reason: reason})
	return err
}

// plural renders a count with its noun: "1 tool call", "2 tool calls".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func thatOrThose(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}
