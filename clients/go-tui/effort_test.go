package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/corporealshift/rho/protocol"
)

// /effort sets a session's effort, clears it, or says what it is
// (docs/specs/2026-10-16-session-effort-design.md).
func TestEffortCommand(t *testing.T) {
	for _, tc := range []struct {
		line, level, note string
	}{
		{"/effort medium", "medium", ""},
		{"/effort XHigh", "xhigh", ""},
		{"/effort none", "none", ""},
		{"/effort max", "max", ""},
		{"/effort default", "default", ""},
		{"/effort", "", ""},
		{"/effort extreme", "", "takes none, low"},
	} {
		res := parseCommand(tc.line)
		if tc.note != "" {
			if res.act != nil || !strings.Contains(res.note, tc.note) {
				t.Errorf("%q: want the note %q, got %+v", tc.line, tc.note, res)
			}
			continue
		}
		if res.act == nil || res.act.kind != actEffort || res.act.text != tc.level {
			t.Errorf("%q: want an effort action for %q, got %+v", tc.line, tc.level, res)
		}
	}
}

// Setting an effort goes to the daemon; asking for it does not.
func TestEffortCommandSendsOnlyAChange(t *testing.T) {
	submit := func(m model, line string) (model, tea.Cmd) {
		m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
		m = typeIn(m, line)
		return send(m, tea.KeyMsg{Type: tea.KeyEnter})
	}

	actions := make(chan action, 2)
	m := sized(t, actions)
	m, cmd := submit(m, "/effort low")
	cmd()
	if a := <-actions; a.kind != actEffort || a.text != "low" || a.sessionID == "" {
		t.Fatalf("action = %+v", a)
	}

	m, cmd = submit(m, "/effort")
	if cmd != nil {
		cmd()
	}
	select {
	case a := <-actions:
		t.Fatalf("asking sent %+v", a)
	default:
	}
	if !strings.Contains(m.body(), "effort: default") {
		t.Errorf("the transcript should say the effort is the server's:\n%s", m.body())
	}
}

// The model follows the effort from the session event and later changes, and
// the status line shows it only when set.
func TestTheStatusLineShowsTheEffort(t *testing.T) {
	m := newModel("S1", make(chan action, 1))
	m.appendEvent(event("e0", protocol.EventSession, protocol.SessionData{
		Workspace: "C:/w", WorkspaceKey: "w-1", ContextWindow: 1000,
		Options: protocol.Options{Model: "m", PermissionMode: "auto", Effort: protocol.EffortMedium},
	}))
	if !strings.Contains(m.status(), "effort medium") {
		t.Errorf("status = %q, want it to show effort medium", m.status())
	}
	m.appendEvent(event("e1", protocol.EventOptionsChange, protocol.OptionsChangeData{
		Key: "effort", From: []byte(`"medium"`), To: []byte(`""`), Source: "client"}))
	if strings.Contains(m.status(), "effort") {
		t.Errorf("status = %q after clearing", m.status())
	}
	if got := effortWords(""); !strings.Contains(got, "default") {
		t.Errorf("effortWords(\"\") = %q", got)
	}
}
