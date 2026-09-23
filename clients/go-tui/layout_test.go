package tui

import (
	"strings"
	"testing"
	"time"

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
		"~ thought (2 words) · t to show",
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
