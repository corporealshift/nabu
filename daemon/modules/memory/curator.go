package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// Window bounds. A pass is a model call, so what it sees has to be affordable
// on every session end and every compaction. A pass that cannot see everything
// is worth more than one too expensive to make.
const (
	maxWindowEvents = 200
	maxWindowBytes  = 24 << 10
	// maxResultChars is how much of a tool result reaches the window. Results
	// are the largest thing in a log and rarely the thing worth remembering.
	maxResultChars = 400
)

// curatorWindow is the slice of a session the curator is asked about.
type curatorWindow struct {
	events []protocol.Event
	// last is the id of the final event, which becomes the next cursor.
	last string
}

// window builds the window of events after cursor. An empty cursor takes the
// whole log, which is where a daemon restart lands.
func window(events []protocol.Event, cursor string) curatorWindow {
	var kept []protocol.Event
	past := cursor == ""
	for _, e := range events {
		if !past {
			if e.ID == cursor {
				past = true
			}
			continue
		}
		if e.Type == protocol.EventContext {
			continue // what this module injected; feeding it back is a loop
		}
		kept = append(kept, e)
	}

	w := curatorWindow{}
	if len(kept) == 0 {
		return w
	}
	w.last = kept[len(kept)-1].ID
	if len(kept) > maxWindowEvents {
		kept = kept[len(kept)-maxWindowEvents:]
	}
	w.events = kept
	return w
}

// worthAPass reports whether anything happened that a pass could learn from. A
// pass costs a model call, and a session where the model only talked to itself
// has nothing to teach.
func (w curatorWindow) worthAPass() bool {
	for _, e := range w.events {
		switch e.Type {
		case protocol.EventToolCall:
			return true
		case protocol.EventMessage:
			if d, err := decodeEvent[protocol.MessageData](e); err == nil && d.Role == "user" {
				return true
			}
		}
	}
	return false
}

// render writes the window as a transcript, newest last, within the byte cap.
// The cap drops the oldest lines, because the most recent turn is the one most
// likely to hold what was learned.
func (w curatorWindow) render() string {
	lines := make([]string, 0, len(w.events))
	for _, e := range w.events {
		if line := renderEvent(e); line != "" {
			lines = append(lines, line)
		}
	}

	// Drop from the front until it fits.
	total := 0
	for _, l := range lines {
		total += len(l) + 1
	}
	for total > maxWindowBytes && len(lines) > 1 {
		total -= len(lines[0]) + 1
		lines = lines[1:]
	}
	out := strings.Join(lines, "\n")
	if len(out) > maxWindowBytes {
		out = out[len(out)-maxWindowBytes:]
	}
	return out
}

// renderEvent is one transcript line, or empty for an event the curator has no
// use for.
func renderEvent(e protocol.Event) string {
	switch e.Type {
	case protocol.EventMessage:
		d, err := decodeEvent[protocol.MessageData](e)
		if err != nil || strings.TrimSpace(d.Content) == "" {
			return ""
		}
		return d.Role + ": " + strings.TrimSpace(d.Content)

	case protocol.EventToolCall:
		d, err := decodeEvent[protocol.ToolCallData](e)
		if err != nil {
			return ""
		}
		return "tool " + d.Tool + " " + clip(string(d.Arguments), maxResultChars)

	case protocol.EventToolResult:
		d, err := decodeEvent[protocol.ToolResultData](e)
		if err != nil {
			return ""
		}
		return "result " + d.Tool + ": " + clip(strings.TrimSpace(d.Content), maxResultChars)

	case protocol.EventNotice:
		d, err := decodeEvent[protocol.NoticeData](e)
		if err != nil {
			return ""
		}
		return "notice: " + d.Message

	case protocol.EventStopVeto:
		d, err := decodeEvent[protocol.StopVetoData](e)
		if err != nil {
			return ""
		}
		return "veto from " + d.Module + ": " + d.Reason

	case protocol.EventGoal:
		d, err := decodeEvent[protocol.GoalData](e)
		if err != nil {
			return ""
		}
		return fmt.Sprintf("goal %s: %s", d.State, d.Condition)
	}
	return ""
}

// decodeEvent decodes one event's data. It never panics: a malformed event in
// a log the curator is only reading should cost that line, not the pass.
func decodeEvent[T any](e protocol.Event) (T, error) {
	var out T
	err := json.Unmarshal(e.Data, &out)
	return out, err
}

// clip shortens text and says that it did, so the curator never mistakes a
// truncated result for the whole of one.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + " …[truncated]"
}

// curatorSystem frames the pass. The curator is told to find nothing when
// there is nothing, because a model asked to produce memories will produce
// them, and a memory of something unremarkable is worse than no memory.
const curatorSystem = "You are curating the memory of a coding agent. You are shown a " +
	"transcript and the memories that already exist. Decide what, if anything, a future " +
	"session on this project would act on differently. Most sessions teach nothing: " +
	"replying with an empty list is the common and correct answer.\n\n" +
	"Reply with JSON only: an array of objects with scope, name, type, description and " +
	"body. scope is \"global\" for facts about the owner and how they work, or " +
	"\"workspace\" for this project. type is one of user, feedback, project, reference. " +
	"name is kebab-case and names the subject. To correct something already remembered, " +
	"reuse its exact name and the old one is replaced."

// proposal is one memory the curator wants written. It is deliberately the
// same shape memory.save takes, because that is how it gets written.
type proposal struct {
	Scope       string `json:"scope"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

// curatorPrompt is the user message: what is already known, then what happened.
func curatorPrompt(existing []Memory, w curatorWindow) string {
	var b strings.Builder
	b.WriteString(SaveInstructions + "\n\n")

	b.WriteString("## Already remembered\n\n")
	if len(existing) == 0 {
		b.WriteString("Nothing yet.\n")
	}
	for _, m := range existing {
		b.WriteString("- " + m.Name)
		if m.Description != "" {
			b.WriteString(": " + m.Description)
		}
		b.WriteString("\n")
	}

	b.WriteString("\n## What happened\n\n")
	b.WriteString(w.render())
	b.WriteString("\n")
	return b.String()
}

// parseProposals reads the model's reply. A model that wraps JSON in prose or
// a code fence is being helpful rather than wrong, so the array is located
// rather than demanded.
//
// Anything unusable is dropped rather than corrected: a guess at what the
// model meant is a memory nobody chose.
func parseProposals(reply string, limit int) []proposal {
	raw := jsonArray(reply)
	if raw == "" {
		return nil
	}
	var all []proposal
	if err := json.Unmarshal([]byte(raw), &all); err != nil {
		return nil
	}

	var out []proposal
	for _, p := range all {
		p.Name = strings.TrimSpace(p.Name)
		p.Body = strings.TrimSpace(p.Body)
		p.Description = strings.TrimSpace(p.Description)
		p.Type = strings.TrimSpace(p.Type)
		switch Scope(strings.TrimSpace(p.Scope)) {
		case ScopeGlobal, ScopeWorkspace:
		default:
			continue
		}
		if p.Name == "" || p.Body == "" || !validTypes[p.Type] {
			continue
		}
		out = append(out, p)
		if limit > 0 && len(out) == limit {
			break // finding too much is not a reason to keep none of it
		}
	}
	return out
}

// jsonArray finds the outermost [...] in a reply.
func jsonArray(s string) string {
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start < 0 || end < start {
		return ""
	}
	return s[start : end+1]
}

// curate runs one pass: decide whether it is worth asking, ask, and write what
// comes back through the tool API.
//
// Nothing here can fail a session. Memory filling itself is a convenience on
// top of a session, not a step in it, so every failure is logged and the pass
// simply ends.
func (m *Module) curate(ctx context.Context, s module.Session) {
	if !m.enabled || !m.Curator || s == nil || m.host == nil {
		return
	}
	if m.host.Model() == nil || m.host.Tools() == nil {
		return // nothing to ask, or no way to write what it says
	}

	events, err := s.Events(nil)
	if err != nil {
		m.log.Warn("memory: cannot read the session log", "error", err)
		return
	}
	w := window(events, m.cursorFor(s.ID()))
	if !w.worthAPass() {
		return
	}

	existing := append(m.globalCorpus(), m.workspaceCorpus(s)...)
	resp, err := m.host.Model().Complete(ctx, s, module.CompletionRequest{
		Model:     m.CuratorModel,
		System:    curatorSystem,
		Messages:  []module.Message{{Role: "user", Content: curatorPrompt(existing, w)}},
		MaxTokens: 1200,
	})
	// The cursor advances either way. A pass that failed still saw these
	// events, and retrying them doubles the cost of a question already asked.
	m.setCursor(s.ID(), w.last)
	if err != nil {
		m.log.Warn("memory: the curator pass failed", "error", err)
		return
	}

	for _, p := range parseProposals(resp.Content, m.CuratorMaxPerPass) {
		m.write(ctx, s, p)
	}

	// Writing is what pushes the index over its cap, so this is the moment to
	// check. It costs nothing while the index still fits.
	m.consolidate(ctx, s)
}

// write saves one proposal through the host's tool API. Spec 11.4 requires it:
// a curator that writes files directly is an invisible actor, and going
// through the tool makes every write a logged, gateable, reversible call.
func (m *Module) write(ctx context.Context, s module.Session, p proposal) {
	args, err := json.Marshal(p)
	if err != nil {
		return
	}
	if _, err := m.host.Tools().Call(ctx, s, "memory.save", args); err != nil {
		// One refused write is not a reason to abandon the others.
		m.log.Warn("memory: the curator could not save", "name", p.Name, "error", err)
	}
}

// cursorFor is the last event a pass saw for this session.
func (m *Module) cursorFor(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cursors[id]
}

func (m *Module) setCursor(id, last string) {
	if last == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cursors == nil {
		m.cursors = map[string]string{}
	}
	m.cursors[id] = last
}
