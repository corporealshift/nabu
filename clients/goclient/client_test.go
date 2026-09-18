package goclient

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/corporealshift/nabu/daemon/agent"
	"github.com/corporealshift/nabu/daemon/api"
	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/modules/guard"
	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/daemon/session"
	"github.com/corporealshift/nabu/daemon/tools"
	"github.com/corporealshift/nabu/protocol"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// realDaemon stands up an actual API server on a random loopback port and
// returns its address plus the manager behind it. Testing against the real
// server is the point: a fake would not catch a protocol mismatch.
func realDaemon(t *testing.T) (addr string, m *agent.Manager, dir string) {
	t.Helper()
	dir = t.TempDir()
	store, err := session.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	pr := provider.NewRegistry()
	pr.Add(provider.Config{Name: "fake", ContextWindow: 8000}, &provider.Fake{}, true)
	builtins := &tools.Builtins{}
	g := &guard.Module{}
	mr := module.NewRegistry([]module.Module{builtins, g}, module.Options{Log: discardLogger()})
	mr.Init(
		func(string) module.Host { return nil },
		func(string) module.Config { return module.Config{} },
	)

	h := api.NewHandler(nil, store, discardLogger())
	m, err = agent.New(agent.Deps{
		Store: store, Providers: pr, Modules: mr, Builtins: builtins,
		Root: dir, Log: discardLogger(), Asker: h, Deltas: h.Deltas(),
	}, agent.Config{DefaultModel: "fake/m"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	h.SetManager(m)

	srv := api.NewServer(h, &api.Config{}, discardLogger())
	hs := httptest.NewServer(srv)
	t.Cleanup(hs.Close)
	return strings.TrimPrefix(hs.URL, "http://"), m, dir
}

func dialTest(t *testing.T, addr string) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	c, err := Dial(ctx, addr, "", "test", "0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

func TestDialCompletesTheHandshake(t *testing.T) {
	addr, _, _ := realDaemon(t)
	c := dialTest(t, addr)
	if c.conn == nil {
		t.Fatal("no connection")
	}
}

// A protocol version the daemon refuses must surface as an error, not a hang.
func TestDialRefusedOnProtocolMismatch(t *testing.T) {
	addr, _, _ := realDaemon(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, "ws://"+addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	if err := wsjson.Write(ctx, conn, Message{
		JSONRPC: "2.0", ID: 0, Method: "nabu.hello",
		Params: mustJSON(map[string]any{"protocol_version": "99.0"}),
	}); err != nil {
		t.Fatal(err)
	}
	var reply Message
	if err := wsjson.Read(ctx, conn, &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Error == nil {
		t.Fatal("a mismatched major version must be refused")
	}
	if reply.Error.Code != protocol.CodeProtocolMismatch {
		t.Errorf("code: got %d, want %d", reply.Error.Code, protocol.CodeProtocolMismatch)
	}
}

func TestListAndState(t *testing.T) {
	addr, m, dir := realDaemon(t)
	c := dialTest(t, addr)
	ctx := context.Background()

	sessions, err := c.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions: got %d, want 0", len(sessions))
	}

	s, err := m.Create(ctx, dir, agent.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	sessions, err = c.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != s.ID() {
		t.Fatalf("sessions: %+v", sessions)
	}

	st, err := c.State(ctx, s.ID())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != protocol.StateIdle {
		t.Errorf("state: got %q, want idle", st.State)
	}
}

// The cursor is how a reconnecting client catches up without losing events.
func TestEventsAfterCursor(t *testing.T) {
	addr, m, dir := realDaemon(t)
	c := dialTest(t, addr)
	ctx := context.Background()

	s, err := m.Create(ctx, dir, agent.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	all, synced, err := c.EventsAfter(ctx, s.ID(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) == 0 {
		t.Fatal("a nil cursor should return the whole log")
	}
	if !synced {
		t.Error("a nil cursor should report synced")
	}

	last := all[len(all)-1].ID
	tail, _, err := c.EventsAfter(ctx, s.ID(), &last)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 0 {
		t.Fatalf("a cursor at the tail should return nothing, got %d", len(tail))
	}

	// Something new appears after the cursor.
	if _, err := m.SetGoal(ctx, s.ID(), "the gate is green"); err != nil {
		t.Fatal(err)
	}
	tail, _, err = c.EventsAfter(ctx, s.ID(), &last)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) == 0 {
		t.Fatal("events appended after the cursor should be returned")
	}
}

func TestUnknownSessionIsAnError(t *testing.T) {
	addr, _, _ := realDaemon(t)
	c := dialTest(t, addr)

	_, err := c.State(context.Background(), "01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err == nil {
		t.Fatal("an unknown session must be an error")
	}
	var rpcErr *RPCError
	if !asRPCError(err, &rpcErr) {
		t.Fatalf("want an RPC error, got %T: %v", err, err)
	}
	if rpcErr.Code != protocol.CodeSessionNotFound {
		t.Errorf("code: got %d, want %d", rpcErr.Code, protocol.CodeSessionNotFound)
	}
}

// Subscribing must deliver events as notifications.
func TestSubscribeDeliversEvents(t *testing.T) {
	addr, m, dir := realDaemon(t)
	c := dialTest(t, addr)
	ctx := context.Background()

	s, err := m.Create(ctx, dir, agent.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Subscribe(ctx, s.ID()); err != nil {
		t.Fatal(err)
	}

	got := make(chan SessionEvent, 8)
	go func() {
		_ = c.Stream(ctx, func(msg Message) bool {
			if ev, ok := ParseEvent(msg); ok {
				select {
				case got <- ev:
				default:
				}
			}
			return true
		})
	}()

	if _, err := m.SetGoal(ctx, s.ID(), "tests pass"); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-got:
		if ev.SessionID != s.ID() {
			t.Errorf("session_id: got %q, want %q", ev.SessionID, s.ID())
		}
		if ev.Event.Type == "" {
			t.Error("the notification carried no event")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no event arrived after subscribing")
	}
}

func TestParseHelpers(t *testing.T) {
	perm := Message{Method: "nabu.rpc.permission.request", ID: "r1",
		Params: mustJSON(map[string]any{"request_id": "r1", "tool": "bash",
			"summary": "rm -rf /", "risk": "high"})}
	p, ok := ParsePermission(perm)
	if !ok || p.Tool != "bash" || p.Risk != "high" {
		t.Fatalf("ParsePermission: %+v ok=%v", p, ok)
	}
	if !perm.IsRequest() {
		t.Error("a method with an id is a request")
	}

	ask := Message{Method: "nabu.rpc.ui.ask", ID: "r2",
		Params: mustJSON(map[string]any{"question": "which branch?", "choices": []string{"a", "b"}})}
	a, ok := ParseAsk(ask)
	if !ok || a.Question != "which branch?" || len(a.Choices) != 2 {
		t.Fatalf("ParseAsk: %+v ok=%v", a, ok)
	}

	delta := Message{Method: "nabu.session.delta",
		Params: mustJSON(map[string]any{"session_id": "s", "turn_id": "t1", "text": "hi"})}
	d, ok := ParseDelta(delta)
	if !ok || d.Text != "hi" || d.TurnID != "t1" {
		t.Fatalf("ParseDelta: %+v ok=%v", d, ok)
	}
	if !delta.IsNotification() {
		t.Error("a method with no id is a notification")
	}

	// A message of the wrong method is not silently accepted.
	if _, ok := ParsePermission(delta); ok {
		t.Error("ParsePermission should reject a delta")
	}
	if _, ok := ParseEvent(delta); ok {
		t.Error("ParseEvent should reject a delta")
	}
}

func TestSameID(t *testing.T) {
	for _, tc := range []struct {
		got  any
		want int
		ok   bool
	}{
		{float64(3), 3, true}, // JSON numbers decode as float64
		{3, 3, true},
		{float64(4), 3, false},
		{"3", 3, false},
		{nil, 3, false},
	} {
		if got := sameID(tc.got, tc.want); got != tc.ok {
			t.Errorf("sameID(%#v, %d) = %v, want %v", tc.got, tc.want, got, tc.ok)
		}
	}
}

// asRPCError is errors.As without pulling the package in for one use.
func asRPCError(err error, target **RPCError) bool {
	for err != nil {
		if e, ok := err.(*RPCError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// A notification arriving while a call waits for its response must not be
// dropped. Subscribing and then streaming otherwise loses every event that
// landed in between, including the state change that ends a run.
func TestNotificationsDuringACallAreNotLost(t *testing.T) {
	addr, m, dir := realDaemon(t)
	c := dialTest(t, addr)
	ctx := context.Background()

	s, err := m.Create(ctx, dir, agent.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Subscribe(ctx, s.ID()); err != nil {
		t.Fatal(err)
	}

	// Append while nobody is streaming, so the events land in the socket
	// buffer, then make a call that will read past them.
	if _, err := m.SetGoal(ctx, s.ID(), "the gate is green"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // let the fan-out write them

	if _, err := c.State(ctx, s.ID()); err != nil {
		t.Fatal(err)
	}

	// Whatever the call read past must still be delivered, in order.
	got := make(chan string, 8)
	go func() {
		_ = c.Stream(ctx, func(msg Message) bool {
			if ev, ok := ParseEvent(msg); ok {
				got <- string(ev.Event.Type)
			}
			return true
		})
	}()

	select {
	case typ := <-got:
		if typ != string(protocol.EventGoal) {
			t.Errorf("first queued event: got %q, want %q", typ, protocol.EventGoal)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the event read past by the call was lost")
	}
}

// A TUI streams events on one goroutine while answering prompts and sending
// input on another. The websocket library forbids concurrent reads and
// concurrent writes, and doing either corrupts the frame stream: the symptom
// is "unexpected rsv bits set" and JSON that starts mid-value.
func TestCallsAndStreamCanRunTogether(t *testing.T) {
	addr, m, dir := realDaemon(t)
	c := dialTest(t, addr)
	ctx := context.Background()

	s, err := m.Create(ctx, dir, agent.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Subscribe(ctx, s.ID()); err != nil {
		t.Fatal(err)
	}

	streamCtx, stopStream := context.WithCancel(ctx)
	defer stopStream()
	streamed := make(chan struct{}, 64)
	go func() {
		_ = c.Stream(streamCtx, func(Message) bool {
			select {
			case streamed <- struct{}{}:
			default:
			}
			return true
		})
	}()

	// Hammer the same connection with calls while the stream is live.
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 8; j++ {
				if _, err := c.State(ctx, s.ID()); err != nil {
					errs <- err
					return
				}
				if _, err := c.List(ctx); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("a call failed while streaming: %v", err)
	}

	// The connection is still usable and still delivering.
	if _, err := m.SetGoal(ctx, s.ID(), "tests pass"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-streamed:
	case <-time.After(10 * time.Second):
		t.Fatal("the stream stopped delivering after concurrent calls")
	}
}

// The bash tool caps its output at 32 KiB and the websocket library defaults
// its read limit to 32 KiB, so a maximum-size tool result plus its JSON-RPC
// envelope is always over the limit. The connection dies rather than the
// message being rejected, which is what "read limited at 32769 bytes" is.
func TestALargeEventDoesNotKillTheConnection(t *testing.T) {
	addr, m, dir := realDaemon(t)
	c := dialTest(t, addr)
	ctx := context.Background()

	s, err := m.Create(ctx, dir, agent.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Subscribe(ctx, s.ID()); err != nil {
		t.Fatal(err)
	}

	got := make(chan int, 4)
	errs := make(chan error, 1)
	go func() {
		errs <- c.Stream(ctx, func(msg Message) bool {
			if ev, ok := ParseEvent(msg); ok && ev.Event.Type == protocol.EventToolResult {
				d := protocol.MustData[protocol.ToolResultData](ev.Event)
				select {
				case got <- len(d.Content):
				default:
				}
				return false
			}
			return true
		})
	}()

	// Exactly what the bash tool is allowed to return.
	big := strings.Repeat("x", 32<<10)
	if _, err := s.Append(protocol.EventToolResult, protocol.ToolResultData{
		CallID: "c1", Tool: "bash", Content: big, Status: "ok"}); err != nil {
		t.Fatal(err)
	}

	// Stream returns as soon as the callback asks it to stop, so on a loaded
	// machine its nil return is ready before the delivery that prompted it,
	// and select picks between them at random. A stop only means the
	// connection dropped if nothing arrived with it.
	var n int
	select {
	case n = <-got:
	case stopped := <-errs:
		select {
		case n = <-got:
		default:
			t.Fatalf("the connection dropped instead of delivering the event: %v", stopped)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the large event never arrived")
	}
	if n != len(big) {
		t.Errorf("received %d bytes of content, want %d", n, len(big))
	}
}

// The same limit applies to what a client sends: pasting a large prompt must
// not drop the connection either.
func TestALargePromptIsAccepted(t *testing.T) {
	addr, m, dir := realDaemon(t)
	c := dialTest(t, addr)
	ctx := context.Background()

	s, err := m.Create(ctx, dir, agent.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	big := strings.Repeat("a stack trace line\n", 4000) // well over 32 KiB
	_, err = c.Call(ctx, "nabu.session.send_prompt",
		map[string]any{"session_id": s.ID(), "content": big})
	if err != nil {
		t.Fatalf("a large prompt was refused: %v", err)
	}
}

// Both ends have to agree. If one side will read more than the other, a
// message one considers sendable is one the other drops the connection over,
// which is the shape of the bug this pair of limits fixed.
func TestBothSidesAgreeOnTheMessageLimit(t *testing.T) {
	if MaxMessageBytes != api.MaxMessageBytes {
		t.Errorf("client limit %d, daemon limit %d: they must match",
			MaxMessageBytes, api.MaxMessageBytes)
	}
	// Generous enough for a whole session replay, which events_after returns
	// in one message.
	if MaxMessageBytes < 16<<20 {
		t.Errorf("limit %d is too small for a long session's catch-up", MaxMessageBytes)
	}
}
