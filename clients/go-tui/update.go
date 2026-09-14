package main

import (
	"encoding/json"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/corporealshift/nabu/clients/goclient"
	"github.com/corporealshift/nabu/protocol"
)

// Messages the client goroutine sends into the program.
type (
	// eventMsg is one logged session event.
	eventMsg struct{ ev protocol.Event }
	// deltaMsg is streaming assistant text. Ephemeral: never logged.
	deltaMsg struct{ d goclient.SessionDelta }
	// promptMsg is a permission request awaiting an answer.
	promptMsg struct{ p prompt }
	// resolvedMsg says a request was answered elsewhere, so dismiss it.
	resolvedMsg struct{ requestID string }
	// connMsg reports a change in the connection.
	connMsg struct {
		state connState
		note  string
	}
	// errMsg is something the user should see but that did not come from the log.
	errMsg struct{ text string }
)

// answer is what the program sends back when a human decides. The client
// goroutine picks it up and replies to the daemon.
type answer struct {
	id      any
	approve bool
	reason  string
}

func (m model) Init() tea.Cmd { return nil }

// Update is the whole of the TUI's behaviour, and it is a pure function of the
// model and one message, so every case below is directly testable.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		h := msg.Height - 2 // one line of status, one of help
		if h < 1 {
			h = 1
		}
		m.viewport.Width, m.viewport.Height = msg.Width, h
		m.refresh()
		return m, nil

	case tea.KeyMsg:
		return m.onKey(msg)

	case eventMsg:
		m.appendEvent(msg.ev)
		return m, nil

	case deltaMsg:
		m.addDelta(msg.d)
		return m, nil

	case promptMsg:
		m.enqueue(msg.p)
		return m, nil

	case resolvedMsg:
		if m.dropPrompt(msg.requestID) {
			m.note("that request was already answered")
		}
		return m, nil

	case connMsg:
		m.conn, m.connNote = msg.state, msg.note
		if msg.note != "" {
			m.note(msg.note)
		}
		return m, nil

	case errMsg:
		m.lastError = msg.text
		m.note(msg.text)
		return m, nil
	}
	return m, nil
}

// onKey handles a keypress. While a prompt is up it takes every key, so a
// stray keystroke cannot scroll the transcript instead of answering.
func (m model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pending != nil {
		switch msg.String() {
		case "y", "enter":
			return m.answerPending(true, "")
		case "n", "esc":
			return m.answerPending(false, "denied from the terminal")
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	}

	switch msg.String() {
	case "q", "ctrl+c", "esc":
		// Quitting closes the view, never the run. The daemon owns the
		// session and it keeps going.
		m.quitting = true
		return m, tea.Quit
	case "g", "home":
		m.viewport.GotoTop()
	case "G", "end":
		m.viewport.GotoBottom()
	default:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

// answerPending replies to the prompt on screen and shows the next one.
func (m model) answerPending(approve bool, reason string) (tea.Model, tea.Cmd) {
	p := m.pending
	m.resolve()

	verb := "approved"
	if !approve {
		verb = "denied"
	}
	m.note(verb + ": " + p.req.Summary)

	a := answer{id: p.id, approve: approve, reason: reason}
	answers := m.answers
	return m, func() tea.Msg {
		if answers != nil {
			answers <- a
		}
		return nil
	}
}

// unmarshal decodes an event's data.
func unmarshal(ev protocol.Event, v any) error {
	return json.Unmarshal(ev.Data, v)
}
