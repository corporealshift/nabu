package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/corporealshift/nabu/protocol"
)

func thinkingEvent(id, content string) protocol.Event {
	return event(id, protocol.EventThinking,
		protocol.ThinkingData{Content: content, Source: "model"})
}

func messageEvent(id, role, content string) protocol.Event {
	return event(id, protocol.EventMessage,
		protocol.MessageData{Role: role, Content: content})
}

const longThought = "First I should read the file. Then I should check whether the " +
	"test covers the branch. If it does not, I will add one before touching " +
	"the implementation at all."

// Issue 29: thinking is there to be explored, but it must not bury the answer.
func TestThinkingIsCollapsedByDefault(t *testing.T) {
	m := newModel("S1", make(chan action, 1))
	m.appendEvent(thinkingEvent("01A", longThought))

	body := m.body()

	if strings.Contains(body, "check whether the test covers") {
		t.Error("thinking should be collapsed until asked for")
	}
	if !strings.Contains(body, "thought") {
		t.Errorf("a collapsed thought should still say it is there, got %q", body)
	}
}

func TestThinkingExpandsOnToggle(t *testing.T) {
	m := newModel("S1", make(chan action, 1))
	m.appendEvent(thinkingEvent("01A", longThought))

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	expanded := next.(model)

	if !strings.Contains(expanded.body(), "check whether the test covers") {
		t.Error("t should expand thinking")
	}

	next, _ = expanded.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if strings.Contains(next.(model).body(), "check whether the test covers") {
		t.Error("t again should collapse it")
	}
}

// Toggling must not disturb the conversation around it.
func TestTogglingKeepsTheTranscriptInOrder(t *testing.T) {
	m := newModel("S1", make(chan action, 1))
	m.appendEvent(messageEvent("01A", "user", "do the thing"))
	m.appendEvent(thinkingEvent("01B", longThought))
	m.appendEvent(messageEvent("01C", "assistant", "done"))

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	body := next.(model).body()

	prompt := strings.Index(body, "do the thing")
	thought := strings.Index(body, "First I should read")
	answer := strings.Index(body, "done")

	if prompt < 0 || thought < 0 || answer < 0 {
		t.Fatalf("something is missing from the transcript: %q", body)
	}
	if !(prompt < thought && thought < answer) {
		t.Errorf("thinking should sit between the prompt and the answer, got %q", body)
	}
}

// While the composer is open, t is a letter.
func TestToggleDoesNotFireWhileTyping(t *testing.T) {
	m := newModel("S1", make(chan action, 1))
	m.appendEvent(thinkingEvent("01A", longThought))
	m.composing = true

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	typed := next.(model)

	if typed.input != "t" {
		t.Errorf("t should have been typed, input is %q", typed.input)
	}
	if strings.Contains(typed.body(), "check whether the test covers") {
		t.Error("typing t must not expand thinking")
	}
}

// A provider that reports no reasoning should leave no trace.
func TestNoThinkingNoLine(t *testing.T) {
	m := newModel("S1", make(chan action, 1))
	m.appendEvent(messageEvent("01A", "assistant", "done"))

	if strings.Contains(m.body(), "thought") {
		t.Errorf("a session without thinking should say nothing about it: %q", m.body())
	}
}
