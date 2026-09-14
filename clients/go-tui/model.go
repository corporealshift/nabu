package main

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"

	"github.com/corporealshift/nabu/clients/goclient"
	"github.com/corporealshift/nabu/protocol"
)

// connState is what the client is doing about its connection.
type connState int

const (
	connecting connState = iota
	connected
	disconnected
)

// prompt is a permission request waiting on the human.
type prompt struct {
	id  any // the JSON-RPC id to answer with
	req goclient.PermissionRequest
}

// model is the whole TUI state. It holds no session state of its own: the
// daemon owns the log and this renders from events. What lives here is only
// what is on screen.
type model struct {
	sessionID string

	// transcript is the rendered log, one line per entry.
	transcript []string
	viewport   viewport.Model
	ready      bool

	// lastEventID is the cursor. A reconnect fetches from here, so a dropped
	// connection costs nothing but the gap.
	lastEventID string

	// streaming is the assistant text accumulated from deltas for the current
	// turn. It is shown live and replaced by the final message event, so the
	// text never appears twice.
	streaming string
	turnID    string

	// pending is the prompt on screen; queued are the ones behind it. The
	// daemon can ask about several calls, and answering the wrong one would
	// approve something the human never saw.
	pending *prompt
	queued  []prompt

	conn      connState
	connNote  string
	state     protocol.SessionState
	turns     int
	lastError string

	width, height int
	quitting      bool

	// answers carries a human's decision out to the connection goroutine.
	// Update is pure, so it cannot reply to the daemon itself; it sends here
	// and the goroutine that owns the socket does the writing.
	answers chan<- answer
}

func newModel(sessionID string, answers chan<- answer) model {
	return model{
		sessionID: sessionID,
		conn:      connecting,
		state:     protocol.StateIdle,
		answers:   answers,
	}
}

// appendEvent renders an event into the transcript and advances the cursor.
// A duplicate arriving from both the live stream and a replay is ignored,
// because the cursor only ever moves forward.
func (m *model) appendEvent(ev protocol.Event) {
	if ev.ID != "" && ev.ID <= m.lastEventID {
		return
	}
	if ev.ID != "" {
		m.lastEventID = ev.ID
	}

	// The final message supersedes every delta of its turn.
	if ev.Type == protocol.EventMessage {
		m.streaming = ""
		m.turnID = ""
	}
	if ev.Type == protocol.EventStateChange {
		if st, ok := stateOf(ev); ok {
			m.state = st
		}
	}
	m.transcript = append(m.transcript, renderEvent(ev)...)
	m.refresh()
}

// note adds a line that did not come from the log: a connection change, or a
// local error. The daemon never sees these.
func (m *model) note(line string) {
	m.transcript = append(m.transcript, dim.Render(line))
	m.refresh()
}

// addDelta accumulates streaming text for the live preview.
func (m *model) addDelta(d goclient.SessionDelta) {
	if d.TurnID != m.turnID {
		m.turnID = d.TurnID
		m.streaming = ""
	}
	m.streaming += d.Text
	m.refresh()
}

// enqueue adds a permission request, showing it if nothing else is on screen.
func (m *model) enqueue(p prompt) {
	if m.pending == nil {
		m.pending = &p
		return
	}
	m.queued = append(m.queued, p)
}

// resolve dismisses the current prompt and promotes the next.
func (m *model) resolve() {
	m.pending = nil
	if len(m.queued) > 0 {
		next := m.queued[0]
		m.queued = m.queued[1:]
		m.pending = &next
	}
}

// dropPrompt removes a request by id wherever it is, for one the daemon has
// already resolved elsewhere.
func (m *model) dropPrompt(requestID string) bool {
	if m.pending != nil && m.pending.req.RequestID == requestID {
		m.resolve()
		return true
	}
	for i, q := range m.queued {
		if q.req.RequestID == requestID {
			m.queued = append(m.queued[:i], m.queued[i+1:]...)
			return true
		}
	}
	return false
}

// body is the transcript plus the live streaming preview.
func (m model) body() string {
	lines := m.transcript
	if m.streaming != "" {
		lines = append(append([]string{}, lines...),
			strings.Split(m.streaming, "\n")...)
	}
	return strings.Join(lines, "\n")
}

// refresh puts the current body in the viewport and follows the tail, which is
// what a live transcript should do.
func (m *model) refresh() {
	if !m.ready {
		return
	}
	atBottom := m.viewport.AtBottom()
	m.viewport.SetContent(m.body())
	if atBottom {
		m.viewport.GotoBottom()
	}
}

// stateOf reads the destination state out of a state_change event.
func stateOf(ev protocol.Event) (protocol.SessionState, bool) {
	var d protocol.StateChangeData
	if unmarshal(ev, &d) != nil {
		return "", false
	}
	return d.To, true
}

// terminal reports whether a state ends the run.
func terminal(s protocol.SessionState) bool {
	switch s {
	case protocol.StateCompleted, protocol.StateBlocked,
		protocol.StatePaused, protocol.StateError:
		return true
	}
	return false
}
