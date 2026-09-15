package memory

import (
	"encoding/json"
	"fmt"
	"strings"

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
