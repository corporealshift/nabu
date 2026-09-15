package memory

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/protocol"
)

// logged builds an event with a real id, so cursors can be compared.
func logged(id string, t protocol.EventType, data any) protocol.Event {
	raw, _ := json.Marshal(data)
	return protocol.Event{ID: id, Type: t, Data: raw}
}

func userMsg(id, text string) protocol.Event {
	return logged(id, protocol.EventMessage, protocol.MessageData{Role: "user", Content: text})
}

func assistantMsg(id, text string) protocol.Event {
	return logged(id, protocol.EventMessage, protocol.MessageData{Role: "assistant", Content: text})
}

func toolCall(id, tool string) protocol.Event {
	return logged(id, protocol.EventToolCall, protocol.ToolCallData{
		CallID: id, Tool: tool, Source: "model",
		Arguments: json.RawMessage(`{"path":"README.md"}`)})
}

func TestNothingToLookAtMeansNoPass(t *testing.T) {
	events := []protocol.Event{
		logged("01A", protocol.EventContext, protocol.ContextData{
			Source: "module:memory", Slot: "prefix", Content: "## Memory"}),
	}
	w := window(events, "")
	if w.worthAPass() {
		t.Error("a log with only injected context is not worth a model call")
	}
}

func TestAUserMessageOrAToolCallWarrantsAPass(t *testing.T) {
	if w := window([]protocol.Event{userMsg("01A", "hello")}, ""); !w.worthAPass() {
		t.Error("a user message should warrant a pass")
	}
	if w := window([]protocol.Event{toolCall("01B", "read")}, ""); !w.worthAPass() {
		t.Error("a tool call should warrant a pass")
	}
	only := []protocol.Event{assistantMsg("01C", "sure")}
	if w := window(only, ""); w.worthAPass() {
		t.Error("an assistant message alone is the model talking to itself")
	}
}

func TestWindowStartsAfterTheCursor(t *testing.T) {
	events := []protocol.Event{
		userMsg("01A", "first question"),
		assistantMsg("01B", "first answer"),
		userMsg("01C", "second question"),
		assistantMsg("01D", "second answer"),
	}
	w := window(events, "01B")
	text := w.render()
	if strings.Contains(text, "first question") {
		t.Errorf("the window reaches back past the cursor:\n%s", text)
	}
	if !strings.Contains(text, "second question") {
		t.Errorf("the window is missing events after the cursor:\n%s", text)
	}
	if w.last != "01D" {
		t.Errorf("last = %q, want the final event id", w.last)
	}
}

func TestNoCursorTakesTheWholeLog(t *testing.T) {
	events := []protocol.Event{userMsg("01A", "a question"), assistantMsg("01B", "an answer")}
	w := window(events, "")
	if !strings.Contains(w.render(), "a question") {
		t.Error("with no cursor the whole log is the window")
	}
}

func TestWindowIsCapped(t *testing.T) {
	var events []protocol.Event
	for i := 0; i < maxWindowEvents+50; i++ {
		events = append(events, userMsg(fmt.Sprintf("ID%05d", i),
			"question "+strings.Repeat("x", 40)))
	}
	events = append(events, userMsg("ZZLAST", "the last question"))

	w := window(events, "")
	if len(w.events) > maxWindowEvents {
		t.Errorf("kept %d events, want at most %d", len(w.events), maxWindowEvents)
	}
	text := w.render()
	if len(text) > maxWindowBytes {
		t.Errorf("window is %d bytes, want at most %d", len(text), maxWindowBytes)
	}
	// The most recent is what matters; the oldest is what gets dropped.
	if !strings.Contains(text, "the last question") {
		t.Error("the cap dropped the most recent event")
	}
}

func TestToolResultsAreTruncatedNotDropped(t *testing.T) {
	huge := strings.Repeat("output ", 4000)
	events := []protocol.Event{
		toolCall("01A", "bash"),
		logged("01B", protocol.EventToolResult, protocol.ToolResultData{
			CallID: "01A", Tool: "bash", Content: huge}),
	}
	text := window(events, "").render()
	if !strings.Contains(text, "bash") {
		t.Errorf("the tool result vanished:\n%s", text)
	}
	if len(text) > maxWindowBytes {
		t.Errorf("a single huge result blew the cap: %d bytes", len(text))
	}
	if !strings.Contains(text, "truncated") {
		t.Errorf("truncation should be visible in the text:\n%s", text)
	}
}

func TestRenderCoversTheEventsThatMatter(t *testing.T) {
	events := []protocol.Event{
		userMsg("01A", "do the thing"),
		assistantMsg("01B", "doing it"),
		toolCall("01C", "edit"),
		logged("01D", protocol.EventNotice, protocol.NoticeData{
			Source: "daemon", Level: "warn", Message: "a notice worth seeing"}),
		logged("01E", protocol.EventStopVeto, protocol.StopVetoData{
			Module: "verify", Reason: "the gate failed"}),
		// Context is what this module injected; feeding it back is a loop.
		logged("01F", protocol.EventContext, protocol.ContextData{
			Source: "module:memory", Slot: "prefix", Content: "## Memory index"}),
	}
	text := window(events, "").render()

	for _, want := range []string{"do the thing", "doing it", "edit", "a notice worth seeing", "the gate failed"} {
		if !strings.Contains(text, want) {
			t.Errorf("the window is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "## Memory index") {
		t.Errorf("injected context was fed back to the curator:\n%s", text)
	}
}

func TestTwoPassesDoNotSeeTheSameEvents(t *testing.T) {
	events := []protocol.Event{userMsg("01A", "first"), assistantMsg("01B", "answer")}
	first := window(events, "")
	if !strings.Contains(first.render(), "first") {
		t.Fatal("the first pass should see the first message")
	}

	events = append(events, userMsg("01C", "second"))
	second := window(events, first.last)
	if strings.Contains(second.render(), "first") {
		t.Errorf("the second pass saw what the first already had:\n%s", second.render())
	}
	if !strings.Contains(second.render(), "second") {
		t.Errorf("the second pass missed the new message:\n%s", second.render())
	}
}
