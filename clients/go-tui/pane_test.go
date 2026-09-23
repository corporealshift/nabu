package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/corporealshift/nabu/clients/goclient"
	"github.com/corporealshift/nabu/protocol"
)

// freshBody is the transcript wrapped from scratch, the way it was before the
// cache existed. The cache must never show anything else.
func freshBody(m model) string {
	width := m.transcriptWidth()
	lines := wrapAll(append(append([]entry{}, m.transcript...), m.preview()...), width)
	return strings.Join(lines, "\n")
}

// long is a line wide enough to wrap at any width the tests use.
func long(n int) string {
	return fmt.Sprintf("message %d ", n) + strings.Repeat("words that wrap ", 12)
}

func TestTheWrapCacheShowsWhatAFreshWrapWould(t *testing.T) {
	m := sized(t, nil)
	check := func(step string) {
		t.Helper()
		if got, want := m.body(), freshBody(m); got != want {
			t.Fatalf("after %s the cached transcript differs from a fresh wrap:\n got %q\nwant %q", step, got, want)
		}
	}

	var batch []protocol.Event
	for i := 0; i < 5; i++ {
		batch = append(batch, event(fmt.Sprintf("e%02d", i), protocol.EventMessage,
			protocol.MessageData{Role: "assistant", Content: long(i)}))
	}
	m, _ = send(m, eventsMsg{events: batch})
	check("a replay")

	m, _ = send(m, eventMsg{ev: event("e10", protocol.EventThinking,
		protocol.ThinkingData{Content: "first line of thought\nsecond line of thought"})})
	check("a thought")

	m, _ = send(m, deltaMsg{d: goclient.SessionDelta{TurnID: "t1", Text: long(99)}})
	check("a streamed token")

	m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	check("showing thinking")

	m, _ = send(m, tea.WindowSizeMsg{Width: 50, Height: 20})
	check("a resize")

	m, _ = send(m, eventMsg{ev: event("e11", protocol.EventMessage,
		protocol.MessageData{Role: "assistant", Content: long(11)})})
	check("the message that ends the stream")

	m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	check("hiding thinking")
}

// Issue 82: every streamed token re-wrapped the whole history, and on a long
// session the TUI fell behind the stream. A token must leave history alone.
func TestAStreamedTokenDoesNotRewrapHistory(t *testing.T) {
	m := sized(t, nil)
	var batch []protocol.Event
	for i := 0; i < 200; i++ {
		batch = append(batch, event(fmt.Sprintf("e%04d", i), protocol.EventMessage,
			protocol.MessageData{Role: "assistant", Content: long(i)}))
	}
	m, _ = send(m, eventsMsg{events: batch})
	before := &m.wrapped[0]

	for i := 0; i < 20; i++ {
		m, _ = send(m, deltaMsg{d: goclient.SessionDelta{TurnID: "t1", Text: "token "}})
	}
	if &m.wrapped[0] != before || m.wrappedN != len(m.transcript) {
		t.Fatal("streaming re-wrapped the history it had already wrapped")
	}
	if !strings.Contains(m.viewport.View(), "token token") {
		t.Fatalf("the streamed text should be on screen:\n%s", m.viewport.View())
	}
}

func TestThePaneScrollsAndFollowsTheTail(t *testing.T) {
	p := newPane(20, 3)
	p.SetLines([]string{"1", "2", "3", "4", "5", "6"})
	p.GotoBottom()
	if !p.AtBottom() || !strings.Contains(p.View(), "6") || strings.Contains(p.View(), "3") {
		t.Fatalf("at the bottom the last three lines show:\n%s", p.View())
	}

	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyUp})
	if p.AtBottom() || !strings.Contains(p.View(), "3") {
		t.Fatalf("up scrolls one line:\n%s", p.View())
	}
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if !p.AtTop() {
		t.Fatal("a page up from the second screen reaches the top")
	}
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if !p.AtBottom() {
		t.Fatal("paging down stops at the bottom rather than past it")
	}

	short := newPane(20, 4)
	short.SetLines([]string{"only"})
	if got := len(strings.Split(short.View(), "\n")); got != 4 {
		t.Fatalf("a short transcript still fills the pane, got %d lines", got)
	}
}
