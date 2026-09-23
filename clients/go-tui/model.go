package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"

	"github.com/corporealshift/nabu/clients/goclient"
	"github.com/corporealshift/nabu/daemon/modules/artifact"
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

	// thoughts maps a transcript slot to the reasoning written there, so
	// showing or hiding thinking rewrites that slot rather than the list.
	thoughts     map[int]string
	showThinking bool
	thinkingNow  string
	thinkingTurn string
	turnID       string

	// pending is the prompt on screen; queued are the ones behind it. The
	// daemon can ask about several calls, and answering the wrong one would
	// approve something the human never saw.
	pending *prompt
	queued  []prompt

	// asking is the question the agent put to the human, if any. One at a
	// time: the agent is blocked until it is answered, so it cannot ask a
	// second thing while waiting.
	asking *question

	conn        connState
	connNote    string
	state       protocol.SessionState
	turns       int
	lastError   string
	lastEventAt time.Time

	// runningSince is when the current turn began, so the working indicator
	// reports how long the model has actually been thinking rather than how
	// long the program has been open. Zero when not running.
	runningSince time.Time
	// spinner advances on every tick, so a screen with no new output is still
	// visibly alive.
	spinner int

	// contextWindow and lastInput are the denominator and numerator of how
	// full the context is. The window comes from the session event, the usage
	// from the last assistant message: a sum would be wrong, since each
	// request carries the whole conversation again.
	contextWindow int
	lastInput     int

	// tasks and goal are what the agent believes it is doing. They come from
	// events but never reach the transcript: re-rendering a task list inline
	// on every update would drown the conversation.
	tasks []protocol.Task
	goal  *protocol.GoalData

	// artifacts are the pages the agent made, latest version of each by name,
	// and lastArtifact the most recent: what o opens.
	artifacts    map[string]artifact.Args
	lastArtifact string

	// composer
	composing bool
	input     string

	// picker
	picking  bool
	sessions []goclient.SessionSummary
	cursorAt int

	width, height int
	quitting      bool

	// actions carries what the human asked for out to the connection
	// goroutine. Update is pure, so it cannot touch the network itself; it
	// sends here and the goroutine that owns the socket does the work.
	actions chan<- action
}

// Default dimensions until the terminal reports its own. Waiting for a size
// message before rendering anything leaves the screen blank if one is slow or
// never comes, which is what happens when output is not a terminal.
const (
	defaultWidth  = 80
	defaultHeight = 24
)

func newModel(sessionID string, actions chan<- action) model {
	m := model{
		sessionID: sessionID,
		conn:      connecting,
		state:     protocol.StateIdle,
		actions:   actions,
		width:     defaultWidth,
		height:    defaultHeight,
	}
	m.viewport = viewport.New(defaultWidth, defaultHeight-2)
	m.ready = true
	return m
}

// appendEvent renders an event into the transcript and advances the cursor.
// A duplicate arriving from both the live stream and a replay is ignored,
// because the cursor only ever moves forward.
func (m *model) appendEvent(ev protocol.Event) {
	m.appendEvents([]protocol.Event{ev})
}

// appendEvents applies a replay batch and refreshes the viewport once. Rebuilding
// the viewport for each event makes attaching to a long session quadratic.
func (m *model) appendEvents(events []protocol.Event) {
	for _, ev := range events {
		m.appendEventWithoutRefresh(ev)
	}
	m.refresh()
}

func (m *model) appendEventWithoutRefresh(ev protocol.Event) {
	if ev.ID != "" && ev.ID <= m.lastEventID {
		return
	}
	if ev.ID != "" {
		m.lastEventID = ev.ID
	}
	if !ev.Timestamp.IsZero() {
		m.lastEventAt = ev.Timestamp
	}

	// The final message supersedes every delta of its turn.
	if ev.Type == protocol.EventMessage {
		m.streaming = ""
		m.turnID = ""
	}
	if ev.Type == protocol.EventStateChange {
		if st, ok := stateOf(ev); ok {
			m.setState(st)
		}
	}
	if ev.Type == protocol.EventSession {
		var d protocol.SessionData
		if unmarshal(ev, &d) == nil {
			m.contextWindow = d.ContextWindow
		}
	}
	if ev.Type == protocol.EventMessage {
		var d protocol.MessageData
		if unmarshal(ev, &d) == nil && d.Role == "assistant" && d.Usage != nil {
			m.lastInput = d.Usage.InputTokens
		}
	}
	if ev.Type == protocol.EventToolCall {
		m.rememberArtifact(ev)
	}
	if ev.Type == protocol.EventTasks {
		var d protocol.TasksData
		if unmarshal(ev, &d) == nil {
			m.tasks = d.Tasks
		}
	}
	if ev.Type == protocol.EventGoal {
		var d protocol.GoalData
		if unmarshal(ev, &d) == nil {
			if d.State == "cleared" {
				m.goal = nil
			} else {
				m.goal = &d
			}
		}
	}
	// The thinking event supersedes the stream it was assembled from.
	if ev.Type == protocol.EventThinking {
		m.thinkingNow, m.thinkingTurn = "", ""
		if line := m.rememberThought(ev); line != "" {
			m.transcript = append(m.transcript, line)
		}
		return
	}
	m.transcript = append(m.transcript, renderEvent(ev)...)
}

// setState records a state change and starts or stops the turn clock. The
// clock runs from the transition into running, not from the prompt, so a
// session that waited its turn does not appear to have been working the whole
// time.
func (m *model) setState(st protocol.SessionState) {
	if st == protocol.StateRunning && m.state != protocol.StateRunning {
		m.runningSince = time.Now()
	}
	if st != protocol.StateRunning {
		m.runningSince = time.Time{}
	}
	m.state = st
}

// working reports whether the agent is mid-turn, which is what the indicator
// answers.
func (m model) working() bool {
	return m.state == protocol.StateRunning
}

// elapsed is how long the current turn has run.
func (m model) elapsed() time.Duration {
	if m.runningSince.IsZero() {
		return 0
	}
	return time.Since(m.runningSince).Truncate(time.Second)
}

// reset clears everything belonging to one session, in one operation. Doing it
// field by field is how a transcript ends up under the wrong session's id.
func (m *model) reset(sessionID string) {
	m.sessionID = sessionID
	m.transcript = nil
	m.lastEventID = ""
	m.streaming = ""
	m.turnID = ""
	m.tasks = nil
	m.goal = nil
	m.artifacts, m.lastArtifact = nil, ""
	m.contextWindow = 0
	m.lastInput = 0
	m.pending = nil
	m.queued = nil
	m.asking = nil
	m.state = protocol.StateIdle
	m.runningSince = time.Time{}
	m.lastEventAt = time.Time{}
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

// taskPaneWidth is the right-hand column. Below minWideTerminal the pane is
// hidden instead of squeezed: two columns in eighty make both unreadable.
const (
	taskPaneWidth   = 34
	minWideTerminal = 100
)

// showTasks reports whether there is both something to show and room to show it.
func (m model) showTasks() bool {
	return len(m.tasks) > 0 && m.width >= minWideTerminal
}

// transcriptWidth is what the transcript gets once the task pane has its share.
func (m model) transcriptWidth() int {
	if m.showTasks() {
		return m.width - taskPaneWidth
	}
	return m.width
}

// body is the transcript plus the live streaming preview.
func (m model) body() string {
	// Wrapped here rather than when the event was rendered: the width is not
	// known then, and it changes when the terminal does.
	lines := wrapAll(m.transcript, m.transcriptWidth())
	if m.showThinking && m.thinkingNow != "" {
		lines = append(append([]string{}, lines...),
			wrapAll(strings.Split(thinkingLine(m.thinkingNow, true), "\n"), m.transcriptWidth())...)
	}
	if m.streaming != "" {
		lines = append(append([]string{}, lines...),
			wrapAll(strings.Split(m.streaming, "\n"), m.transcriptWidth())...)
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
