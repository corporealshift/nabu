package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/corporealshift/rho/protocol"
)

// recordConn captures everything written to it and hands each message to the
// test over a channel, so assertions synchronise on delivery instead of
// sleeping.
type recordConn struct {
	out chan jsonrpcNotification

	mu      sync.Mutex
	lastErr *rpcError
}

func newRecordConn() *recordConn {
	return &recordConn{out: make(chan jsonrpcNotification, 64)}
}

func (c *recordConn) ReadJSON(ctx context.Context, _ any) error {
	<-ctx.Done()
	return ctx.Err()
}

func (c *recordConn) WriteJSON(_ context.Context, v any) error {
	switch m := v.(type) {
	case jsonrpcNotification:
		select {
		case c.out <- m:
		default:
		}
	case jsonrpcRequest:
		select {
		case c.out <- jsonrpcNotification{JSONRPC: m.JSONRPC, Method: m.Method, Params: m.Params}:
		default:
		}
	case *jsonrpcResponse:
		if m.Error != nil {
			c.mu.Lock()
			c.lastErr = m.Error
			c.mu.Unlock()
		}
	}
	return nil
}

func (c *recordConn) Close(websocket.StatusCode, string) {}
func (c *recordConn) CloseNow()                          {}

// await returns the next notification with the given method, or fails.
func (c *recordConn) await(t *testing.T, method string) map[string]any {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case n := <-c.out:
			if n.Method != method {
				continue
			}
			raw, err := json.Marshal(n.Params)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			return m
		case <-deadline:
			t.Fatalf("timed out waiting for %s", method)
			return nil
		}
	}
}

func (hn *harness) attach(t *testing.T) (*connState, *recordConn) {
	t.Helper()
	rc := newRecordConn()
	cs := newConnState(context.Background(), rc)
	t.Cleanup(func() { hn.h.dropConn(cs); cs.finish() })
	return cs, rc
}

func (hn *harness) subscribeVia(t *testing.T, cs *connState, sessionID string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "rho.session.subscribe",
		"params": map[string]any{"session_id": sessionID},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := hn.h.dispatch(context.Background(), cs, raw)
	if resp == nil || resp.Error != nil {
		t.Fatalf("subscribe failed: %+v", resp)
	}
}

// Multi-client is the normal case: the TUI and the phone may both be attached
// to one session, and every event must reach both.
func TestEventFanOutToTwoSubscribers(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)

	csA, rcA := hn.attach(t)
	csB, rcB := hn.attach(t)
	hn.subscribeVia(t, csA, id)
	hn.subscribeVia(t, csB, id)

	if _, err := hn.m.SetGoal(context.Background(), id, "tests pass"); err != nil {
		t.Fatal(err)
	}

	for name, rc := range map[string]*recordConn{"A": rcA, "B": rcB} {
		got := rc.await(t, "rho.session.event")
		if got["session_id"] != id {
			t.Errorf("subscriber %s: session_id %v, want %v", name, got["session_id"], id)
		}
		if got["event"] == nil {
			t.Errorf("subscriber %s: notification carried no event", name)
		}
	}
	_ = csB
}

// Deltas are ephemeral: they reach subscribers but never the session log, and
// events_after must never replay one.
func TestDeltasAreBroadcastButNeverLogged(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)

	cs, rc := hn.attach(t)
	hn.subscribeVia(t, cs, id)

	s, rpcErr := hn.h.getSession(id)
	if rpcErr != nil {
		t.Fatalf("getSession: %+v", rpcErr)
	}
	before := len(s.Events())

	hn.h.Deltas()(id, "turn-1", "partial text")

	got := rc.await(t, "rho.session.delta")
	if got["turn_id"] != "turn-1" || got["text"] != "partial text" {
		t.Fatalf("delta payload: %#v", got)
	}

	if after := len(s.Events()); after != before {
		t.Fatalf("a delta must not be logged: log grew from %d to %d", before, after)
	}
	for _, ev := range s.Events() {
		if string(ev.Type) == "delta" {
			t.Fatal("found a delta event in the log")
		}
	}
}

// A delta for a session nobody is watching must not panic or block.
func TestDeltaWithNoSubscribersIsDropped(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)
	hn.h.Deltas()(id, "turn-1", "nobody is listening")
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)
	cs, rc := hn.attach(t)
	hn.subscribeVia(t, cs, id)

	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "rho.session.unsubscribe",
		"params": map[string]any{"session_id": id},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := hn.h.dispatch(context.Background(), cs, raw)
	var out struct {
		Subscribed bool `json:"subscribed"`
	}
	result(t, resp, &out)
	if out.Subscribed {
		t.Error("unsubscribe must report subscribed=false")
	}

	if _, err := hn.m.SetGoal(context.Background(), id, "after unsubscribe"); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-rc.out:
		t.Fatalf("received %s after unsubscribing", n.Method)
	case <-time.After(300 * time.Millisecond):
	}
}

// A disconnect must not leave the session pump running.
func TestDropConnReleasesTheFanout(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)
	cs, _ := hn.attach(t)
	hn.subscribeVia(t, cs, id)

	hn.h.subMu.Lock()
	live := len(hn.h.fanouts)
	hn.h.subMu.Unlock()
	if live != 1 {
		t.Fatalf("fanouts after subscribe: got %d, want 1", live)
	}

	hn.h.dropConn(cs)

	hn.h.subMu.Lock()
	live = len(hn.h.fanouts)
	hn.h.subMu.Unlock()
	if live != 0 {
		t.Fatalf("fanouts after disconnect: got %d, want 0", live)
	}
}

func TestSubscribeUnknownSession(t *testing.T) {
	hn := newHarness(t)
	cs, _ := hn.attach(t)
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "rho.session.subscribe",
		"params": map[string]any{"session_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := hn.h.dispatch(context.Background(), cs, raw)
	if resp == nil || resp.Error == nil {
		t.Fatal("want a session-not-found error")
	}
	if resp.Error.Code != protocol.CodeSessionNotFound {
		t.Errorf("code: got %d, want %d", resp.Error.Code, protocol.CodeSessionNotFound)
	}
}

func TestGoalAndOptionMethods(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)

	var out struct {
		EventID string `json:"event_id"`
	}
	result(t, hn.call(t, 1, "rho.session.set_goal",
		map[string]any{"session_id": id, "condition": "the gate is green"}), &out)
	if out.EventID == "" {
		t.Fatal("set_goal returned no event_id")
	}
	s, _ := hn.h.getSession(id)
	if s.State().Goal == nil {
		t.Fatal("goal was not recorded")
	}

	result(t, hn.call(t, 2, "rho.session.clear_goal",
		map[string]any{"session_id": id}), &out)
	// Clearing appends a goal event with state "cleared", so the projection
	// keeps the goal but stops reporting it active.
	if s.State().GoalActive() {
		t.Error("goal should no longer be active after clear_goal")
	}

	result(t, hn.call(t, 3, "rho.session.set_option",
		map[string]any{"session_id": id, "key": "permission_mode", "value": "auto"}), &out)
	if got := s.State().Options.PermissionMode; got != protocol.PermissionAuto {
		t.Errorf("permission_mode: got %q, want %q", got, protocol.PermissionAuto)
	}
}

func TestSetGoalRequiresCondition(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)
	resp := hn.call(t, 1, "rho.session.set_goal", map[string]any{"session_id": id})
	if resp == nil || resp.Error == nil {
		t.Fatal("want an invalid-params error")
	}
	if resp.Error.Code != protocol.CodeInvalidParams {
		t.Errorf("code: got %d, want %d", resp.Error.Code, protocol.CodeInvalidParams)
	}
}

func TestSendPromptRequiresContent(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)
	resp := hn.call(t, 1, "rho.session.send_prompt", map[string]any{"session_id": id})
	if resp == nil || resp.Error == nil {
		t.Fatal("want an invalid-params error")
	}
	if resp.Error.Code != protocol.CodeInvalidParams {
		t.Errorf("code: got %d, want %d", resp.Error.Code, protocol.CodeInvalidParams)
	}
}

func TestUpdateTasks(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)

	var out struct {
		EventID string `json:"event_id"`
	}
	result(t, hn.call(t, 1, "rho.session.update_tasks", map[string]any{
		"session_id": id,
		"tasks": []map[string]any{
			{"id": "t1", "title": "write the test", "status": "pending"},
		},
	}), &out)

	s, _ := hn.h.getSession(id)
	tasks := s.State().Tasks
	if len(tasks) != 1 || tasks[0].ID != "t1" {
		t.Fatalf("tasks: got %#v", tasks)
	}
}

// set_status appends a snapshot as given, starts nothing, and refuses one the
// protocol would not accept before anything is written (spec 7.27).
func TestSetStatus(t *testing.T) {
	big := map[string]any{"history": strings.Repeat("x", protocol.MaxStatusBytes)}
	for _, tc := range []struct {
		name string
		kind string
		data any
		ok   bool
	}{
		{"a run", "run", map[string]any{"version": 1, "step": "plan"}, true},
		{"a goal", "goal", map[string]any{"version": 1, "round": 2}, true},
		{"an unknown kind", "build", map[string]any{"version": 1}, false},
		{"data that is not an object", "run", []string{"plan"}, false},
		{"no data", "run", nil, false},
		{"data over the limit", "run", big, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hn := newHarness(t)
			id := hn.mustCreate(t)
			s, _ := hn.h.getSession(id)
			before := len(s.Events())

			resp := hn.call(t, 1, "rho.session.set_status", map[string]any{
				"session_id": id, "kind": tc.kind, "data": tc.data})
			if !tc.ok {
				if resp == nil || resp.Error == nil || resp.Error.Code != protocol.CodeInvalidParams {
					t.Fatalf("want invalid_params, got %+v", resp)
				}
				if len(s.Events()) != before {
					t.Fatalf("a refused status appended %d events", len(s.Events())-before)
				}
				return
			}
			var out struct {
				EventID string `json:"event_id"`
			}
			result(t, resp, &out)
			st := s.State()
			if st.Status == nil || st.Status.Kind != tc.kind || st.Status.Source != "client" || st.LastEventID != out.EventID {
				t.Fatalf("status: %+v, last event %s, returned %s", st.Status, st.LastEventID, out.EventID)
			}
			want, _ := json.Marshal(tc.data)
			if string(st.Status.Data) != string(want) {
				t.Errorf("data: got %s, want %s", st.Status.Data, want)
			}
			if st.State != protocol.StateIdle {
				t.Errorf("state: got %q, want idle", st.State)
			}
		})
	}
}

// Interrupt is a no-op when the session is not running, per spec 7.7.
func TestInterruptWhenIdleIsNoOp(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)
	resp := hn.call(t, 1, "rho.session.interrupt", map[string]any{"session_id": id})
	if resp == nil || resp.Error != nil {
		t.Fatalf("interrupt while idle should succeed, got %+v", resp)
	}
}

func TestStopEndsTheSession(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)
	resp := hn.call(t, 1, "rho.session.stop", map[string]any{"session_id": id})
	if resp == nil || resp.Error != nil {
		t.Fatalf("stop failed: %+v", resp)
	}
	s, _ := hn.h.getSession(id)
	if got := s.State().State; got != protocol.StateCompleted {
		t.Errorf("state after stop: got %q, want %q", got, protocol.StateCompleted)
	}
}

// Every mutating method must reject an unknown session.
func TestMutatorsRejectUnknownSession(t *testing.T) {
	hn := newHarness(t)
	unknown := map[string]any{
		"session_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		"content":    "x", "condition": "x", "key": "permission_mode", "value": "ask",
	}
	for _, method := range []string{
		"rho.session.send_prompt", "rho.session.interrupt", "rho.session.stop",
		"rho.session.resume", "rho.session.set_goal", "rho.session.clear_goal",
		"rho.session.set_option", "rho.session.update_tasks", "rho.session.set_status",
	} {
		resp := hn.call(t, 1, method, unknown)
		if resp == nil || resp.Error == nil {
			t.Fatalf("%s: want an error for an unknown session", method)
		}
		if resp.Error.Code != protocol.CodeSessionNotFound {
			t.Errorf("%s code: got %d, want %d", method, resp.Error.Code, protocol.CodeSessionNotFound)
		}
	}
}

// stuckConn accepts no writes: a phone frozen in the background, whose socket
// stays open with a full receive window.
type stuckConn struct{}

func (stuckConn) ReadJSON(ctx context.Context, _ any) error { <-ctx.Done(); return ctx.Err() }
func (stuckConn) WriteJSON(ctx context.Context, _ any) error {
	<-ctx.Done()
	return ctx.Err()
}
func (stuckConn) Close(websocket.StatusCode, string) {}
func (stuckConn) CloseNow()                          {}

// Issue 110: with a phone asleep and the TUI open on one session, the TUI
// heard nothing. The fan-out wrote to each subscriber in turn, so the frozen
// one held up the rest, and the store then dropped the stalled pump.
func TestAFrozenSubscriberDoesNotStallTheOthers(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)

	frozen := newConnState(context.Background(), stuckConn{})
	t.Cleanup(func() { hn.h.dropConn(frozen); frozen.drop() })
	hn.subscribeVia(t, frozen, id)
	cs, rc := hn.attach(t)
	hn.subscribeVia(t, cs, id)

	// More than the store lets a pump fall behind by, each read before the
	// next is made. Produced in a burst, on one core the burst filled the
	// store's buffer before the pump ran at all, and the store dropped every
	// subscriber to resync, as it should: that is a pump falling behind, not
	// a frozen client holding the others up, which is what this is about.
	const n = 2 * eventBuffer
	var heard atomic.Int64
	go func() {
		for m := range rc.out {
			if m.Method == "rho.session.event" {
				heard.Add(1)
			}
		}
	}()
	for i := range n {
		before := heard.Load()
		if _, err := hn.m.SetGoal(context.Background(), id, fmt.Sprintf("goal %d", i)); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for heard.Load() == before {
			if time.Now().After(deadline) {
				t.Fatalf("the attentive client stopped hearing after %d of %d goals", i, n)
			}
			time.Sleep(time.Millisecond)
		}
	}

	// And a client arriving afterwards hears the session too.
	csC, rcC := hn.attach(t)
	hn.subscribeVia(t, csC, id)
	if _, err := hn.m.SetGoal(context.Background(), id, "after"); err != nil {
		t.Fatal(err)
	}
	rcC.await(t, "rho.session.event")
}

// A connection that stops reading is dropped once its queue is full, rather
// than holding its messages forever.
func TestAConnectionThatStopsReadingIsDropped(t *testing.T) {
	c := &closeCounter{}
	cs := newConnState(context.Background(), c)
	t.Cleanup(cs.drop)
	var err error
	for i := 0; i <= sendQueue+1 && err == nil; i++ {
		err = cs.write(jsonrpcNotification{JSONRPC: "2.0", Method: "m"})
	}
	if err == nil {
		t.Fatal("a queue that never drains accepted everything")
	}
	if c.closed.Load() == 0 {
		t.Error("a connection too far behind was not closed")
	}
}

// closeCounter blocks every write and records being closed.
type closeCounter struct {
	stuckConn
	closed atomic.Int32
}

func (c *closeCounter) CloseNow() { c.closed.Add(1) }

// A session archived with a client attached, then restored, must be heard by
// whoever subscribes next. The pump ended with the archive; left registered,
// it was a dead stream every later subscriber joined.
func TestSubscribingAfterRestoreHearsEvents(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)
	csA, _ := hn.attach(t)
	hn.subscribeVia(t, csA, id)

	for _, method := range []string{"rho.session.archive", "rho.session.restore"} {
		if resp := hn.call(t, 2, method, map[string]any{"session_id": id}); resp.Error != nil {
			t.Fatalf("%s: %+v", method, resp.Error)
		}
	}

	csB, rcB := hn.attach(t)
	hn.subscribeVia(t, csB, id)
	if _, err := hn.m.SetGoal(context.Background(), id, "after restore"); err != nil {
		t.Fatal(err)
	}
	rcB.await(t, "rho.session.event")
}
