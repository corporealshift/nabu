// Package stats measures how much work a session was, from its log alone
// (issue 38).
//
// Everything here is derived: the log is the state (invariant 1), so there is
// nothing to store and nothing to drift. It is not normative the way the
// projection in protocol is, so the clients do not recompute it; they ask.
package stats

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

// Session is one session's numbers.
type Session struct {
	SessionID string `json:"session_id"`
	// Turns are model round trips: one per assistant message.
	Turns int `json:"turns"`
	// Prompts are what the person said.
	Prompts int `json:"prompts"`

	Tokens Tokens `json:"tokens"`
	// PerTurn is each turn's usage in order, for a chart of how the context
	// grew and where compaction cut it back.
	PerTurn []Turn `json:"per_turn"`

	Tools []Tool `json:"tools"`

	Compactions   Compactions `json:"compactions"`
	Vetoes        int         `json:"vetoes"`
	Interruptions int         `json:"interruptions"`

	StartedAt   time.Time `json:"started_at"`
	LastEventAt time.Time `json:"last_event_at"`
	// WorkingSeconds is time spent running: what the agent was busy for, not
	// how long the session has existed. A silence longer than maxGap while
	// running counts as maxGap: it is a daemon that stopped with the session
	// still marked running, not work.
	WorkingSeconds int64 `json:"working_seconds"`

	// Rereads are files read three times or more. A first signal of an agent
	// going round in circles rather than a verdict: sometimes a file is read
	// again because it changed.
	Rereads []Reread `json:"rereads"`
}

// Tokens are totals across the session.
type Tokens struct {
	// Input is every request's prompt, summed. Each request carries the whole
	// conversation again, so this is what was processed, not how big the
	// conversation is; PeakContext says that.
	Input       int `json:"input"`
	Output      int `json:"output"`
	Cached      int `json:"cached"`
	PeakContext int `json:"peak_context"`
	// ContextWindow is the model's window when known, else 0.
	ContextWindow int `json:"context_window"`
}

// Turn is one model round trip.
type Turn struct {
	At     time.Time `json:"at"`
	Input  int       `json:"input"`
	Output int       `json:"output"`
}

// Tool is how one tool was used.
type Tool struct {
	Name   string `json:"tool"`
	Calls  int    `json:"calls"`
	Errors int    `json:"errors"`
}

// Compactions counts each stage.
type Compactions struct {
	Summarize    int `json:"summarize"`
	ClearResults int `json:"clear_results"`
}

// Reread is a file read more than it probably needed to be.
type Reread struct {
	Path  string `json:"path"`
	Reads int    `json:"reads"`
}

// maxGap is the most one quiet stretch counts toward working time. A local
// model's turn takes minutes, not hours.
const maxGap = 15 * time.Minute

// rereadAt is how many reads of one file make it worth pointing at.
const rereadAt = 3

// Of measures a session's log.
func Of(sessionID string, log []protocol.Event) Session {
	s := Session{SessionID: sessionID, PerTurn: []Turn{}, Tools: []Tool{}, Rereads: []Reread{}}
	if len(log) == 0 {
		return s
	}
	s.StartedAt, s.LastEventAt = log[0].Timestamp, log[len(log)-1].Timestamp

	tools := map[string]*Tool{}
	callTool := map[string]string{}
	reads := map[string]int{}
	running := false
	var last time.Time // the previous event, while running

	for _, e := range log {
		if running {
			s.WorkingSeconds += int64(min(e.Timestamp.Sub(last), maxGap) / time.Second)
			last = e.Timestamp
		}
		switch e.Type {
		case protocol.EventSession:
			var d protocol.SessionData
			if json.Unmarshal(e.Data, &d) == nil {
				s.Tokens.ContextWindow = d.ContextWindow
			}
		case protocol.EventMessage:
			var d protocol.MessageData
			if json.Unmarshal(e.Data, &d) != nil {
				continue
			}
			if d.Role == "user" {
				s.Prompts++
				continue
			}
			s.Turns++
			if d.Interrupted {
				s.Interruptions++
			}
			if u := d.Usage; u != nil {
				s.Tokens.Input += u.InputTokens
				s.Tokens.Output += u.OutputTokens
				s.Tokens.Cached += u.CachedTokens
				s.Tokens.PeakContext = max(s.Tokens.PeakContext, u.InputTokens)
				s.PerTurn = append(s.PerTurn, Turn{At: e.Timestamp, Input: u.InputTokens, Output: u.OutputTokens})
			}
		case protocol.EventToolCall:
			var d protocol.ToolCallData
			if json.Unmarshal(e.Data, &d) != nil {
				continue
			}
			t := tools[d.Tool]
			if t == nil {
				t = &Tool{Name: d.Tool}
				tools[d.Tool] = t
			}
			t.Calls++
			callTool[d.CallID] = d.Tool
			if d.Tool == "read" {
				var a struct {
					Path string `json:"path"`
				}
				if json.Unmarshal(d.Arguments, &a) == nil && a.Path != "" {
					reads[a.Path]++
				}
			}
		case protocol.EventToolResult:
			var d protocol.ToolResultData
			if json.Unmarshal(e.Data, &d) != nil || d.Status == "ok" {
				continue
			}
			if t := tools[callTool[d.CallID]]; t != nil {
				t.Errors++
			}
		case protocol.EventCompaction:
			var d protocol.CompactionData
			if json.Unmarshal(e.Data, &d) != nil {
				continue
			}
			switch d.Mode {
			case protocol.CompactionSummarize:
				s.Compactions.Summarize++
			case protocol.CompactionClearResults:
				s.Compactions.ClearResults++
			}
		case protocol.EventStopVeto:
			s.Vetoes++
		case protocol.EventStateChange:
			var d protocol.StateChangeData
			if json.Unmarshal(e.Data, &d) != nil {
				continue
			}
			if d.To == protocol.StateRunning && !running {
				running, last = true, e.Timestamp
			} else if d.To != protocol.StateRunning {
				running = false
			}
		}
	}

	for _, t := range tools {
		s.Tools = append(s.Tools, *t)
	}
	sort.Slice(s.Tools, func(i, j int) bool {
		if s.Tools[i].Calls != s.Tools[j].Calls {
			return s.Tools[i].Calls > s.Tools[j].Calls
		}
		return s.Tools[i].Name < s.Tools[j].Name
	})
	for p, n := range reads {
		if n >= rereadAt {
			s.Rereads = append(s.Rereads, Reread{Path: p, Reads: n})
		}
	}
	sort.Slice(s.Rereads, func(i, j int) bool {
		if s.Rereads[i].Reads != s.Rereads[j].Reads {
			return s.Rereads[i].Reads > s.Rereads[j].Reads
		}
		return s.Rereads[i].Path < s.Rereads[j].Path
	})
	return s
}

// Day is one calendar day's usage across every session.
type Day struct {
	Date   string `json:"date"` // YYYY-MM-DD in the daemon's zone
	Turns  int    `json:"turns"`
	Input  int    `json:"input"`
	Output int    `json:"output"`
}

// Usage totals turns and tokens per day across logs, for the days from
// first through last inclusive. Every day in the range is present, idle ones
// as zero: a gap in a chart should mean a quiet day, not a missing one.
func Usage(logs [][]protocol.Event, first, last time.Time, loc *time.Location) []Day {
	key := func(t time.Time) string { return t.In(loc).Format("2006-01-02") }
	byDay := map[string]*Day{}
	var out []Day
	for d := first.In(loc); !d.After(last.In(loc)); d = d.AddDate(0, 0, 1) {
		out = append(out, Day{Date: key(d)})
	}
	for i := range out {
		byDay[out[i].Date] = &out[i]
	}
	for _, log := range logs {
		for _, e := range log {
			if e.Type != protocol.EventMessage {
				continue
			}
			day := byDay[key(e.Timestamp)]
			if day == nil {
				continue
			}
			var d protocol.MessageData
			if json.Unmarshal(e.Data, &d) != nil || d.Role != "assistant" {
				continue
			}
			day.Turns++
			if d.Usage != nil {
				day.Input += d.Usage.InputTokens
				day.Output += d.Usage.OutputTokens
			}
		}
	}
	return out
}
