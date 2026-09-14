package main

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/corporealshift/nabu/clients/goclient"
	"github.com/corporealshift/nabu/protocol"
)

// running puts the model into a running turn.
func running(t *testing.T, m model) model {
	t.Helper()
	from := protocol.StateIdle
	m, _ = send(m, eventMsg{ev: event("e1", protocol.EventStateChange,
		protocol.StateChangeData{From: &from, To: protocol.StateRunning})})
	return m
}

// typeIn feeds text to the composer one key at a time.
func typeIn(m model, text string) model {
	for _, r := range text {
		if r == ' ' {
			m, _ = send(m, tea.KeyMsg{Type: tea.KeySpace})
			continue
		}
		m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

// stripANSI removes styling so a test can assert on the text itself.
func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEscape = true
		case inEscape && (r == 'm' || r == 'K'):
			inEscape = false
		case !inEscape:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// The working indicator

// A screen that has stopped changing is indistinguishable from a hang, and a
// local turn can take minutes before its first token.
func TestWorkingIndicatorOnlyWhileRunning(t *testing.T) {
	m := running(t, sized(t, nil))
	if !strings.Contains(m.status(), "working") {
		t.Fatalf("a running session must say so, got %q", m.status())
	}

	m, _ = send(m, eventMsg{ev: event("e2", protocol.EventStateChange,
		protocol.StateChangeData{To: protocol.StateIdle})})
	if strings.Contains(m.status(), "working") {
		t.Errorf("a finished session must not claim to be working, got %q", m.status())
	}
}

func TestSpinnerAdvancesOnTick(t *testing.T) {
	m := running(t, sized(t, nil))
	first := m.workingIndicator()
	m, _ = send(m, tickMsg(time.Now()))
	if second := m.workingIndicator(); second == first {
		t.Error("the spinner should advance, or a still screen looks dead")
	}
}

// The clock runs from the turn starting, not from the program opening.
func TestElapsedMeasuresTheTurn(t *testing.T) {
	m := sized(t, nil)
	if m.elapsed() != 0 {
		t.Error("an idle session has no elapsed turn")
	}
	m = running(t, m)
	if m.runningSince.IsZero() {
		t.Fatal("entering running should start the clock")
	}
	m, _ = send(m, eventMsg{ev: event("e2", protocol.EventStateChange,
		protocol.StateChangeData{To: protocol.StateIdle})})
	if !m.runningSince.IsZero() {
		t.Error("leaving running should stop the clock")
	}
}

// A turn that has started producing text is still working.
func TestIndicatorSurvivesTheFirstDelta(t *testing.T) {
	m := running(t, sized(t, nil))
	m, _ = send(m, deltaMsg{d: goclient.SessionDelta{TurnID: "t1", Text: "thinking"}})
	if !strings.Contains(m.status(), "working") {
		t.Error("the indicator should stay once tokens start arriving")
	}
}

func TestTickWithNoRunningSessionChangesNothing(t *testing.T) {
	m := sized(t, nil)
	before := m.status()
	m, _ = send(m, tickMsg(time.Now()))
	if m.status() != before {
		t.Error("a tick on an idle session should change nothing visible")
	}
}

// ---------------------------------------------------------------------------
// Interrupt

func TestInterruptWhileRunning(t *testing.T) {
	actions := make(chan action, 2)
	m := running(t, sized(t, actions))

	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlX})
	if cmd == nil {
		t.Fatal("ctrl+x should produce an action")
	}
	cmd()
	a := <-actions
	if a.kind != actInterrupt {
		t.Errorf("kind: got %v, want interrupt", a.kind)
	}
	if m.quitting {
		t.Error("interrupting must not quit")
	}
	if !strings.Contains(m.body(), "interrupting") {
		t.Error("the transcript should record the interrupt")
	}
}

func TestInterruptWhenIdleDoesNothing(t *testing.T) {
	actions := make(chan action, 2)
	m := sized(t, actions)
	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlX})
	if cmd != nil {
		cmd()
	}
	select {
	case a := <-actions:
		t.Fatalf("nothing should be sent when idle, got %v", a.kind)
	default:
	}
	if !strings.Contains(m.body(), "nothing to interrupt") {
		t.Error("a keypress that does nothing should say so")
	}
}

// ---------------------------------------------------------------------------
// The composer

func TestComposerSendsAPrompt(t *testing.T) {
	actions := make(chan action, 2)
	m := sized(t, actions)

	m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if !m.composing {
		t.Fatal("i should open the composer")
	}
	m = typeIn(m, "fix the build")
	if m.input != "fix the build" {
		t.Fatalf("input: got %q", m.input)
	}

	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyEnter})
	cmd()
	a := <-actions
	if a.kind != actPrompt || a.text != "fix the build" {
		t.Fatalf("action: %+v", a)
	}
	if m.composing {
		t.Error("submitting should close the composer")
	}
}

// While the composer is open, q is a letter.
func TestComposerCapturesLetters(t *testing.T) {
	m := sized(t, nil)
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = typeIn(m, "q")
	if m.quitting {
		t.Fatal("q must not quit while typing")
	}
	if m.input != "q" {
		t.Errorf("input: got %q, want %q", m.input, "q")
	}
}

func TestComposerBackspaceAndCancel(t *testing.T) {
	m := sized(t, nil)
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = typeIn(m, "abc")
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.input != "ab" {
		t.Errorf("after backspace: got %q", m.input)
	}
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.composing || m.input != "" {
		t.Error("esc should close and discard")
	}
}

func TestEmptyComposerSendsNothing(t *testing.T) {
	actions := make(chan action, 1)
	m := sized(t, actions)
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = typeIn(m, "   ")
	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		cmd()
	}
	select {
	case a := <-actions:
		t.Fatalf("whitespace should send nothing, got %v", a.kind)
	default:
	}
	if m.composing {
		t.Error("the composer should close")
	}
}

// A permission prompt takes precedence over everything.
func TestPromptBlocksTheComposer(t *testing.T) {
	m := sized(t, nil)
	m, _ = send(m, promptMsg{p: prompt{id: "r1", req: goclient.PermissionRequest{RequestID: "r1", Summary: "x"}}})
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if m.composing {
		t.Error("the composer must not open while a permission prompt is up")
	}
}

// Spec 7: send_prompt does not queue, so steering a running session is allowed.
func TestPromptCanBeSentWhileRunning(t *testing.T) {
	actions := make(chan action, 2)
	m := running(t, sized(t, actions))
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = typeIn(m, "actually do it differently")
	_, cmd := send(m, tea.KeyMsg{Type: tea.KeyEnter})
	cmd()
	if a := <-actions; a.kind != actPrompt {
		t.Errorf("steering a running session should send a prompt, got %v", a.kind)
	}
}

// ---------------------------------------------------------------------------
// The session picker

func TestPickerAttachesAndResets(t *testing.T) {
	actions := make(chan action, 2)
	m := sized(t, actions)
	m, _ = send(m, eventMsg{ev: event("e1", protocol.EventMessage,
		protocol.MessageData{Role: "user", Content: "old session text"})})

	m, _ = send(m, sessionsMsg{sessions: []goclient.SessionSummary{
		{SessionID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", State: "idle", Workspace: "/a"},
		{SessionID: "01BRZ3NDEKTSV4RRFFQ69G5FAV", State: "running", Workspace: "/b"},
	}})
	if !m.picking {
		t.Fatal("the picker should open")
	}

	m, _ = send(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.cursorAt != 1 {
		t.Fatalf("cursor: got %d, want 1", m.cursorAt)
	}
	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyEnter})
	cmd()

	a := <-actions
	if a.kind != actAttach || a.sessionID != "01BRZ3NDEKTSV4RRFFQ69G5FAV" {
		t.Fatalf("action: %+v", a)
	}
	if strings.Contains(m.body(), "old session text") {
		t.Error("switching sessions must clear the previous transcript")
	}
	if m.lastEventID != "" {
		t.Error("switching sessions must reset the cursor")
	}
	if m.picking {
		t.Error("the picker should close")
	}
}

// Re-attaching the session already attached would clear the view for nothing.
func TestPickingTheCurrentSessionIsANoOp(t *testing.T) {
	actions := make(chan action, 1)
	m := sized(t, actions)
	m, _ = send(m, eventMsg{ev: event("e1", protocol.EventMessage,
		protocol.MessageData{Role: "user", Content: "keep me"})})
	m, _ = send(m, sessionsMsg{sessions: []goclient.SessionSummary{
		{SessionID: m.sessionID, State: "idle", Workspace: "/a"},
	}})
	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		cmd()
	}
	select {
	case a := <-actions:
		t.Fatalf("no action expected, got %v", a.kind)
	default:
	}
	if !strings.Contains(m.body(), "keep me") {
		t.Error("the transcript should be untouched")
	}
}

func TestPickerCursorStopsAtTheEnds(t *testing.T) {
	m := sized(t, nil)
	m, _ = send(m, sessionsMsg{sessions: []goclient.SessionSummary{
		{SessionID: "a"}, {SessionID: "b"},
	}})
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.cursorAt != 0 {
		t.Errorf("cursor should stop at the top, got %d", m.cursorAt)
	}
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.cursorAt != 1 {
		t.Errorf("cursor should stop at the bottom, got %d", m.cursorAt)
	}
}

func TestEmptyPickerSaysSo(t *testing.T) {
	m := sized(t, nil)
	m, _ = send(m, sessionsMsg{sessions: nil})
	if !strings.Contains(m.View(), "no sessions") {
		t.Error("an empty list should say so rather than show an empty box")
	}
}

// ---------------------------------------------------------------------------
// Tasks and the goal

func TestTasksUpdateThePaneNotTheTranscript(t *testing.T) {
	m := sized(t, nil)
	m, _ = send(m, tea.WindowSizeMsg{Width: 120, Height: 30})
	before := len(m.transcript)

	m, _ = send(m, eventMsg{ev: event("e1", protocol.EventTasks,
		protocol.TasksData{Tasks: []protocol.Task{
			{ID: "t1", Title: "write the test", Status: protocol.TaskDone},
			{ID: "t2", Title: "make it pass", Status: protocol.TaskInProgress},
		}})})

	if len(m.tasks) != 2 {
		t.Fatalf("tasks: got %d, want 2", len(m.tasks))
	}
	if len(m.transcript) != before {
		t.Error("a tasks event must not add transcript lines")
	}
	pane := m.taskPane()
	if !strings.Contains(pane, "write the test") || !strings.Contains(pane, "make it pass") {
		t.Error("the pane should list the tasks")
	}
}

// Two columns in eighty make both unreadable.
func TestTaskPaneHiddenOnANarrowTerminal(t *testing.T) {
	m := sized(t, nil)
	m, _ = send(m, eventMsg{ev: event("e1", protocol.EventTasks,
		protocol.TasksData{Tasks: []protocol.Task{{ID: "t1", Title: "x", Status: protocol.TaskDone}}})})

	m, _ = send(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.showTasks() {
		t.Error("the pane should be hidden at 80 columns")
	}
	m, _ = send(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	if !m.showTasks() {
		t.Error("the pane should show at 120 columns")
	}
}

func TestTaskPaneHiddenWithNoTasks(t *testing.T) {
	m := sized(t, nil)
	m, _ = send(m, tea.WindowSizeMsg{Width: 200, Height: 40})
	if m.showTasks() {
		t.Error("no tasks means no pane, however wide the terminal")
	}
}

// Marks differ in shape, not only colour, so the pane reads without it.
func TestTaskMarksAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, st := range []protocol.TaskStatus{
		protocol.TaskPending, protocol.TaskInProgress,
		protocol.TaskDone, protocol.TaskBlocked, protocol.TaskFailed,
	} {
		line := taskLine(protocol.Task{ID: "t", Title: "x", Status: st})
		mark := strings.Fields(stripANSI(line))
		key := ""
		if len(mark) > 0 && mark[0] != "x" {
			key = mark[0]
		}
		if seen[key] {
			t.Errorf("status %q reuses the mark %q", st, key)
		}
		seen[key] = true
	}
}

func TestGoalBadge(t *testing.T) {
	m := sized(t, nil)
	if m.goalBadge() != "" {
		t.Error("no goal means no badge")
	}

	m, _ = send(m, eventMsg{ev: event("e1", protocol.EventGoal,
		protocol.GoalData{Condition: "the gate is green", State: "set"})})
	if !strings.Contains(m.status(), "goal") {
		t.Error("a set goal should appear in the status bar")
	}

	m, _ = send(m, eventMsg{ev: event("e2", protocol.EventGoal,
		protocol.GoalData{Condition: "the gate is green", State: "unmet"})})
	if !strings.Contains(m.goalBadge(), "unmet") {
		t.Error("an unmet goal is the interesting one and must be visible")
	}

	m, _ = send(m, eventMsg{ev: event("e3", protocol.EventGoal,
		protocol.GoalData{Condition: "the gate is green", State: "cleared"})})
	if m.goalBadge() != "" {
		t.Error("a cleared goal should remove the badge")
	}
}
