package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/protocol"
)

// summarizePrompt asks the model to compress the earlier conversation. It
// maximises recall first, as Anthropic's context-engineering guidance
// recommends: decisions, unresolved problems, and what was actually done.
const summarizePrompt = `Summarize the conversation so far for an agent that will continue the work with no other memory of it.

Keep, in this order:
1. What the user asked for, in their terms, including constraints they stated.
2. Decisions made and why, including approaches that were tried and rejected.
3. What has actually been done: files changed, commands run and their results.
4. What is unresolved: open questions, failing checks, known bugs.
5. Anything the next turn must not do.

Drop pleasantries, repeated tool output, and narration. Be specific: name files, commands and errors. Do not invent anything that is not in the conversation.`

// lastInputTokens returns the most recent assistant turn's input token count,
// the best available estimate of the current request size.
func lastInputTokens(log []protocol.Event) int {
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Type != protocol.EventMessage {
			continue
		}
		d := protocol.MustData[protocol.MessageData](log[i])
		if d.Role == "assistant" && d.Usage != nil {
			return d.Usage.InputTokens
		}
	}
	return 0
}

// maybeCompact runs between turns. It returns an error only when the session
// cannot continue.
func (m *Manager) maybeCompact(ctx context.Context, h *sessionHandle, pcfg provider.Config) error {
	if pcfg.ContextWindow <= 0 {
		return nil // unknown window: size-based compaction is disabled
	}
	log := h.s.Events()
	st := protocol.Project(log)
	used := float64(lastInputTokens(log)) / float64(pcfg.ContextWindow)

	if !st.Options.CompactionEnabled {
		if used >= m.cfg.Compaction.SummarizeAt {
			h.s.Append(protocol.EventNotice, protocol.NoticeData{Source: "daemon", Level: "error",
				Message: fmt.Sprintf("context limit reached (%.0f%% of %d tokens) and compaction is disabled for this session",
					used*100, pcfg.ContextWindow)})
			return m.finish(ctx, h, protocol.StateBlocked, "context limit with compaction disabled")
		}
		return nil
	}
	if used >= m.cfg.Compaction.SummarizeAt {
		return m.summarize(ctx, h)
	}
	if used >= m.cfg.Compaction.ClearAt {
		return m.clearResults(ctx, h)
	}
	return nil
}

// clearResults is stage 0: stub out tool results older than KeepTurns
// assistant turns. The log keeps everything; only the request shrinks.
func (m *Manager) clearResults(ctx context.Context, h *sessionHandle) error {
	log := h.s.Events()
	cutoff := keepFrom(log, m.cfg.Compaction.KeepTurns)
	start, end := "", ""
	for i, e := range log {
		if i >= cutoff {
			break
		}
		if e.Type != protocol.EventToolResult || clearedAlready(log, e.ID) {
			continue
		}
		if start == "" {
			start = e.ID
		}
		end = e.ID
	}
	if start == "" {
		return nil // nothing left to clear
	}
	_, err := h.s.Append(protocol.EventCompaction, protocol.CompactionData{
		Mode: protocol.CompactionClearResults, RangeStart: start, RangeEnd: end})
	return err
}

// keepFrom returns the log index at which the last n assistant turns begin.
// Only stage 0 uses it: stage 1 summarizes everything, for the reason given
// on summarize below.
func keepFrom(log []protocol.Event, n int) int {
	if n < 1 {
		n = 1
	}
	seen := 0
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Type != protocol.EventMessage {
			continue
		}
		if protocol.MustData[protocol.MessageData](log[i]).Role != "assistant" {
			continue
		}
		seen++
		if seen == n {
			return i
		}
	}
	return 0
}

// clearedAlready reports whether an event id already falls in a clear_results
// range, so repeated stage-0 runs do not re-clear the same events.
func clearedAlready(log []protocol.Event, id string) bool {
	for _, e := range log {
		if e.Type != protocol.EventCompaction {
			continue
		}
		d := protocol.MustData[protocol.CompactionData](e)
		if d.Mode == protocol.CompactionClearResults && d.RangeStart <= id && id <= d.RangeEnd {
			return true
		}
	}
	return false
}

// summarize is stage 1: one model call compresses the conversation into a
// summary event, then modules re-inject their prefixes.
//
// The range must run to the last event before the compaction record, not to
// some earlier "keep the recent turns" cutoff. Request assembly (protocol spec
// §6) takes messages after the last summarize compaction *by log position*, so
// anything the range left out would be dropped from the request without ever
// reaching the summary. Everything up to the compaction point is summarized;
// the Current state block carries goal, tasks and budget across losslessly.
func (m *Manager) summarize(ctx context.Context, h *sessionHandle) error {
	log := h.s.Events()
	st := protocol.Project(log)
	start := 1 // never the session event
	if st.CompactedThrough != "" {
		for i, e := range log {
			if e.ID == st.CompactedThrough {
				start = i + 1
				break
			}
		}
	}
	if !enoughToSummarise(log, st) {
		return nil // not enough history to be worth a model call
	}
	rng := module.Range{Start: log[start].ID, End: log[len(log)-1].ID}
	preserve := m.deps.Modules.BeforeCompaction(ctx, h, rng)

	transcript := renderForSummary(log[start:])
	sys := summarizePrompt
	if len(preserve) > 0 {
		sys += "\n\nThe summary MUST preserve these facts verbatim:\n- " + strings.Join(preserve, "\n- ")
	}
	p, modelName, _, err := m.deps.Providers.Resolve(st.Options.Model)
	if err != nil {
		return err
	}
	resp, err := p.Complete(ctx, provider.Request{
		Model: modelName,
		Messages: []provider.Message{
			{Role: "system", Content: sys},
			{Role: "user", Content: transcript},
		},
	}, nil, nil)
	if err != nil {
		// A failed summary is not fatal: fall back to clearing results.
		h.s.Append(protocol.EventNotice, protocol.NoticeData{Source: "daemon", Level: "warn",
			Message: "summarization failed (" + err.Error() + "); clearing tool results instead"})
		return m.clearResults(ctx, h)
	}
	summary := strings.TrimSpace(resp.Content)
	if summary == "" {
		summary = "(the summarizer returned nothing)"
	}
	if _, err := h.s.Append(protocol.EventCompaction, protocol.CompactionData{
		Mode: protocol.CompactionSummarize, RangeStart: rng.Start, RangeEnd: rng.End, Summary: summary}); err != nil {
		return err
	}
	m.appendContexts(h, m.deps.Modules.AfterCompaction(ctx, h))
	return nil
}

// renderForSummary turns events into plain text for the summarizer.
func renderForSummary(events []protocol.Event) string {
	var b strings.Builder
	for _, e := range events {
		switch e.Type {
		case protocol.EventMessage:
			d := protocol.MustData[protocol.MessageData](e)
			if strings.TrimSpace(d.Content) == "" {
				continue
			}
			fmt.Fprintf(&b, "%s: %s\n\n", d.Role, d.Content)
		case protocol.EventToolCall:
			d := protocol.MustData[protocol.ToolCallData](e)
			fmt.Fprintf(&b, "tool call %s(%s)\n", d.Tool, truncateText(string(d.Arguments), 400))
		case protocol.EventToolResult:
			d := protocol.MustData[protocol.ToolResultData](e)
			fmt.Fprintf(&b, "tool result [%s]: %s\n\n", d.Status, truncateText(d.Content, 1200))
		case protocol.EventCheck:
			d := protocol.MustData[protocol.CheckData](e)
			fmt.Fprintf(&b, "check %s: %s (%s)\n\n", d.Name, d.Status, d.Summary)
		}
	}
	return b.String()
}

func truncateText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf(" …(%d more bytes)", len(s)-max)
}

// ErrNothingToCompact is returned when a session holds too little history for a
// summary to be worth a model call.
//
// maybeCompact treats that case as "not yet" and says nothing, which is right
// for something that runs on its own between turns. A person who asked for it
// deserves an answer: a button that appears to do nothing is indistinguishable
// from one that is broken.
var ErrNothingToCompact = errors.New("nothing to compact yet: the session has too little history to summarise")

// Compact summarises a session's history on request, rather than waiting for it
// to cross the automatic threshold.
//
// Refused while the session is running. Compaction rewrites what the next
// request is assembled from, and doing that under a turn already in flight
// would change the ground beneath it. Interrupt first, then compact — which is
// why the two arrived together.
//
// Allowed when compaction_enabled is false. That option turns off the automatic
// pass; asking explicitly is the owner overriding their own default, not
// working around it.
func (m *Manager) Compact(ctx context.Context, id string) (protocol.Event, error) {
	h, err := m.handle(id)
	if err != nil {
		return protocol.Event{}, err
	}

	st := h.State()
	switch st.State {
	case protocol.StateRunning:
		return protocol.Event{}, protocol.NewRPCError(protocol.CodeInvalidTransition,
			"session is running; interrupt it before compacting")
	case protocol.StateCompleted, protocol.StateError:
		return protocol.Event{}, protocol.NewRPCError(protocol.CodeInvalidTransition,
			fmt.Sprintf("session has ended (%s); there is nothing further to compact", st.State))
	}

	if !enoughToSummarise(h.s.Events(), st) {
		return protocol.Event{}, protocol.NewRPCError(protocol.CodeInvalidParams,
			ErrNothingToCompact.Error())
	}

	before := len(h.s.Events())
	if err := m.summarize(ctx, h); err != nil {
		return protocol.Event{}, err
	}

	// summarize appends the compaction itself, and falls back to clearing tool
	// results when the summariser fails. Report whichever actually landed
	// rather than the one that was asked for.
	log := h.s.Events()
	for i := len(log) - 1; i >= before; i-- {
		if log[i].Type == protocol.EventCompaction {
			return log[i], nil
		}
	}
	return protocol.Event{}, fmt.Errorf("compaction produced no event")
}

// enoughToSummarise is summarize's own threshold, asked in advance so a request
// that would do nothing is refused instead of silently succeeding.
func enoughToSummarise(log []protocol.Event, st protocol.State) bool {
	start := 1 // never the session event
	if st.CompactedThrough != "" {
		for i, e := range log {
			if e.ID == st.CompactedThrough {
				start = i + 1
				break
			}
		}
	}
	return len(log)-start >= 2
}
