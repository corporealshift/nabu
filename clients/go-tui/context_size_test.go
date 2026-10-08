package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/corporealshift/nabu/protocol"
)

// /context sets a session's size, or says what it is
// (docs/specs/2026-10-08-context-size-design.md).
func TestContextCommand(t *testing.T) {
	for _, tc := range []struct {
		line, size, note string
	}{
		{"/context large", "large", ""},
		{"/context Normal", "normal", ""},
		{"/context", "", ""},
		{"/context huge", "", "takes normal or large"},
	} {
		res := parseCommand(tc.line)
		if tc.note != "" {
			if res.act != nil || !strings.Contains(res.note, tc.note) {
				t.Errorf("%q: want the note %q, got %+v", tc.line, tc.note, res)
			}
			continue
		}
		if res.act == nil || res.act.kind != actContext || res.act.text != tc.size {
			t.Errorf("%q: want a context action for %q, got %+v", tc.line, tc.size, res)
		}
	}
}

// Setting a size goes to the daemon; asking for it does not.
func TestContextCommandSendsOnlyAChange(t *testing.T) {
	submit := func(m model, line string) (model, tea.Cmd) {
		m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
		m = typeIn(m, line)
		return send(m, tea.KeyMsg{Type: tea.KeyEnter})
	}

	actions := make(chan action, 2)
	m := sized(t, actions)
	m, cmd := submit(m, "/context large")
	cmd()
	if a := <-actions; a.kind != actContext || a.text != "large" || a.sessionID == "" {
		t.Fatalf("action = %+v", a)
	}

	m, cmd = submit(m, "/context")
	if cmd != nil {
		cmd()
	}
	select {
	case a := <-actions:
		t.Fatalf("asking sent %+v", a)
	default:
	}
	if !strings.Contains(m.body(), "context: normal") {
		t.Errorf("the transcript should say the session is normal:\n%s", m.body())
	}
}

// The badge marks a large session, from its session event or a later change.
func TestTheBadgeMarksALargeSession(t *testing.T) {
	m := newModel("S1", make(chan action, 1))
	m.appendEvent(event("e0", protocol.EventSession, protocol.SessionData{
		Workspace: "C:/w", WorkspaceKey: "w-1", ContextWindow: 1000,
		Options: protocol.Options{Model: "m", PermissionMode: "auto", Context: protocol.ContextLarge},
	}))
	m.appendEvent(assistantEvent("e1", 400))
	if got := m.contextBadge(); !strings.Contains(got, "large") {
		t.Errorf("badge = %q, want it to say large", got)
	}
	m.appendEvent(event("e2", protocol.EventOptionsChange, protocol.OptionsChangeData{
		Key: "context", From: []byte(`"large"`), To: []byte(`"normal"`), Source: "client"}))
	if got := m.contextBadge(); strings.Contains(got, "large") {
		t.Errorf("badge = %q after switching to normal", got)
	}
}
