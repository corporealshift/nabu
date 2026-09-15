package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/corporealshift/nabu/daemon/module"
	"io"
	"log/slog"
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

// ---------------------------------------------------------------------------
// The prompt and the reply

func TestPromptCarriesTheCriteriaTheIndexAndTheWindow(t *testing.T) {
	existing := []Memory{
		{Name: "rancher-desktop", Description: "the owner runs Rancher Desktop", Type: "user"},
	}
	w := window([]protocol.Event{userMsg("01A", "always use make ship to deploy")}, "")

	p := curatorPrompt(existing, w)

	// The wording that governs when to save is the same wording the session
	// prefix uses. Two versions of it would drift apart.
	if !strings.Contains(p, SaveInstructions) {
		t.Error("the prompt does not carry the save instructions")
	}
	if !strings.Contains(p, "rancher-desktop") {
		t.Error("the prompt does not list what is already remembered, so it will duplicate")
	}
	if !strings.Contains(p, "make ship") {
		t.Error("the prompt does not carry the window")
	}
}

func TestParseProposals(t *testing.T) {
	good := `[{"scope":"workspace","name":"deploy-command","type":"project",
	  "description":"deploy with make ship","body":"Use make ship, never npm publish."}]`

	for _, tc := range []struct{ name, reply string }{
		{"bare json", good},
		{"in a fence", "```json\n" + good + "\n```"},
		{"with prose", "Here is what I found:\n" + good + "\nThat is all."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseProposals(tc.reply, 3)
			if len(got) != 1 {
				t.Fatalf("got %d proposals, want 1: %+v", len(got), got)
			}
			if got[0].Name != "deploy-command" || got[0].Scope != "workspace" {
				t.Errorf("proposal = %+v", got[0])
			}
		})
	}
}

func TestParseProposalsDropsWhatItCannotUse(t *testing.T) {
	reply := `[
	  {"scope":"workspace","name":"good","type":"project","description":"d","body":"b"},
	  {"scope":"workspace","name":"bad-type","type":"notes","description":"d","body":"b"},
	  {"scope":"workspace","name":"","type":"project","description":"d","body":"b"},
	  {"scope":"workspace","name":"no-body","type":"project","description":"d","body":"  "},
	  {"scope":"elsewhere","name":"bad-scope","type":"project","description":"d","body":"b"}
	]`
	got := parseProposals(reply, 3)
	if len(got) != 1 || got[0].Name != "good" {
		t.Fatalf("got %+v, want only the usable one", got)
	}
}

func TestParseProposalsTruncatesRatherThanRejects(t *testing.T) {
	var parts []string
	for i := 0; i < 6; i++ {
		parts = append(parts, fmt.Sprintf(
			`{"scope":"global","name":"m%d","type":"user","description":"d","body":"b"}`, i))
	}
	got := parseProposals("["+strings.Join(parts, ",")+"]", 3)
	if len(got) != 3 {
		t.Fatalf("got %d, want 3: finding too much is not a reason to keep none", len(got))
	}
	if got[0].Name != "m0" {
		t.Errorf("truncation should keep the first three, got %+v", got)
	}
}

func TestParseProposalsOnRubbish(t *testing.T) {
	for _, reply := range []string{"", "nothing worth saving", "{not json", "[]", "null"} {
		if got := parseProposals(reply, 3); len(got) != 0 {
			t.Errorf("reply %q produced %+v, want nothing", reply, got)
		}
	}
}

// ---------------------------------------------------------------------------
// Applying the pass

// recordingHost supplies a scripted model and records tool calls, which is how
// the curator is required to write.
type recordingHost struct {
	model *scriptedModel
	tools *recordingTools
}

// Model returns a nil interface rather than a typed nil pointer when there is
// no model, which is what a host with no provider configured looks like.
func (h *recordingHost) Model() module.Model {
	if h.model == nil {
		return nil
	}
	return h.model
}

func (h *recordingHost) Tools() module.ToolCaller       { return h.tools }
func (h *recordingHost) UI() module.UI                  { return nil }
func (h *recordingHost) DataDir(string) (string, error) { return "", nil }
func (h *recordingHost) Log() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type scriptedModel struct {
	content string
	err     error
	calls   int
	lastReq module.CompletionRequest
}

func (m *scriptedModel) Complete(_ context.Context, _ module.Session, req module.CompletionRequest) (module.CompletionResponse, error) {
	m.calls++
	m.lastReq = req
	if m.err != nil {
		return module.CompletionResponse{}, m.err
	}
	return module.CompletionResponse{Content: m.content}, nil
}

// recordingTools routes back into the module's own tools, so a curator write
// exercises the real save path rather than a stub of it.
type recordingTools struct {
	mod   *Module
	calls []string
	fail  error
}

func (r *recordingTools) Call(ctx context.Context, s module.Session, tool string, args json.RawMessage) (protocol.ToolResultData, error) {
	r.calls = append(r.calls, tool+" "+string(args))
	if r.fail != nil {
		return protocol.ToolResultData{}, r.fail
	}
	for _, t := range r.mod.Tools() {
		if t.Name == tool {
			out, err := t.Run(ctx, s, args)
			return protocol.ToolResultData{Tool: tool, Content: out}, err
		}
	}
	return protocol.ToolResultData{}, fmt.Errorf("no tool %q", tool)
}

// curatorModule builds a module with a scripted curator model.
func curatorModule(t *testing.T, reply string, cfg module.Config) (*Module, *recordingHost) {
	t.Helper()
	m := &Module{Root: t.TempDir(), NoGit: true}
	h := &recordingHost{model: &scriptedModel{content: reply}}
	h.tools = &recordingTools{mod: m}
	if cfg == nil {
		cfg = module.Config{}
	}
	if err := m.Init(h, cfg); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return m, h
}

func TestPassWritesThroughTheToolAPI(t *testing.T) {
	reply := `[{"scope":"workspace","name":"deploy-command","type":"project",
	  "description":"deploy with make ship","body":"Use make ship, never npm publish."}]`
	m, h := curatorModule(t, reply, nil)
	s := newSession(t, "repo-abc123")
	s.events = []protocol.Event{userMsg("01A", "we always deploy with make ship")}

	m.curate(context.Background(), s)

	if len(h.tools.calls) != 1 || !strings.HasPrefix(h.tools.calls[0], "memory.save ") {
		t.Fatalf("writes did not go through the tool API: %+v", h.tools.calls)
	}
	// And the write actually landed.
	mems, err := m.WorkspaceStore(s).Load()
	if err != nil || len(mems) != 1 || mems[0].Name != "deploy-command" {
		t.Fatalf("Load: %v %+v", err, mems)
	}
}

func TestPassSkippedWhenNothingHappened(t *testing.T) {
	m, h := curatorModule(t, `[]`, nil)
	s := newSession(t, "repo-abc123")
	s.events = []protocol.Event{
		logged("01A", protocol.EventContext, protocol.ContextData{
			Source: "module:memory", Slot: "prefix", Content: "## Memory"}),
	}

	m.curate(context.Background(), s)

	if h.model.calls != 0 {
		t.Errorf("made %d model calls for a session that did nothing", h.model.calls)
	}
}

func TestPassIsSkippedWhenDisabled(t *testing.T) {
	reply := `[{"scope":"global","name":"x","type":"user","description":"d","body":"b"}]`
	m, h := curatorModule(t, reply, module.Config{"curator": false})
	s := newSession(t, "repo-abc123")
	s.events = []protocol.Event{userMsg("01A", "something worth remembering")}

	m.curate(context.Background(), s)

	if h.model.calls != 0 {
		t.Errorf("a disabled curator made %d model calls", h.model.calls)
	}
	if len(h.tools.calls) != 0 {
		t.Errorf("a disabled curator wrote %+v", h.tools.calls)
	}
}

func TestCursorAdvancesSoTwoPassesDoNotRepeat(t *testing.T) {
	m, h := curatorModule(t, `[]`, nil)
	s := newSession(t, "repo-abc123")
	s.events = []protocol.Event{userMsg("01A", "first")}

	m.curate(context.Background(), s)
	if h.model.calls != 1 {
		t.Fatalf("first pass made %d calls, want 1", h.model.calls)
	}
	first := h.model.lastReq.Messages[0].Content

	// Nothing new since: no second pass.
	m.curate(context.Background(), s)
	if h.model.calls != 1 {
		t.Errorf("a pass ran with nothing new since the last one")
	}

	s.events = append(s.events, userMsg("01B", "second"))
	m.curate(context.Background(), s)
	if h.model.calls != 2 {
		t.Fatalf("new events did not produce a pass")
	}
	second := h.model.lastReq.Messages[0].Content
	if strings.Contains(second, "first") {
		t.Errorf("the second pass saw the first pass's events:\n%s", second)
	}
	if first == second {
		t.Error("both passes were asked the same question")
	}
}

func TestAFailedToolCallDoesNotStopTheRest(t *testing.T) {
	reply := `[
	  {"scope":"global","name":"one","type":"user","description":"d","body":"b"},
	  {"scope":"global","name":"two","type":"user","description":"d","body":"b"}
	]`
	m, h := curatorModule(t, reply, nil)
	h.tools.fail = fmt.Errorf("gate refused")
	s := newSession(t, "repo-abc123")
	s.events = []protocol.Event{userMsg("01A", "something")}

	m.curate(context.Background(), s) // must not panic

	if len(h.tools.calls) != 2 {
		t.Errorf("a refused write stopped the others: %+v", h.tools.calls)
	}
}

func TestNoModelMeansNoPassAndNoError(t *testing.T) {
	m := &Module{Root: t.TempDir(), NoGit: true}
	h := &recordingHost{}
	h.tools = &recordingTools{mod: m}
	if err := m.Init(h, module.Config{}); err != nil {
		t.Fatal(err)
	}
	s := newSession(t, "repo-abc123")
	s.events = []protocol.Event{userMsg("01A", "something")}

	m.curate(context.Background(), s) // no model configured

	if len(h.tools.calls) != 0 {
		t.Errorf("wrote without a model: %+v", h.tools.calls)
	}
}

func TestSessionEndCuratesBeforeCommitting(t *testing.T) {
	needGit(t)
	reply := `[{"scope":"global","name":"learned","type":"user","description":"d","body":"a fact"}]`
	m := &Module{Root: t.TempDir()}
	h := &recordingHost{model: &scriptedModel{content: reply}}
	h.tools = &recordingTools{mod: m}
	if err := m.Init(h, module.Config{}); err != nil {
		t.Fatal(err)
	}
	s := newSession(t, "repo-abc123")
	s.events = []protocol.Event{userMsg("01A", "remember this")}

	m.SessionEnd(context.Background(), s)

	// The curator's write must be inside the session's own commit, not
	// trailing into the next one.
	files := gitOut(t, m.Root, "show", "--name-only", "--format=", "HEAD")
	if !strings.Contains(files, "learned.md") {
		t.Errorf("the curator's write is not in the commit:\n%s", files)
	}
}
