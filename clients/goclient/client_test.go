package goclient

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
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
