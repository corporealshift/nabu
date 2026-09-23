package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/corporealshift/nabu/clients/goclient"
	"github.com/corporealshift/nabu/protocol"
)

// Issue 106: each block stands on its own, with a blank line before it, except
// a tool result, which belongs under its call.
func TestBlocksAreSeparatedAndResultsSitUnderTheirCalls(t *testing.T) {
	m := sized(t, nil)
	m.appendEvents([]protocol.Event{
		event("e1", protocol.EventMessage, protocol.MessageData{Role: "user", Content: "fix it"}),
		event("e2", protocol.EventThinking, protocol.ThinkingData{Content: "look first"}),
		event("e3", protocol.EventToolCall, protocol.ToolCallData{Tool: "bash"}),
		event("e4", protocol.EventToolResult, protocol.ToolResultData{Status: "ok", Content: "ok"}),
		event("e5", protocol.EventToolCall, protocol.ToolCallData{Tool: "read_file"}),
		event("e6", protocol.EventToolResult, protocol.ToolResultData{Status: "ok", Content: "package tui"}),
		event("e7", protocol.EventMessage, protocol.MessageData{Role: "assistant", Content: "fixed\n\nit was the cursor"}),
	})
	m.note("attaching")

	var got []string
	for _, e := range m.transcript {
		got = append(got, stripANSI(e.text))
	}
	want := []string{
		"› fix it",
		"",
		"~ thought (2 words)",
		"",
		"→ bash",
		"  ok",
		"",
		"→ read_file",
		"  package tui",
		"",
		"fixed",
		"",
		"it was the cursor",
		"",
		"attaching",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("transcript:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The thought slot moves past the blank line, so showing it rewrites the
// thought and not the gap before it.
func TestShowingAThoughtRewritesItsOwnSlot(t *testing.T) {
	m := sized(t, nil)
	m.appendEvents([]protocol.Event{
		event("e1", protocol.EventMessage, protocol.MessageData{Role: "user", Content: "go"}),
		event("e2", protocol.EventThinking, protocol.ThinkingData{Content: "the reasoning itself"}),
	})
	m.toggleThinking()
	if blank := m.transcript[1].text; blank != "" {
		t.Errorf("the gap before the thought was rewritten: %q", stripANSI(blank))
	}
	if body := stripANSI(m.body()); !strings.Contains(body, "the reasoning itself") {
		t.Errorf("the thought did not expand:\n%s", body)
	}
}

// Every line is kept off the edge, and a prompt's time ends at the pane's right
// edge however the prompt wraps.
func TestMarginAndPromptStamp(t *testing.T) {
	stamp := time.Date(2026, time.September, 23, 15, 1, 0, 0, time.Local)
	for _, width := range []int{80, 120} {
		m := sized(t, nil)
		m.width = width
		m.relayout()
		ev := event("e1", protocol.EventMessage,
			protocol.MessageData{Role: "user", Content: "fix the flaky attach test " + strings.Repeat("and more ", 20)})
		ev.Timestamp = stamp
		m.appendEvents([]protocol.Event{ev})

		lines := strings.Split(m.body(), "\n")
		first := lines[0]
		if w := visibleWidth(first); w != m.transcriptWidth() {
			t.Errorf("width %d: first line is %d wide, want %d: %q", width, w, m.transcriptWidth(), stripANSI(first))
		}
		if !strings.HasSuffix(stripANSI(first), "Sep 23 15:01") {
			t.Errorf("width %d: the time should end the first line: %q", width, stripANSI(first))
		}
		for i, l := range lines {
			if l != "" && !strings.HasPrefix(stripANSI(l), margin) {
				t.Errorf("width %d: line %d is not inside the margin: %q", width, i, stripANSI(l))
			}
			if w := visibleWidth(l); w > m.transcriptWidth() {
				t.Errorf("width %d: line %d is %d wide", width, i, w)
			}
		}
	}
}

// On a narrow pane the time goes rather than squeezing the prompt.
func TestNarrowPaneDropsTheStamp(t *testing.T) {
	got := wrapEntry(entry{text: "› hello", aside: "Sep 23 15:01"}, minAsideWidth-1)
	if len(got) != 1 || strings.Contains(got[0], "Sep") {
		t.Errorf("wrapped = %q, want the prompt alone", got)
	}
}

// Running and idle are the beat of every turn, and the status line shows them.
// The transcript keeps only the changes someone would want to find later.
func TestOnlyStateChangesWorthReadingAreShown(t *testing.T) {
	st := func(s protocol.SessionState) *protocol.SessionState { return &s }
	tests := []struct {
		name  string
		d     protocol.StateChangeData
		shown bool
	}{
		{"a prompt starts a turn", protocol.StateChangeData{From: st(protocol.StateIdle), To: protocol.StateRunning, Reason: "prompt"}, false},
		{"a turn ends", protocol.StateChangeData{From: st(protocol.StateRunning), To: protocol.StateIdle, Reason: "turn_complete"}, false},
		{"the first change", protocol.StateChangeData{To: protocol.StateRunning, Reason: "prompt"}, false},
		{"an interruption", protocol.StateChangeData{From: st(protocol.StateRunning), To: protocol.StateIdle, Reason: "interrupted"}, true},
		{"a shutdown", protocol.StateChangeData{From: st(protocol.StateRunning), To: protocol.StatePaused, Reason: "daemon_shutdown"}, true},
		{"a resume", protocol.StateChangeData{From: st(protocol.StatePaused), To: protocol.StateRunning, Reason: "resumed"}, true},
		{"blocked", protocol.StateChangeData{From: st(protocol.StateRunning), To: protocol.StateBlocked, Reason: "loop"}, true},
		{"completed", protocol.StateChangeData{From: st(protocol.StateRunning), To: protocol.StateCompleted}, true},
		{"error", protocol.StateChangeData{From: st(protocol.StateRunning), To: protocol.StateError}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderEvent(event("e1", protocol.EventStateChange, tt.d))
			if shown := len(got) > 0; shown != tt.shown {
				t.Errorf("shown = %v, want %v (%q)", shown, tt.shown, got)
			}
		})
	}
}

// The report is one line whose mark says how the run ended.
func TestReportLine(t *testing.T) {
	dirty := true
	tests := []struct {
		d    protocol.ReportData
		want string
	}{
		{protocol.ReportData{ExitStatus: protocol.StateCompleted,
			Tasks: protocol.ReportTasks{Done: 3, Total: 3}, FilesTouched: []string{"a.go"}},
			"✓ completed · tasks 3/3 · files 1"},
		{protocol.ReportData{ExitStatus: protocol.StateBlocked, TreeDirty: &dirty},
			"! blocked · tree dirty"},
		{protocol.ReportData{ExitStatus: protocol.StatePaused}, "! paused"},
		{protocol.ReportData{ExitStatus: protocol.StateError}, "✗ error"},
	}
	for _, tt := range tests {
		if got := stripANSI(renderReport(tt.d)); got != tt.want {
			t.Errorf("report = %q, want %q", got, tt.want)
		}
	}
}

// The screen is exactly the terminal: the composer's box, the gap above it and
// the status line all come out of the transcript's rows, and nothing is wider
// than the terminal, whatever is on screen.
func TestTheViewFillsTheTerminalExactly(t *testing.T) {
	states := []struct {
		name  string
		setup func(model) model
	}{
		{"idle", func(m model) model { return m }},
		{"composing a long prompt", func(m model) model {
			m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
			m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.Repeat("a long prompt ", 20))})
			return m
		}},
		{"asking", func(m model) model {
			return asked(m, "which design do you want?", "the simple one", "the fast one")
		}},
		{"with the task pane", func(m model) model {
			m, _ = send(m, eventMsg{ev: event("t1", protocol.EventTasks, protocol.TasksData{Tasks: []protocol.Task{
				{ID: "1", Title: "read the code", Status: protocol.TaskDone},
				{ID: "2", Title: "fix the race", Status: protocol.TaskInProgress},
			}})})
			return m
		}},
		{"the keys panel", func(m model) model {
			m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
			return m
		}},
		{"the picker, with more sessions than fit", func(m model) model {
			var sessions []goclient.SessionSummary
			for i := 0; i < 20; i++ {
				sessions = append(sessions, goclient.SessionSummary{
					SessionID: fmt.Sprintf("S%02d", i), LastPrompt: long(i), Workspace: `C:\work\nabu`, State: "idle"})
			}
			m, _ = send(m, sessionsMsg{sessions: sessions})
			return m
		}},
		{"a permission prompt", func(m model) model {
			m, _ = send(m, promptMsg{p: prompt{id: "r1", req: goclient.PermissionRequest{
				Tool: "bash", Risk: "high", Summary: "run " + long(1)}}})
			return m
		}},
		{"session ended", func(m model) model {
			m, _ = send(m, eventMsg{ev: event("s1", protocol.EventStateChange,
				protocol.StateChangeData{To: protocol.StateCompleted})})
			return m
		}},
	}
	for _, size := range [][2]int{{80, 24}, {120, 40}} {
		for _, st := range states {
			t.Run(fmt.Sprintf("%s at %dx%d", st.name, size[0], size[1]), func(t *testing.T) {
				m := sized(t, nil)
				m, _ = send(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				for i := 0; i < 30; i++ {
					m.appendEvent(event(fmt.Sprintf("e%02d", i), protocol.EventMessage,
						protocol.MessageData{Role: "assistant", Content: long(i)}))
				}
				m = st.setup(m)

				view := m.View()
				if h := lipgloss.Height(view); h != size[1] {
					t.Errorf("the view is %d rows on a %d-row terminal:\n%s", h, size[1], stripANSI(view))
				}
				for i, line := range strings.Split(view, "\n") {
					if w := visibleWidth(line); w > size[0] {
						t.Errorf("row %d is %d wide on a %d-column terminal: %q", i, w, size[0], stripANSI(line))
					}
				}
			})
		}
	}
}

// On a narrow terminal the hints give way to the status.
func TestTheStatusLineFitsANarrowTerminal(t *testing.T) {
	m := sized(t, nil)
	m, _ = send(m, tea.WindowSizeMsg{Width: 40, Height: 24})
	m, _ = send(m, connMsg{state: connected})
	status := m.status()
	if w := visibleWidth(status); w > 40 {
		t.Errorf("the status is %d wide on a 40-column terminal: %q", w, stripANSI(status))
	}
	if strings.Contains(status, "? keys") {
		t.Errorf("the hints should give way: %q", stripANSI(status))
	}
	if !strings.Contains(status, "connected") {
		t.Errorf("the status itself must stay: %q", stripANSI(status))
	}

	m, _ = send(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if status := stripANSI(m.status()); !strings.HasSuffix(status, "q quit"+margin) {
		t.Errorf("at 80 columns the hints fit at the right: %q", status)
	}
}

// ? shows every key; esc puts it away, and q closes the panel rather than the
// client.
func TestTheKeysPanel(t *testing.T) {
	m := sized(t, nil)
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	view := stripANSI(m.View())
	for _, want := range []string{"ctrl+x", "/help", m.sessionID} {
		if !strings.Contains(view, want) {
			t.Errorf("the keys panel should list %q:\n%s", want, view)
		}
	}

	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if m.quitting || cmd != nil {
		t.Error("q on the keys panel should close it, not quit")
	}
	if m.showKeys {
		t.Error("q should close the keys panel")
	}

	m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showKeys {
		t.Error("esc should close the keys panel")
	}
}

// A long task title is cut to the pane, never wrapped onto a second row.
func TestALongTaskTitleFitsThePane(t *testing.T) {
	line := taskLine(protocol.Task{Title: strings.Repeat("a very long task title ", 5), Status: protocol.TaskInProgress})
	if w, room := visibleWidth(line), taskPaneWidth-2-taskPanePad; w > room {
		t.Errorf("the task line is %d wide, the pane holds %d", w, room)
	}
}

// A long line runs on under its text, not back at the margin, so a wrapped
// command or result stays inside its block.
func TestWrappedLinesHangUnderTheirText(t *testing.T) {
	m := sized(t, nil)
	m.appendEvents([]protocol.Event{
		event("e1", protocol.EventToolCall, protocol.ToolCallData{Tool: "bash",
			Arguments: []byte(`{"command":"` + strings.Repeat("git add -A ", 12) + `"}`)}),
		event("e2", protocol.EventToolResult, protocol.ToolResultData{Status: "ok",
			Content: strings.Repeat("warning: LF will be replaced by CRLF ", 4)}),
	})
	lines := strings.Split(stripANSI(m.body()), "\n")
	var call, result []string
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "  → "):
			call = append(call, l)
		case strings.HasPrefix(l, "    warning"):
			result = append(result, l)
		case len(result) > 0:
			result = append(result, l)
		case len(call) > 0:
			call = append(call, l)
		}
	}
	if len(call) < 2 || len(result) < 2 {
		t.Fatalf("both should wrap:\n%s", strings.Join(lines, "\n"))
	}
	for _, l := range call[1:] {
		if !strings.HasPrefix(l, "    ") || strings.HasPrefix(l, "     ") {
			t.Errorf("a call's continuation should hang at column 4: %q", l)
		}
	}
	for _, l := range result[1:] {
		if !strings.HasPrefix(l, "    ") || strings.HasPrefix(l, "     ") {
			t.Errorf("a result's continuation should stay at its indent: %q", l)
		}
	}
}

// A result that is one long word, a line of JSON, used to wrap whole and leave
// its indent alone on a line that looked like a gap between call and result.
func TestAnUnbrokenResultStaysUnderItsCall(t *testing.T) {
	m := sized(t, nil)
	m.appendEvents([]protocol.Event{
		event("e1", protocol.EventToolCall, protocol.ToolCallData{Tool: "gh"}),
		event("e2", protocol.EventToolResult, protocol.ToolResultData{Status: "ok",
			Content: `{"additions":5674,` + strings.Repeat(`"author":"x",`, 20) + `}`}),
	})
	lines := strings.Split(stripANSI(m.body()), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[1], `    {"additions"`) {
		t.Errorf("the result should start on the line under its call:\n%s", strings.Join(lines, "\n"))
	}
}
