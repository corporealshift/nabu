package stats

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

// The period under test is 2026-09-20 and 2026-09-21, UTC.
var (
	from = time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	to   = from.AddDate(0, 0, 2)
)

// at builds an event at an hour offset from the period's start; negative is
// before it.
func at(hours float64, typ protocol.EventType, data any) protocol.Event {
	raw, _ := json.Marshal(data)
	return protocol.Event{Type: typ, Timestamp: from.Add(time.Duration(hours * float64(time.Hour))), Data: raw}
}

func opened(hours float64, description string, labels ...string) protocol.Event {
	return at(hours, protocol.EventSession, protocol.SessionData{Workspace: "/w",
		Options: protocol.Options{Labels: labels, Description: description}})
}

func said(hours float64, content string) protocol.Event {
	return at(hours, protocol.EventMessage, protocol.MessageData{Role: "user", Content: content})
}

func turn(hours float64, in, out int) protocol.Event {
	return at(hours, protocol.EventMessage, protocol.MessageData{Role: "assistant",
		Usage: &protocol.Usage{InputTokens: in, OutputTokens: out}})
}

func called(hours float64, id, tool string, args any) protocol.Event {
	raw, _ := json.Marshal(args)
	return at(hours, protocol.EventToolCall, protocol.ToolCallData{CallID: id, Tool: tool, Arguments: raw})
}

func answered(hours float64, id, tool, status, kind, content string) protocol.Event {
	return at(hours, protocol.EventToolResult, protocol.ToolResultData{CallID: id, Tool: tool,
		Status: status, Kind: kind, Content: content})
}

func search(q string) map[string]string { return map[string]string{"query": q} }

// interactive started before the period and kept going into it.
var interactive = Log{ID: "I", Events: []protocol.Event{
	opened(-30, ""),
	said(-30, "old question\nwith a second line"),
	turn(-29, 900, 90),
	called(-29, "c0", "web.search", search("before the period")),
	answered(-29, "c0", "web.search", "ok", "", "old"),
	said(2, "is createFromFile a copy?\nand more"),
	turn(3, 100, 10),
	called(3, "c1", "web.search", search("createFromFile copies")),
	answered(3, "c1", "web.search", "ok", "", "it copies the file"),
	called(4, "c2", "read", map[string]string{"path": "a.go"}),
	answered(4, "c2", "read", "error", protocol.ToolErrorNotFound, "no such file"),
}}

// run is a run's step, wholly inside the period.
var run = Log{ID: "R", Archived: true, Events: []protocol.Event{
	opened(25, "Room database and DAOs\nthe brief goes on", protocol.LabelUnattended),
	said(25, "do the task"),
	turn(26, 200, 20),
	called(26, "r1", "web.search", search("room 2.6.1")),
	answered(26, "r1", "web.search", "ok", "", strings.Repeat("é", ResultChars+5)),
	turn(27, 300, 30),
	called(30, "r2", "web.search", search("no answer yet")),
}}

// quiet had nothing in the period.
var quiet = Log{ID: "Q", Events: []protocol.Event{
	opened(-40, ""),
	said(-40, "long ago"),
	turn(-39, 50, 5),
}}

var logs = []Log{interactive, run, quiet}

func TestTotalsSumOfOverThePeriod(t *testing.T) {
	cases := []struct {
		kind Kind
		want []Log // the logs whose cuts should be summed
	}{
		{KindAll, []Log{interactive, run}},
		{KindInteractive, []Log{interactive}},
		{KindUnattended, []Log{run}},
	}
	for _, c := range cases {
		t.Run(string(c.kind), func(t *testing.T) {
			w := Totals(logs, from, to, time.UTC, c.kind)

			var turns, prompts, in, out int
			for _, l := range c.want {
				s := Of(l.ID, cut(l.Events, from, to))
				turns, prompts, in, out = turns+s.Turns, prompts+s.Prompts, in+s.Tokens.Input, out+s.Tokens.Output
			}
			if w.Sessions != len(c.want) || w.Turns != turns || w.Prompts != prompts ||
				w.Tokens.Input != in || w.Tokens.Output != out {
				t.Errorf("got sessions %d turns %d prompts %d tokens %d/%d, want %d %d %d %d/%d",
					w.Sessions, w.Turns, w.Prompts, w.Tokens.Input, w.Tokens.Output,
					len(c.want), turns, prompts, in, out)
			}
			if len(w.PerDay) != 2 {
				t.Errorf("per day = %+v, want both days", w.PerDay)
			}
		})
	}
}

func TestTotalsTools(t *testing.T) {
	w := Totals(logs, from, to, time.UTC, KindAll)
	want := []WindowTool{
		// c0 was before the period; c1, r1 and r2 are inside it.
		{Name: "web.search", Calls: 3, Errors: 0, Sessions: 2},
		{Name: "read", Calls: 1, Errors: 1, Sessions: 1},
	}
	if !reflect.DeepEqual(w.Tools, want) {
		t.Errorf("tools = %+v, want %+v", w.Tools, want)
	}
	// The interactive session's first day had one turn, the run's second two.
	if w.PerDay[0].Turns != 1 || w.PerDay[1].Turns != 2 {
		t.Errorf("per day = %+v", w.PerDay)
	}
}

func TestTotalsOfNothing(t *testing.T) {
	w := Totals(nil, from, to, time.UTC, KindAll)
	if w.Sessions != 0 || w.Tools == nil || len(w.PerDay) != 2 {
		t.Errorf("empty window = %+v", w)
	}
}

func TestCalls(t *testing.T) {
	period := CallQuery{Tool: "web.search", From: from, To: to, Kind: KindAll}
	type row struct {
		session, status, kind string
		archived              bool
	}
	cases := []struct {
		name      string
		q         CallQuery
		want      []row
		truncated bool
	}{
		{"newest first within the period", period,
			[]row{{"R", "pending", "", true}, {"R", "ok", "", true}, {"I", "ok", "", false}}, false},
		{"runs only", with(period, func(q *CallQuery) { q.Kind = KindUnattended }),
			[]row{{"R", "pending", "", true}, {"R", "ok", "", true}}, false},
		{"interactive only", with(period, func(q *CallQuery) { q.Kind = KindInteractive }),
			[]row{{"I", "ok", "", false}}, false},
		{"a limit", with(period, func(q *CallQuery) { q.Limit = 2 }),
			[]row{{"R", "pending", "", true}, {"R", "ok", "", true}}, true},
		{"a limit nothing reaches", with(period, func(q *CallQuery) { q.Limit = 3 }),
			[]row{{"R", "pending", "", true}, {"R", "ok", "", true}, {"I", "ok", "", false}}, false},
		{"one session ignores the period and kind",
			with(period, func(q *CallQuery) { q.SessionID = "I"; q.Kind = KindUnattended }),
			[]row{{"I", "ok", "", false}, {"I", "ok", "", false}}, false},
		{"errors carry their kind", with(period, func(q *CallQuery) { q.Tool = "read" }),
			[]row{{"I", "error", protocol.ToolErrorNotFound, false}}, false},
		{"a tool nobody called", with(period, func(q *CallQuery) { q.Tool = "claude.ask" }),
			[]row{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			calls, truncated := Calls(logs, c.q)
			got := []row{}
			for _, call := range calls {
				got = append(got, row{call.SessionID, call.Status, call.Kind, call.Archived})
			}
			if !reflect.DeepEqual(got, c.want) || truncated != c.truncated {
				t.Errorf("got %+v truncated %v, want %+v truncated %v", got, truncated, c.want, c.truncated)
			}
		})
	}
}

func TestCallDetails(t *testing.T) {
	calls, _ := Calls(logs, CallQuery{Tool: "web.search", From: from, To: to, Kind: KindAll})
	pending, long, short := calls[0], calls[1], calls[2]

	if pending.Label != "Room database and DAOs" {
		t.Errorf("a run is named by its description's first line, got %q", pending.Label)
	}
	if short.Label != "old question" {
		t.Errorf("a session without one is named by its first prompt's, got %q", short.Label)
	}
	if n := len([]rune(long.Result)); n != ResultChars || !strings.HasPrefix(long.Result, "é") {
		t.Errorf("result cut to %d characters, want %d whole ones", n, ResultChars)
	}
	if short.Result != "it copies the file" {
		t.Errorf("result = %q", short.Result)
	}
	if string(short.Arguments) != `{"query":"createFromFile copies"}` {
		t.Errorf("arguments = %s", short.Arguments)
	}
	if !short.At.Equal(from.Add(3 * time.Hour)) {
		t.Errorf("at = %v", short.At)
	}
}

func TestValidKind(t *testing.T) {
	for k, want := range map[Kind]bool{KindAll: true, KindInteractive: true, KindUnattended: true, "": false, "goals": false} {
		if ValidKind(k) != want {
			t.Errorf("ValidKind(%q) = %v", k, !want)
		}
	}
}

func with(q CallQuery, change func(*CallQuery)) CallQuery {
	change(&q)
	return q
}

// Logs from before the unattended label are known by what runners and GitHub
// jobs put on their sessions anyway.
func TestUnattendedFromOlderLogs(t *testing.T) {
	for name, o := range map[string]protocol.Options{
		"labelled":         {Labels: []string{protocol.LabelUnattended}},
		"a step":           {Parent: "H"},
		"a run's home":     {Labels: []string{"run:work"}},
		"a goal's home":    {Labels: []string{"goal:runs", "goal:round:2"}},
		"a runner's guard": {Labels: []string{"guard:no-push"}},
	} {
		if !unattended(o) {
			t.Errorf("%s should be unattended", name)
		}
	}
	for name, o := range map[string]protocol.Options{
		"bare":               {},
		"some other label":   {Labels: []string{"wip"}},
		"only a model":       {Model: "qwen"},
		"only a description": {Description: "notes"},
	} {
		if unattended(o) {
			t.Errorf("%s should be interactive", name)
		}
	}
}

// A step's own prompt is the same few words for every step; the run it
// belongs to is what a person recognises.
func TestAStepIsNamedByItsRun(t *testing.T) {
	home := Log{ID: "H", Events: []protocol.Event{opened(0, "# Navigation shell\n\nGive Liftoff its shell", "run:work")}}
	step := Log{ID: "S", Events: []protocol.Event{
		at(1, protocol.EventSession, protocol.SessionData{Workspace: "/w", Options: protocol.Options{Parent: "H"}}),
		said(1, "Do task 2 of 6 of this run:"),
		called(2, "s1", "web.search", search("compose nav")),
		answered(2, "s1", "web.search", "ok", "", "use NavHost"),
	}}
	orphan := Log{ID: "O", Events: []protocol.Event{
		at(1, protocol.EventSession, protocol.SessionData{Workspace: "/w", Options: protocol.Options{Parent: "GONE"}}),
		said(1, "Do task 1 of 2 of this run:"),
		called(3, "o1", "web.search", search("x")),
	}}
	calls, _ := Calls([]Log{home, step, orphan}, CallQuery{Tool: "web.search", From: from, To: to, Kind: KindAll})
	got := map[string]string{}
	for _, c := range calls {
		got[c.SessionID] = c.Label
	}
	if got["S"] != "Navigation shell" {
		t.Errorf("a step is named by its run's description, got %q", got["S"])
	}
	if got["O"] != "Do task 1 of 2 of this run:" {
		t.Errorf("a step whose run is gone falls back to its prompt, got %q", got["O"])
	}
}
