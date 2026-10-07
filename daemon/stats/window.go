package stats

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

// Log is one session's events, live or archived, with what is needed to say
// whose they were.
type Log struct {
	ID       string
	Events   []protocol.Event
	Archived bool
}

// Kind narrows which sessions count.
type Kind string

const (
	KindAll         Kind = "all"
	KindInteractive Kind = "interactive"
	// KindUnattended is work no person was watching: runs, goals and their
	// steps, GitHub jobs, headless `nabu run`. See unattended.
	KindUnattended Kind = "unattended"
)

// ValidKind reports whether k is a kind a caller may ask for.
func ValidKind(k Kind) bool {
	return k == KindAll || k == KindInteractive || k == KindUnattended
}

// Window is the work done across sessions over a stretch of time.
type Window struct {
	Sessions       int          `json:"sessions"`
	Turns          int          `json:"turns"`
	Prompts        int          `json:"prompts"`
	Tokens         WindowTokens `json:"tokens"`
	Tools          []WindowTool `json:"tools"`
	Compactions    Compactions  `json:"compactions"`
	Vetoes         int          `json:"vetoes"`
	Interruptions  int          `json:"interruptions"`
	WorkingSeconds int64        `json:"working_seconds"`
	PerDay         []Day        `json:"per_day"`
}

// WindowTokens are token totals. There is no peak or window here: those
// describe one conversation, and summed across many they mean nothing.
type WindowTokens struct {
	Input  int `json:"input"`
	Output int `json:"output"`
	Cached int `json:"cached"`
}

// WindowTool is how one tool was used across sessions.
type WindowTool struct {
	Name     string `json:"tool"`
	Calls    int    `json:"calls"`
	Errors   int    `json:"errors"`
	Sessions int    `json:"sessions"`
}

// Totals measures the logs of the given kind over [from, to). Each log is cut
// to the period and measured by Of, and the results are added up, so the
// numbers count exactly as they do for one session. A session with nothing in
// the period is not counted at all. A session already running when the period
// began counts its working time from its first state change inside it.
func Totals(logs []Log, from, to time.Time, loc *time.Location, kind Kind) Window {
	w := Window{Tools: []WindowTool{}}
	tools := map[string]*WindowTool{}
	var cuts [][]protocol.Event
	for _, l := range logs {
		if !isKind(l.Events, kind) {
			continue
		}
		events := cut(l.Events, from, to)
		if len(events) == 0 {
			continue
		}
		cuts = append(cuts, events)
		s := Of(l.ID, events)
		w.Sessions++
		w.Turns += s.Turns
		w.Prompts += s.Prompts
		w.Tokens.Input += s.Tokens.Input
		w.Tokens.Output += s.Tokens.Output
		w.Tokens.Cached += s.Tokens.Cached
		w.Compactions.Summarize += s.Compactions.Summarize
		w.Compactions.ClearResults += s.Compactions.ClearResults
		w.Vetoes += s.Vetoes
		w.Interruptions += s.Interruptions
		w.WorkingSeconds += s.WorkingSeconds
		for _, t := range s.Tools {
			wt := tools[t.Name]
			if wt == nil {
				wt = &WindowTool{Name: t.Name}
				tools[t.Name] = wt
			}
			wt.Calls += t.Calls
			wt.Errors += t.Errors
			wt.Sessions++
		}
	}
	for _, t := range tools {
		w.Tools = append(w.Tools, *t)
	}
	sort.Slice(w.Tools, func(i, j int) bool {
		if w.Tools[i].Calls != w.Tools[j].Calls {
			return w.Tools[i].Calls > w.Tools[j].Calls
		}
		return w.Tools[i].Name < w.Tools[j].Name
	})
	w.PerDay = Usage(cuts, from, to.Add(-time.Nanosecond), loc)
	return w
}

// Call is one tool call and what came back.
type Call struct {
	SessionID string          `json:"session_id"`
	Label     string          `json:"label"`
	At        time.Time       `json:"at"`
	Arguments json.RawMessage `json:"arguments"`
	// Status is the result's, or "pending" when there is none yet.
	Status string `json:"status"`
	Kind   string `json:"kind"`
	// Result is the start of the result's content.
	Result   string `json:"result"`
	Archived bool   `json:"archived"`
}

// CallQuery says which calls to list.
type CallQuery struct {
	Tool     string
	From, To time.Time
	Kind     Kind
	// SessionID, when set, lists that session's calls whatever the period
	// and kind.
	SessionID string
	// Limit caps how many come back; zero is no cap.
	Limit int
}

// ResultChars is how much of a result a call carries.
const ResultChars = 400

// labelChars is how much of a description or prompt names a session.
const labelChars = 80

// Calls lists one tool's calls, newest first, and whether more matched than
// the limit let through.
func Calls(logs []Log, q CallQuery) ([]Call, bool) {
	out := []Call{}
	byID := make(map[string]Log, len(logs))
	for _, l := range logs {
		byID[l.ID] = l
	}
	for _, l := range logs {
		if q.SessionID != "" {
			if l.ID != q.SessionID {
				continue
			}
		} else if !isKind(l.Events, q.Kind) {
			continue
		}
		results := map[string]protocol.ToolResultData{}
		for _, e := range l.Events {
			var d protocol.ToolResultData
			if e.Type == protocol.EventToolResult && json.Unmarshal(e.Data, &d) == nil {
				results[d.CallID] = d
			}
		}
		label := ""
		for _, e := range l.Events {
			if e.Type != protocol.EventToolCall {
				continue
			}
			if q.SessionID == "" && (e.Timestamp.Before(q.From) || !e.Timestamp.Before(q.To)) {
				continue
			}
			var d protocol.ToolCallData
			if json.Unmarshal(e.Data, &d) != nil || d.Tool != q.Tool {
				continue
			}
			if label == "" {
				label = labelOf(l.Events, byID)
			}
			c := Call{SessionID: l.ID, Label: label, At: e.Timestamp, Arguments: d.Arguments,
				Status: "pending", Archived: l.Archived}
			if r, ok := results[d.CallID]; ok {
				c.Status, c.Kind, c.Result = r.Status, r.Kind, firstChars(r.Content, ResultChars)
			}
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	if q.Limit > 0 && len(out) > q.Limit {
		return out[:q.Limit], true
	}
	return out, false
}

// cut keeps the events in [from, to).
func cut(events []protocol.Event, from, to time.Time) []protocol.Event {
	var out []protocol.Event
	for _, e := range events {
		if !e.Timestamp.Before(from) && e.Timestamp.Before(to) {
			out = append(out, e)
		}
	}
	return out
}

func isKind(events []protocol.Event, kind Kind) bool {
	if kind == KindAll || kind == "" {
		return true
	}
	return unattended(protocol.Project(events).Options) == (kind == KindUnattended)
}

// unattended says no person was watching the session. The label says so for
// sessions made since it existed. Older logs are known by what only clients
// working for themselves put on a session: a parent (a run's or goal's step),
// a run: or goal: label (their homes, which never carried the label), or the
// guard:no-push every runner and GitHub job asked for.
func unattended(o protocol.Options) bool {
	if o.Parent != "" {
		return true
	}
	for _, l := range o.Labels {
		if l == protocol.LabelUnattended || l == "guard:no-push" ||
			strings.HasPrefix(l, "run:") || strings.HasPrefix(l, "goal:") {
			return true
		}
	}
	return false
}

// labelOf names a session the way a person would recognise it: its
// description's first line; else, for a step, the label of what it is a step
// of, since its own prompt is the same few words for every step; else its
// first prompt's first line.
func labelOf(events []protocol.Event, byID map[string]Log) string {
	for depth := 0; depth < 4; depth++ {
		o := protocol.Project(events).Options
		if o.Description != "" {
			return firstLine(strings.TrimLeft(strings.TrimSpace(o.Description), "# "))
		}
		parent, ok := byID[o.Parent]
		if o.Parent == "" || !ok {
			break
		}
		events = parent.Events
	}
	for _, e := range events {
		var d protocol.MessageData
		if e.Type == protocol.EventMessage && json.Unmarshal(e.Data, &d) == nil && d.Role == "user" {
			return firstLine(d.Content)
		}
	}
	return ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return firstChars(s, labelChars)
}

// firstChars cuts s to n characters, never through one.
func firstChars(s string, n int) string {
	i := 0
	for j := range s {
		if i == n {
			return s[:j]
		}
		i++
	}
	return s
}
