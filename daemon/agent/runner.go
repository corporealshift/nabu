package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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

	maxTokens := replyCap(log, pcfg, m.cfg.MaxTokens)
	req := buildRequest(log, m.cfg.SystemPrompt, modelName, m.toolsFor(pcfg), maxTokens)
	turnID := protocol.NewULID()
	// Each stream is watched on its own. A reply that starts repeating a
	// passage ends the call within a few copies rather than at the cap.
	//
	// Thinking that repeats is only trimmed from the log. Stopping it ended
	// two turns, and blocked the session each time, a thousand tokens into a
	// loop the server's own reasoning budget would have closed with "let's
	// answer now". Thinking never goes back to the model, so the loop costs
	// time, and the reply cap bounds that where a server sets no budget.
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	reply, thought := newRepeatWatch(pcfg.RepeatLimit), newRepeatWatch(pcfg.RepeatLimit)
	replyTripped := false
	onDelta := func(text string) {
		if !replyTripped && reply.add(text) {
			replyTripped = true
			cancel()
		}
		if m.deps.Deltas != nil {
			m.deps.Deltas(h.ID(), turnID, text)
		}
	}
	onThinking := func(text string) {
		thought.add(text)
		if m.deps.Thinking != nil {
			m.deps.Thinking(h.ID(), turnID, text)
		}
	}
	resp, err := p.Complete(callCtx, req, onDelta, onThinking)
	if replyTripped && ctx.Err() == nil {
		return false, m.stopRepeating(ctx, h, thought, reply, resp.Reasoning)
	}
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		// What streamed is otherwise gone: deltas are never logged. It is
		// the only evidence of why a call ran until it timed out.
		if got := partialReply(resp); got != "" {
			h.s.Append(protocol.EventNotice, protocol.NoticeData{
				Source: "daemon", Level: "warn", Message: got})
		}
		return false, fmt.Errorf("model call failed: %w", err)
	}

	// Thinking is appended first, so a reader that stops at the message has
	// already seen the reasoning behind it (spec 3.1).
	if err := m.logThinking(h, thought, resp.Reasoning); err != nil {
		return false, err
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

	cutOff := resp.FinishReason == "length"
	if cutOff {
		h.s.Append(protocol.EventNotice, protocol.NoticeData{Source: "daemon", Level: "warn",
			Message: fmt.Sprintf("the reply reached its %d-token cap and was cut off", maxTokens)})
	}

	if len(resp.ToolCalls) > 0 {
		h.halted.Store(nil)
		for i, tc := range resp.ToolCalls {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			call := protocol.ToolCallData{
				CallID: tc.ID, Tool: tc.Name, Arguments: tc.Arguments, Source: "model"}
			// The cap fell inside the last call, so its arguments are only a
			// beginning. Running it would write half a file; the model is
			// told why instead, since the same call again ends the same way.
			if cutOff && i == len(resp.ToolCalls)-1 {
				m.refuseTool(h, call, protocol.ToolErrorInvalidArgs, fmt.Sprintf(
					"not run: your reply reached the %d-token limit and was cut off before this call was complete. "+
						"Write a large file in parts: create it with the first part, then add the rest with edit.",
					maxTokens))
				continue
			}
			m.invokeTool(ctx, h, call)
			// A gate halted the session: the rest of the batch does not run,
			// and no further turn is taken.
			if why := h.halted.Swap(nil); why != nil {
				h.s.Append(protocol.EventNotice, protocol.NoticeData{
					Source: "daemon", Level: "warn", Message: "stopping: " + *why})
				return false, m.finish(ctx, h, protocol.StateBlocked, *why)
			}
		}
		return m.afterTurn(ctx, h, pcfg)
	}

	// No tool calls: the model believes it is finished. Nothing stops until
	// every stop gate agrees (spec §10.1).
	return m.askStopGate(ctx, h)
}

// logThinking appends a turn's thinking, with any repeated run collapsed to
// one copy and a notice saying so. The whole stream went to clients live; the
// log keeps what a reader needs, not the runaway.
func (m *Manager) logThinking(h *sessionHandle, thought *repeatWatch, reasoning string) error {
	if reasoning == "" {
		return nil
	}
	text, dropped := thought.collapsed(reasoning)
	if _, err := h.s.Append(protocol.EventThinking, protocol.ThinkingData{Content: text, Source: "model"}); err != nil {
		return err
	}
	if dropped > 0 {
		h.s.Append(protocol.EventNotice, protocol.NoticeData{Source: "daemon", Level: "info", Message: fmt.Sprintf(
			"the model's thinking repeated one passage %d more times; the copies were dropped from the log and the turn went on. The passage: %q",
			dropped, strings.ToValidUTF8(truncateText(thought.passage(), 200), ""))})
	}
	return nil
}

// stopRepeating ends a turn whose reply began repeating one passage. What
// streamed is logged only up to the first copy, so the runaway is never sent
// back to the model that wrote it, and no call in it runs. The session blocks
// rather than retrying: a model that does this has usually been circling for
// a while, and the person decides what comes next.
func (m *Manager) stopRepeating(ctx context.Context, h *sessionHandle, thought, reply *repeatWatch, reasoning string) error {
	if err := m.logThinking(h, thought, reasoning); err != nil {
		return err
	}
	if _, err := h.s.Append(protocol.EventMessage, protocol.MessageData{Role: "assistant", Content: reply.kept()}); err != nil {
		return err
	}
	m.deps.Modules.TurnEnd(ctx, h)
	h.s.Append(protocol.EventNotice, protocol.NoticeData{Source: "daemon", Level: "warn", Message: fmt.Sprintf(
		"the model's reply repeated one passage %d times, so it was stopped after %d bytes and %d were dropped. The passage: %q",
		reply.repeats(), reply.length(), reply.length()-len(reply.kept()), strings.ToValidUTF8(truncateText(reply.passage(), 200), ""))})
	return m.finish(ctx, h, protocol.StateBlocked, "the reply repeated itself")
}

// partialTail is how much of a failed reply's end partialReply quotes. The
// end is what shows a loop; the whole would put a runaway into the log.
const partialTail = 2000

// partialReply describes what a failed call had written, or "" if nothing
// arrived. It is a notice, read by people and never sent to the model: a
// reply that ran away should not be fed back to the model that wrote it.
func partialReply(r provider.Response) string {
	var parts []string
	last := ""
	if r.Reasoning != "" {
		parts = append(parts, fmt.Sprintf("%d characters of thinking", len(r.Reasoning)))
		last = r.Reasoning
	}
	if r.Content != "" {
		parts = append(parts, fmt.Sprintf("%d characters of reply", len(r.Content)))
		last = r.Content
	}
	for _, tc := range r.ToolCalls {
		args := rawArgs(tc.Arguments)
		parts = append(parts, fmt.Sprintf("a %s call with %d characters of arguments", tc.Name, len(args)))
		last = args
	}
	if len(parts) == 0 {
		return ""
	}
	if len(last) > partialTail {
		last = "…" + strings.ToValidUTF8(last[len(last)-partialTail:], "")
	}
	return "before the call failed, the model had written " + strings.Join(parts, ", ") +
		". It ended:\n" + last
}

// rawArgs is a tool call's arguments as the model wrote them. Unfinished JSON
// comes back from the provider wrapped as {"_malformed": "..."}.
func rawArgs(args json.RawMessage) string {
	var wrapped struct {
		Malformed *string `json:"_malformed"`
	}
	if json.Unmarshal(args, &wrapped) == nil && wrapped.Malformed != nil {
		return *wrapped.Malformed
	}
	return string(args)
}

// refuseTool logs a call the daemon will not run, and why, without running
// it or asking any gate.
func (m *Manager) refuseTool(h *sessionHandle, call protocol.ToolCallData, kind, why string) {
	if len(call.Arguments) == 0 {
		call.Arguments = json.RawMessage(`{}`)
	}
	if _, err := h.s.Append(protocol.EventToolCall, call); err != nil {
		m.log.Error("tool call append failed", "session", h.ID(), "err", err)
		return
	}
	if _, err := h.s.Append(protocol.EventToolResult, protocol.ToolResultData{
		CallID: call.CallID, Tool: call.Tool, Content: why, Status: "error", Kind: kind}); err != nil {
		m.log.Error("tool result append failed", "session", h.ID(), "err", err)
	}
}

// minReplyCap is the least a reply is allowed, however full the context: a
// cap smaller than this cuts off even a short answer.
const minReplyCap = 1024

// replyCap is the most one reply may write: the provider's cap, or the
// daemon-wide override, and never more than the context has room for. The
// room is judged from the last request's size, which is all the log knows.
func replyCap(log []protocol.Event, pcfg provider.Config, override int) int {
	limit := pcfg.MaxTokens
	if override > 0 {
		limit = override
	}
	if limit <= 0 || pcfg.ContextWindow <= 0 {
		return limit
	}
	if room := pcfg.ContextWindow - lastInputTokens(log); room < limit {
		limit = max(room, minReplyCap)
	}
	return limit
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
