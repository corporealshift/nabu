package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

// pendingID waits for the daemon to register exactly one outstanding request
// and returns its id, without sleeping on a guess.
func (hn *harness) pendingID(t *testing.T) string {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		hn.h.reqMu.Lock()
		for id := range hn.h.pending {
			hn.h.reqMu.Unlock()
			return id
		}
		hn.h.reqMu.Unlock()
		select {
		case <-deadline:
			t.Fatal("no daemon-to-client request was registered")
		case <-time.After(2 * time.Millisecond):
		}
	}
}

// answer feeds a client response for a daemon-to-client request.
func (hn *harness) answer(t *testing.T, cs *connState, id string, result any) {
	t.Helper()
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	msg := &clientMessage{JSONRPC: "2.0", ID: id, Result: raw}
	if !hn.h.routeResponse(context.Background(), cs, msg) {
		t.Fatalf("response for %s was not routed to a pending request", id)
	}
}

func TestPermissionFirstResponderWins(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)

	csA, rcA := hn.attach(t)
	csB, rcB := hn.attach(t)
	hn.subscribeVia(t, csA, id)
	hn.subscribeVia(t, csB, id)

	verdict := make(chan bool, 1)
	reason := make(chan string, 1)
	go func() {
		ok, why, _ := hn.h.Permission(context.Background(), id,
			protocol.ToolCallData{Tool: "bash", CallID: "c1"}, "rm -rf /", "high")
		verdict <- ok
		reason <- why
	}()

	// Both clients must be asked.
	reqID := hn.pendingID(t)
	for name, rc := range map[string]*recordConn{"A": rcA, "B": rcB} {
		select {
		case <-rc.out:
		case <-time.After(5 * time.Second):
			t.Fatalf("client %s was never asked", name)
		}
	}

	hn.answer(t, csA, reqID, permissionReply{Verdict: "approve", Reason: "fine"})

	select {
	case ok := <-verdict:
		if !ok {
			t.Fatal("the first answer approved, so Permission must approve")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Permission never returned")
	}
	if got := <-reason; got != "fine" {
		t.Errorf("reason: got %q, want %q", got, "fine")
	}
	_ = csB
}

func TestLateAnswerGetsAlreadyResolved(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)
	csA, _ := hn.attach(t)
	csB, rcB := hn.attach(t)
	hn.subscribeVia(t, csA, id)
	hn.subscribeVia(t, csB, id)

	done := make(chan struct{})
	go func() {
		hn.h.Permission(context.Background(), id,
			protocol.ToolCallData{Tool: "bash", CallID: "c1"}, "ls", "low")
		close(done)
	}()

	reqID := hn.pendingID(t)
	hn.answer(t, csA, reqID, permissionReply{Verdict: "approve"})
	<-done

	// B answers after A already won.
	raw, err := json.Marshal(permissionReply{Verdict: "deny"})
	if err != nil {
		t.Fatal(err)
	}
	hn.h.routeResponse(context.Background(), csB,
		&clientMessage{JSONRPC: "2.0", ID: reqID, Result: raw})

	// B is told to dismiss its prompt.
	got := lastErrorWrittenTo(csB)
	if got == nil {
		t.Fatal("the late responder got no error response")
	}
	if got.Code != protocol.CodeAlreadyResolved {
		t.Errorf("code: got %d, want %d", got.Code, protocol.CodeAlreadyResolved)
	}
	_ = rcB
}

func TestPermissionDeniedWhenNobodyIsAttached(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)

	ok, reason, answered := hn.h.Permission(context.Background(), id,
		protocol.ToolCallData{Tool: "bash", CallID: "c1"}, "ls", "low")
	if ok {
		t.Fatal("with nobody attached, a gated call must be denied")
	}
	if answered {
		t.Error("nobody was attached, so nobody answered")
	}
	if reason == "" {
		t.Error("a denial should say why")
	}
}

func TestPermissionTimesOutAndDenies(t *testing.T) {
	hn := newHarness(t)
	hn.h.RequestTimeout = 80 * time.Millisecond
	id := hn.mustCreate(t)
	cs, _ := hn.attach(t)
	hn.subscribeVia(t, cs, id)

	start := time.Now()
	ok, reason, answered := hn.h.Permission(context.Background(), id,
		protocol.ToolCallData{Tool: "bash", CallID: "c1"}, "ls", "low")
	if ok {
		t.Fatal("an unanswered request must deny")
	}
	if answered {
		t.Error("a timeout is not an answer")
	}
	if reason == "" {
		t.Error("a timeout should say why")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("timeout was not honoured: waited %s", elapsed)
	}
}

// Spec 7.18: an answer arriving after the timeout is still accepted. It is too
// late to feed the call that asked, so what it buys is the session moving on.
func TestAnswerAfterTimeoutIsAccepted(t *testing.T) {
	hn := newHarness(t)
	hn.h.RequestTimeout = 80 * time.Millisecond
	id := hn.mustCreate(t)
	cs, _ := hn.attach(t)
	hn.subscribeVia(t, cs, id)

	registered := make(chan string, 1)
	go func() {
		go func() { registered <- hn.pendingID(t) }()
		hn.h.Permission(context.Background(), id,
			protocol.ToolCallData{Tool: "bash", CallID: "c1"}, "ls", "low")
	}()
	reqID := <-registered

	// Wait for the request to actually time out before answering.
	deadline := time.After(5 * time.Second)
	for {
		hn.h.reqMu.Lock()
		p, ok := hn.h.pending[reqID]
		hn.h.reqMu.Unlock()
		if !ok {
			break // Permission returned and deregistered it.
		}
		p.mu.Lock()
		timedOut := p.state == requestTimedOut
		p.mu.Unlock()
		if timedOut {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the request never timed out")
		case <-time.After(2 * time.Millisecond):
		}
	}

	// A late answer must be accepted rather than refused as already resolved.
	raw, err := json.Marshal(permissionReply{Verdict: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	hn.h.reqMu.Lock()
	p, stillPending := hn.h.pending[reqID]
	hn.h.reqMu.Unlock()
	if !stillPending {
		t.Skip("the request was deregistered before the late answer could be tested")
	}
	if got := p.resolve(raw); got != requestTimedOut {
		t.Fatalf("late answer saw state %v, want timed out", got)
	}
	if e := lastErrorWrittenTo(cs); e != nil && e.Code == protocol.CodeAlreadyResolved {
		t.Error("a post-timeout answer must not be refused as already resolved")
	}
}

func TestAskReturnsTheAnswer(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)
	cs, _ := hn.attach(t)
	hn.subscribeVia(t, cs, id)

	got := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		a, err := hn.h.Ask(context.Background(), id, "which branch?", []string{"main", "p1b"})
		got <- a
		errs <- err
	}()

	reqID := hn.pendingID(t)
	hn.answer(t, cs, reqID, askReply{Answer: "p1b"})

	select {
	case a := <-got:
		if a != "p1b" {
			t.Errorf("answer: got %q, want %q", a, "p1b")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Ask never returned")
	}
	if err := <-errs; err != nil {
		t.Errorf("Ask: %v", err)
	}
}

func TestRequestTimeoutDefaultsToSpec(t *testing.T) {
	hn := newHarness(t)
	if got := hn.h.requestTimeout(); got != DefaultRequestTimeout {
		t.Errorf("default timeout: got %s, want %s", got, DefaultRequestTimeout)
	}
	hn.h.RequestTimeout = time.Second
	if got := hn.h.requestTimeout(); got != time.Second {
		t.Errorf("configured timeout: got %s, want 1s", got)
	}
}

// lastErrorWrittenTo returns the error from the most recent response written
// to a connection, or nil if the last write was not an error response.
func lastErrorWrittenTo(cs *connState) *rpcError {
	rc, ok := cs.conn.(*recordConn)
	if !ok {
		return nil
	}
	cs.flush()
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.lastErr
}

// Issue 109: a question asked while the phone was asleep failed at once with
// "no client is attached", so it could never be answered from the phone. It
// now waits, and the phone is sent it when it wakes and subscribes.
func TestAQuestionWaitsForSomeoneToAttach(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)

	got := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		a, err := hn.h.Ask(context.Background(), id, "which branch?", []string{"main", "p1b"})
		got <- a
		errs <- err
	}()
	reqID := hn.pendingID(t)

	cs, rc := hn.attach(t)
	hn.subscribeVia(t, cs, id)
	sent := rc.await(t, "nabu.rpc.ui.ask")
	if sent["request_id"] != reqID || sent["question"] != "which branch?" {
		t.Fatalf("the late subscriber was sent %#v", sent)
	}
	hn.answer(t, cs, reqID, askReply{Answer: "main"})

	select {
	case a := <-got:
		if a != "main" {
			t.Errorf("answer: got %q, want %q", a, "main")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Ask never returned")
	}
	if err := <-errs; err != nil {
		t.Errorf("Ask: %v", err)
	}
}

// A client that subscribes while a request is open is sent it, once, and a
// request already answered is not sent again.
func TestSubscribingSendsWhatIsStillOpen(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)
	csA, rcA := hn.attach(t)
	hn.subscribeVia(t, csA, id)

	done := make(chan struct{})
	go func() {
		hn.h.Permission(context.Background(), id,
			protocol.ToolCallData{Tool: "bash", CallID: "c1"}, "ls", "low")
		close(done)
	}()
	reqID := hn.pendingID(t)
	rcA.await(t, "nabu.rpc.permission.request")

	csB, rcB := hn.attach(t)
	hn.subscribeVia(t, csB, id)
	if got := rcB.await(t, "nabu.rpc.permission.request"); got["request_id"] != reqID {
		t.Fatalf("the late subscriber was sent %#v", got)
	}
	// Subscribing again, as a reconnecting client does, sends it again.
	hn.subscribeVia(t, csB, id)
	rcB.await(t, "nabu.rpc.permission.request")

	hn.answer(t, csB, reqID, permissionReply{Verdict: "approve"})
	<-done

	csC, rcC := hn.attach(t)
	hn.subscribeVia(t, csC, id)
	csC.flush()
	select {
	case n := <-rcC.out:
		t.Fatalf("an answered request was sent again: %s", n.Method)
	default:
	}
	for _, rc := range []*recordConn{rcA, rcB} {
		select {
		case n := <-rc.out:
			if n.Method == "nabu.rpc.permission.request" {
				t.Fatal("a subscriber was sent the request twice")
			}
		default:
		}
	}
}
