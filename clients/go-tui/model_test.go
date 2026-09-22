package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/corporealshift/nabu/clients/goclient"
	"github.com/corporealshift/nabu/protocol"
)

// event builds a logged event with the given id, type and payload.
func event(id string, t protocol.EventType, data any) protocol.Event {
	raw, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	return protocol.Event{ID: id, Type: t, Data: raw}
}

// sized returns a model that has been given a window, so the viewport exists.
func sized(t *testing.T, actions chan action) model {
	t.Helper()
	m := newModel("01ARZ3NDEKTSV4RRFFQ69G5FAV", actions)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return next.(model)
}

// send drives the update function and returns the new model and any command.
func send(m model, msg tea.Msg) (model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(model), cmd
}

func TestEventsAppearInTheTranscript(t *testing.T) {
	m := sized(t, nil)
	m, _ = send(m, eventMsg{ev: event("e1", protocol.EventMessage,
		protocol.MessageData{Role: "user", Content: "fix the build"})})
	m, _ = send(m, eventMsg{ev: event("e2", protocol.EventMessage,
		protocol.MessageData{Role: "assistant", Content: "on it"})})

	body := m.body()
	if !strings.Contains(body, "fix the build") {
		t.Error("the user message should appear")
	}
	if !strings.Contains(body, "on it") {
		t.Error("the assistant message should appear")
	}
	if m.lastEventID != "e2" {
		t.Errorf("cursor: got %q, want e2", m.lastEventID)
	}
}

// The cursor only moves forward, so an event arriving from both the live
// stream and a replay is rendered once.
func TestDuplicateEventsAreIgnored(t *testing.T) {
	m := sized(t, nil)
	ev := event("e1", protocol.EventMessage,
		protocol.MessageData{Role: "user", Content: "hello"})

	m, _ = send(m, eventMsg{ev: ev})
	before := len(m.transcript)
	m, _ = send(m, eventMsg{ev: ev})

	if len(m.transcript) != before {
		t.Errorf("a replayed event should not render twice: %d then %d", before, len(m.transcript))
	}
}

func TestOlderEventIsIgnored(t *testing.T) {
	m := sized(t, nil)
	m, _ = send(m, eventMsg{ev: event("e5", protocol.EventMessage,
		protocol.MessageData{Role: "user", Content: "newer"})})
	before := len(m.transcript)
	m, _ = send(m, eventMsg{ev: event("e2", protocol.EventMessage,
		protocol.MessageData{Role: "user", Content: "older"})})

	if len(m.transcript) != before {
		t.Error("an event behind the cursor should not render")
	}
	if strings.Contains(m.body(), "older") {
		t.Error("the older event leaked into the transcript")
	}
}

func TestReplayBatchAppliesEventsInOrder(t *testing.T) {
	m := sized(t, nil)
	from := protocol.StateIdle
	m, _ = send(m, eventsMsg{events: []protocol.Event{
		event("e1", protocol.EventMessage, protocol.MessageData{Role: "user", Content: "first"}),
		event("e2", protocol.EventStateChange, protocol.StateChangeData{From: &from, To: protocol.StateRunning}),
		event("e3", protocol.EventMessage, protocol.MessageData{Role: "assistant", Content: "second"}),
	}})

	body := m.body()
	if !strings.Contains(body, "first") || !strings.Contains(body, "second") {
		t.Errorf("replay batch must render every event, got %q", body)
	}
	if m.lastEventID != "e3" {
		t.Errorf("cursor: got %q, want e3", m.lastEventID)
	}
	if m.state != protocol.StateRunning {
		t.Errorf("state: got %q, want running", m.state)
	}
}

// Deltas are ephemeral. The final message carries the whole text, so the live
// preview must be cleared or the text appears twice.
func TestDeltaPreviewIsReplacedByTheFinalMessage(t *testing.T) {
	m := sized(t, nil)
	m, _ = send(m, deltaMsg{d: goclient.SessionDelta{TurnID: "t1", Text: "partial "}})
	m, _ = send(m, deltaMsg{d: goclient.SessionDelta{TurnID: "t1", Text: "answer"}})

	if !strings.Contains(m.body(), "partial answer") {
		t.Fatalf("streaming text should be visible, got %q", m.body())
	}

	m, _ = send(m, eventMsg{ev: event("e1", protocol.EventMessage,
		protocol.MessageData{Role: "assistant", Content: "partial answer"})})

	if m.streaming != "" {
		t.Error("the preview should be cleared once the message arrives")
	}
	if n := strings.Count(m.body(), "partial answer"); n != 1 {
		t.Errorf("the text should appear once, appeared %d times", n)
	}
}

// A new turn discards the previous turn's unfinished preview.
func TestDeltaFromANewTurnResetsThePreview(t *testing.T) {
	m := sized(t, nil)
	m, _ = send(m, deltaMsg{d: goclient.SessionDelta{TurnID: "t1", Text: "first"}})
	m, _ = send(m, deltaMsg{d: goclient.SessionDelta{TurnID: "t2", Text: "second"}})

	if strings.Contains(m.streaming, "first") {
		t.Error("a new turn should start a fresh preview")
	}
	if m.streaming != "second" {
		t.Errorf("streaming: got %q, want %q", m.streaming, "second")
	}
}

func TestStateChangeUpdatesTheBadge(t *testing.T) {
	m := sized(t, nil)
	from := protocol.StateIdle
	m, _ = send(m, eventMsg{ev: event("e1", protocol.EventStateChange,
		protocol.StateChangeData{From: &from, To: protocol.StateRunning})})
	if m.state != protocol.StateRunning {
		t.Errorf("state: got %q, want running", m.state)
	}
	// While running, the working indicator stands in for the state badge: it
	// says the same thing and adds elapsed time.
	if !strings.Contains(m.status(), "working") {
		t.Errorf("a running session should show the working indicator, got %q", m.status())
	}
}

// The overlay takes the screen, because approving something you did not read
// is the failure it exists to prevent.
func TestPermissionOverlayTakesTheScreen(t *testing.T) {
	m := sized(t, nil)
	m, _ = send(m, promptMsg{p: prompt{id: "r1", req: goclient.PermissionRequest{
		RequestID: "r1", Tool: "bash", Summary: "rm -rf /tmp/build", Risk: "high"}}})

	view := m.View()
	if !strings.Contains(view, "Permission needed") {
		t.Error("the overlay should be shown")
	}
	if !strings.Contains(view, "rm -rf /tmp/build") {
		t.Error("the overlay should show what is being asked")
	}
	if !strings.Contains(strings.ToLower(view), "high") {
		t.Error("the overlay should show the risk tier")
	}
}

func TestApprovingSendsAnAnswer(t *testing.T) {
	actions := make(chan action, 4)
	m := sized(t, actions)
	m, _ = send(m, promptMsg{p: prompt{id: "r1", req: goclient.PermissionRequest{
		RequestID: "r1", Tool: "bash", Summary: "go test ./...", Risk: "low"}}})

	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("approving should produce a command")
	}
	cmd()

	select {
	case a := <-actions:
		if !a.approve {
			t.Error("y should approve")
		}
		if a.id != "r1" {
			t.Errorf("id: got %v, want r1", a.id)
		}
	default:
		t.Fatal("no answer was sent")
	}
	if m.pending != nil {
		t.Error("the overlay should be dismissed after answering")
	}
}

func TestDenyingSendsARefusalWithAReason(t *testing.T) {
	actions := make(chan action, 4)
	m := sized(t, actions)
	m, _ = send(m, promptMsg{p: prompt{id: "r1", req: goclient.PermissionRequest{
		RequestID: "r1", Tool: "bash", Summary: "rm -rf /", Risk: "high"}}})

	_, cmd := send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if cmd == nil {
		t.Fatal("denying should produce a command")
	}
	cmd()

	a := <-actions
	if a.approve {
		t.Error("n should deny")
	}
	if a.reason == "" {
		t.Error("a denial should say why; the model sees it as the tool result")
	}
}

// Answering the wrong request would approve something the human never saw.
func TestPromptsQueueAndAreAnsweredInOrder(t *testing.T) {
	actions := make(chan action, 4)
	m := sized(t, actions)
	m, _ = send(m, promptMsg{p: prompt{id: "r1", req: goclient.PermissionRequest{
		RequestID: "r1", Tool: "bash", Summary: "first", Risk: "low"}}})
	m, _ = send(m, promptMsg{p: prompt{id: "r2", req: goclient.PermissionRequest{
		RequestID: "r2", Tool: "write", Summary: "second", Risk: "medium"}}})

	if m.pending == nil || m.pending.req.RequestID != "r1" {
		t.Fatal("the first request should be on screen")
	}
	if len(m.queued) != 1 {
		t.Fatalf("queued: got %d, want 1", len(m.queued))
	}
	if !strings.Contains(m.View(), "1 more waiting") {
		t.Error("the overlay should say another is waiting")
	}

	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	cmd()
	if a := <-actions; a.id != "r1" {
		t.Errorf("the first answer should be for r1, got %v", a.id)
	}
	if m.pending == nil || m.pending.req.RequestID != "r2" {
		t.Fatal("the queued request should be promoted")
	}

	_, cmd = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	cmd()
	if a := <-actions; a.id != "r2" {
		t.Errorf("the second answer should be for r2, got %v", a.id)
	}
}

// A request the daemon resolved elsewhere must leave the screen.
func TestResolvedElsewhereDismissesThePrompt(t *testing.T) {
	m := sized(t, nil)
	m, _ = send(m, promptMsg{p: prompt{id: "r1", req: goclient.PermissionRequest{
		RequestID: "r1", Tool: "bash", Summary: "x", Risk: "low"}}})
	m, _ = send(m, resolvedMsg{requestID: "r1"})

	if m.pending != nil {
		t.Error("a request answered elsewhere should be dismissed")
	}
	if !strings.Contains(m.body(), "already answered") {
		t.Error("the user should be told why the prompt vanished")
	}
}

func TestResolvedElsewhereRemovesAQueuedPrompt(t *testing.T) {
	m := sized(t, nil)
	m, _ = send(m, promptMsg{p: prompt{id: "r1", req: goclient.PermissionRequest{RequestID: "r1", Summary: "a"}}})
	m, _ = send(m, promptMsg{p: prompt{id: "r2", req: goclient.PermissionRequest{RequestID: "r2", Summary: "b"}}})
	m, _ = send(m, resolvedMsg{requestID: "r2"})

	if len(m.queued) != 0 {
		t.Errorf("queued: got %d, want 0", len(m.queued))
	}
	if m.pending == nil || m.pending.req.RequestID != "r1" {
		t.Error("the on-screen prompt should be untouched")
	}
}

// While a prompt is up, a stray key must not scroll instead of answering.
func TestKeysAreCapturedByTheOverlay(t *testing.T) {
	m := sized(t, nil)
	m, _ = send(m, promptMsg{p: prompt{id: "r1", req: goclient.PermissionRequest{RequestID: "r1", Summary: "x"}}})

	m2, cmd := send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if cmd != nil {
		t.Error("an unrelated key should do nothing while a prompt is up")
	}
	if m2.pending == nil {
		t.Error("an unrelated key must not dismiss the prompt")
	}
}

// Quitting closes the view, never the run.
func TestQuitDoesNotEndTheRun(t *testing.T) {
	m := sized(t, nil)
	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("q should quit")
	}
	if !m.quitting {
		t.Error("the model should know it is quitting")
	}
	if !strings.Contains(m.help(), "the run continues") {
		t.Error("the help should say quitting does not stop the run")
	}
}

// A stale view must never be rendered as if it were live.
func TestDisconnectionIsVisible(t *testing.T) {
	m := sized(t, nil)
	m, _ = send(m, connMsg{state: connected})
	if !strings.Contains(m.status(), "connected") {
		t.Error("a live connection should be shown")
	}

	m, _ = send(m, connMsg{state: disconnected, note: "disconnected — reconnecting"})
	status := m.status()
	if !strings.Contains(status, "disconnected") {
		t.Errorf("a dropped connection must be visible, got %q", status)
	}
	if !strings.Contains(m.body(), "reconnecting") {
		t.Error("the transcript should record the disconnection")
	}
}

func TestTerminalStateChangesTheHelp(t *testing.T) {
	m := sized(t, nil)
	to := protocol.StateCompleted
	m, _ = send(m, eventMsg{ev: event("e1", protocol.EventStateChange,
		protocol.StateChangeData{To: to})})
	if !strings.Contains(m.help(), "ended") {
		t.Error("a finished session should say so")
	}
}

func TestViewBeforeSizingDoesNotPanic(t *testing.T) {
	m := newModel("s", nil)
	if got := m.View(); got == "" {
		t.Error("an unsized model should still render something")
	}
}
