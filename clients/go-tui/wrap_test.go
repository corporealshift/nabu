package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/corporealshift/nabu/protocol"
)

// sizedAt returns a model given a terminal of the width it will be read in.
func sizedAt(t *testing.T, width, height int) model {
	t.Helper()
	m := newModel("S1", make(chan action, 1))
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return next.(model)
}

const longReply = "The bug is in the pad helper: it subtracts one more than it " +
	"should, so every column comes out a character short and the table never " +
	"lines up. Fixing the arithmetic there fixes the test."

// Issue 42: a reply wider than the terminal ran off the side of it, and what
// went past the edge could not be read at all.
func TestATranscriptLineIsWrappedToTheTerminal(t *testing.T) {
	m := sizedAt(t, 40, 24)
	m.appendEvent(event("e1", protocol.EventMessage,
		protocol.MessageData{Role: "assistant", Content: longReply}))

	for i, line := range strings.Split(m.body(), "\n") {
		if w := visibleWidth(line); w > 40 {
			t.Errorf("line %d is %d columns wide in a 40-column terminal: %q", i, w, line)
		}
	}
}

// Wrapping must not lose any of the words it wraps.
func TestWrappingKeepsEveryWord(t *testing.T) {
	m := sizedAt(t, 40, 24)
	m.appendEvent(event("e1", protocol.EventMessage,
		protocol.MessageData{Role: "assistant", Content: longReply}))

	flat := strings.Join(strings.Fields(strings.ReplaceAll(m.body(), "\n", " ")), " ")
	for _, word := range []string{"subtracts", "character", "arithmetic"} {
		if !strings.Contains(flat, word) {
			t.Errorf("%q was lost in the wrapping:\n%s", word, m.body())
		}
	}
}

// A narrower terminal re-wraps rather than keeping the old layout.
func TestResizeRewraps(t *testing.T) {
	m := sizedAt(t, 80, 24)
	m.appendEvent(event("e1", protocol.EventMessage,
		protocol.MessageData{Role: "assistant", Content: longReply}))
	wide := len(strings.Split(m.body(), "\n"))

	next, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 24})
	narrow := len(strings.Split(next.(model).body(), "\n"))

	if narrow <= wide {
		t.Errorf("a 40-column terminal produced %d lines, a 80-column one %d", narrow, wide)
	}
}

// A prompt the user typed wraps too, and keeps its marker on the first line.
func TestAUserLineWraps(t *testing.T) {
	m := sizedAt(t, 30, 24)
	m.appendEvent(event("e1", protocol.EventMessage,
		protocol.MessageData{Role: "user", Content: longReply}))

	lines := strings.Split(m.body(), "\n")
	if len(lines) < 2 {
		t.Fatalf("a long prompt should wrap, got %d line(s)", len(lines))
	}
	for i, line := range lines {
		if w := visibleWidth(line); w > 30 {
			t.Errorf("line %d is %d columns wide: %q", i, w, line)
		}
	}
}

// The task pane takes its share, so the transcript wraps to what is left.
func TestWrappingRespectsTheTaskPane(t *testing.T) {
	m := sizedAt(t, 120, 24)
	m.tasks = []protocol.Task{{ID: "t1", Title: "something", Status: protocol.TaskInProgress}}
	m.appendEvent(event("e1", protocol.EventMessage,
		protocol.MessageData{Role: "assistant", Content: longReply}))

	for i, line := range strings.Split(m.body(), "\n") {
		if w := visibleWidth(line); w > m.transcriptWidth() {
			t.Errorf("line %d is %d columns wide, transcript has %d: %q",
				i, w, m.transcriptWidth(), line)
		}
	}
}
