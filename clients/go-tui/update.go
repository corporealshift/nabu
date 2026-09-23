package tui

import (
	"encoding/json"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/corporealshift/nabu/clients/goclient"
	"github.com/corporealshift/nabu/protocol"
)

// tickInterval is how often the working indicator advances. Fast enough to
// read as motion, slow enough not to churn the terminal.
const tickInterval = 450 * time.Millisecond

// Messages the client goroutine sends into the program.
type (
	// eventMsg is one logged session event.
	eventMsg struct{ ev protocol.Event }
	// eventsMsg is a catch-up batch. Rendering it at once avoids rebuilding the
	// entire viewport once per event when attaching to a long session.
	eventsMsg struct{ events []protocol.Event }
	// askMsg is a question from the agent, waiting on an answer.
	askMsg struct{ q question }

	// deltaMsg is streaming assistant text. Ephemeral: never logged.
	deltaMsg struct{ d goclient.SessionDelta }

	// thinkingMsg is streaming reasoning, so a slow turn can be watched
	// rather than waited out. The thinking event supersedes it.
	thinkingMsg struct{ d goclient.SessionDelta }
	// promptMsg is a permission request awaiting an answer.
	promptMsg struct{ p prompt }
	// resolvedMsg says a request was answered elsewhere, so dismiss it.
	resolvedMsg struct{ requestID string }
	// connMsg reports a change in the connection.
	connMsg struct {
		state connState
		note  string
	}
	// sessionsMsg carries the session list for the picker: the active ones, or
	// the archive.
	sessionsMsg struct {
		sessions []goclient.SessionSummary
		archived bool
	}
	// errMsg is something the user should see that did not come from the log.
	errMsg struct{ text string }
	// noteMsg is the same, for something that went right. It is separate from
	// errMsg so a success does not leave lastError set behind it.
	noteMsg struct{ text string }
	// compactedMsg is the end of a requested compaction: the mode that ran,
	// or why none did.
	compactedMsg struct{ mode, err string }
	// tickMsg advances the working indicator.
	tickMsg time.Time
)

// tick schedules the next indicator frame.
func tick() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Init() tea.Cmd { return tick() }

// Update is the whole of the TUI's behaviour, and a pure function of the model
// and one message, so every case below is directly testable.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.relayout()
		m.refresh()
		return m, nil

	case tea.KeyMsg:
		return m.onKey(msg)

	case tickMsg:
		m.spinner++
		return m, tick()

	case eventMsg:
		m.appendEvent(msg.ev)
		return m, nil

	case eventsMsg:
		m.appendEvents(msg.events)
		return m, nil

	case deltaMsg:
		m.addDelta(msg.d)
		return m, nil

	case thinkingMsg:
		m.addThinking(msg.d)
		return m, nil

	case promptMsg:
		m.enqueue(msg.p)
		return m, nil

	case askMsg:
		q := msg.q
		m.asking = &q
		// The panel is taller than the composer it replaces.
		m.relayout()
		m.refresh()
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

	case statsMsg:
		v := msg.view
		m.stats = &v
		return m, nil

	case sessionsMsg:
		m.sessions = msg.sessions
		m.pickingArchived = msg.archived
		if m.cursorAt >= len(m.sessions) {
			m.cursorAt = 0
		}
		m.picking = true
		return m, nil

	case errMsg:
		m.lastError = msg.text
		m.note(msg.text)
		return m, nil

	case noteMsg:
		m.note(msg.text)
		return m, nil

	case compactedMsg:
		m.compactingSince = time.Time{}
		if msg.err != "" {
			m.lastError = msg.err
			m.note(msg.err)
		} else {
			m.note("compacted (" + msg.mode + ")")
		}
		return m, nil
	}
	return m, nil
}

// onKey routes a keypress to whatever is in front: a permission prompt, the
// picker, the composer, or the transcript.
func (m model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case m.pending != nil:
		return m.onPromptKey(msg)
	case m.asking != nil:
		return m.answerQuestion(msg)
	case m.stats != nil:
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "esc", "q", "enter":
			m.stats = nil
		}
		return m, nil
	case m.picking:
		return m.onPickerKey(msg)
	case m.composing:
		return m.onComposerKey(msg)
	default:
		return m.onTranscriptKey(msg)
	}
}

// onPromptKey handles the permission overlay, which takes every key so a stray
// keystroke cannot scroll instead of answering.
func (m model) onPromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

func (m model) onPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "s", "q":
		m.picking = false
		return m, nil
	case "up", "k":
		if m.cursorAt > 0 {
			m.cursorAt--
		}
		return m, nil
	case "down", "j":
		if m.cursorAt < len(m.sessions)-1 {
			m.cursorAt++
		}
		return m, nil
	case "tab":
		// Between the sessions in use and the archive (issue 56).
		kind := actListArchived
		if m.pickingArchived {
			kind = actListSessions
		}
		m.cursorAt = 0
		return m, m.emit(action{kind: kind})
	case "a":
		if m.pickingArchived || m.cursorAt >= len(m.sessions) {
			return m, nil
		}
		chosen := m.sessions[m.cursorAt].SessionID
		return m, m.emit(action{kind: actArchive, sessionID: chosen})
	case "r", "enter":
		if m.cursorAt >= len(m.sessions) || (msg.String() == "r" && !m.pickingArchived) {
			return m, nil
		}
		m.picking = false
		chosen := m.sessions[m.cursorAt].SessionID
		if m.pickingArchived {
			// Restoring is for reading it again, so it attaches too.
			m.reset(chosen)
			m.note("restoring and attaching to " + shortID(chosen))
			return m, m.emit(action{kind: actRestore, sessionID: chosen})
		}
		if chosen == m.sessionID {
			return m, nil // already attached; resetting would lose the view for nothing
		}
		m.reset(chosen)
		m.note("attaching to " + shortID(chosen))
		return m, m.emit(action{kind: actAttach, sessionID: chosen})
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

// onComposerKey edits the input line. Every printable key belongs to the
// composer while it is open, so q types a letter rather than quitting.
func (m model) onComposerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.composing, m.input = false, ""
		return m, nil
	case tea.KeyCtrlC:
		m.quitting = true
		return m, tea.Quit
	case tea.KeyEnter:
		return m.submit()
	case tea.KeyBackspace:
		// Runes, not bytes: half an accented character is not a character.
		if runes := []rune(m.input); len(runes) > 0 {
			m.input = string(runes[:len(runes)-1])
		}
		m.relayout()
		return m, nil
	case tea.KeySpace:
		m.input += " "
		m.relayout()
		return m, nil
	case tea.KeyRunes:
		m.input += string(msg.Runes)
		m.relayout()
		return m, nil
	}
	return m, nil
}

// relayout gives the viewport whatever the composer is not using. The
// composer grows as it is typed, so this runs on every key, not only on
// a resize.
func (m *model) relayout() {
	m.viewport.Width, m.viewport.Height = m.transcriptWidth(), m.viewportHeight()
}

// submit acts on the composer's contents.
func (m model) submit() (tea.Model, tea.Cmd) {
	line := m.input
	m.composing, m.input = false, ""

	res := parseCommand(line)
	switch {
	case res.note != "":
		m.note(res.note)
		return m, nil
	case res.openPicker:
		return m, m.emit(action{kind: actListSessions})
	case res.act == nil:
		return m, nil
	}

	act := *res.act
	act.sessionID = m.sessionID
	switch act.kind {
	case actPrompt:
		m.note("› " + act.text)
	case actCompact:
		// It is a model call and the transcript does not move while it runs,
		// so without this the client looks like it dropped the command. On a
		// local model it is minutes, not seconds (issue 87).
		if m.compacting() {
			m.note("already compacting")
			return m, nil
		}
		m.compactingSince = time.Now()
		m.note("compacting the history — a model call, so this can take minutes · ctrl+x stops it")
	}
	return m, m.emit(act)
}

func (m model) onTranscriptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		// Quitting closes the view, never the run. The daemon owns the
		// session and it keeps going.
		m.quitting = true
		return m, tea.Quit
	case "i", "enter":
		m.composing = true
		return m, nil
	case "t":
		m.toggleThinking()
		return m, nil
	case "p":
		m.hideTasks = !m.hideTasks
		m.relayout()
		m.refresh()
		return m, nil
	case "s":
		return m, m.emit(action{kind: actListSessions})
	case "ctrl+x":
		// Deliberately not ctrl+c: quitting and interrupting are opposite
		// intentions and confusing them is expensive.
		if !m.working() && !m.compacting() {
			m.note("nothing to interrupt — the session is not running")
			return m, nil
		}
		m.note("interrupting the current turn")
		return m, m.emit(action{kind: actInterrupt, sessionID: m.sessionID})
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

// answerQuestion edits the answer, and sends it when it is finished. Every
// printable key belongs to the answer, so the agent is never left waiting
// because a keystroke was read as a command.
func (m model) answerQuestion(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Ctrl-C still quits: being asked a question must not trap anyone in the
	// client.
	if msg.Type == tea.KeyCtrlC {
		m.quitting = true
		return m, tea.Quit
	}

	next, answer, done := m.onAskKey(msg.String(), msg.Runes)
	if !done {
		return next, nil
	}

	id := next.asking.id
	next.asking = nil
	next.relayout()
	next.note("answered: " + answer)
	return next, next.emit(action{kind: actAnswerAsk, id: id, text: answer})
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

	return m, m.emit(action{
		kind: actAnswer, id: p.id, approve: approve, reason: reason,
		sessionID: m.sessionID,
	})
}

// emit hands an action to the connection goroutine. The channel lives on the
// model because Update is pure and cannot touch the network itself.
func (m model) emit(a action) tea.Cmd {
	ch := m.actions
	return func() tea.Msg {
		if ch != nil {
			ch <- a
		}
		return nil
	}
}

// unmarshal decodes an event's data.
func unmarshal(ev protocol.Event, v any) error {
	return json.Unmarshal(ev.Data, v)
}
