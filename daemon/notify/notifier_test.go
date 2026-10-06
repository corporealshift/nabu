package notify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/corporealshift/nabu/daemon/session"
	"github.com/corporealshift/nabu/protocol"
)

// fakeSender records every message, for every device.
type fakeSender struct {
	mu   sync.Mutex
	sent []map[string]string
}

func (f *fakeSender) Send(_ context.Context, device string, data map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if device == "GONE" {
		return ErrStaleToken
	}
	f.sent = append(f.sent, data)
	return nil
}

func (f *fakeSender) all() []map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]string(nil), f.sent...)
}

// rig is a real session store observed by a running notifier.
type rig struct {
	t      *testing.T
	store  *session.Store
	n      *Notifier
	sender *fakeSender
	seen   int
}

func newRig(t *testing.T) *rig {
	t.Helper()
	root := t.TempDir()
	store, err := session.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	devices, err := OpenDevices(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := devices.Register("PHONE", "Pixel"); err != nil {
		t.Fatal(err)
	}
	sender := &fakeSender{}
	lookup := func(id string) (protocol.State, bool) {
		s, err := store.Get(id)
		if err != nil {
			return protocol.State{}, false
		}
		return s.State(), true
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	n := New(Rules{Labels: DefaultLabels, DoneAfter: time.Minute}, devices, sender, lookup, log)
	store.SetObserver(n.Observe)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go n.Run(ctx)
	return &rig{t: t, store: store, n: n, sender: sender}
}

func (r *rig) create(parent string) *session.Session {
	r.t.Helper()
	s, err := r.store.Create("C:/w", "w", protocol.Options{Model: "m", PermissionMode: "auto", Parent: parent}, 0)
	if err != nil {
		r.t.Fatal(err)
	}
	return s
}

func (r *rig) append(s *session.Session, typ protocol.EventType, data any) {
	r.t.Helper()
	if _, err := s.Append(typ, data); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) state(s *session.Session, from, to protocol.SessionState, reason string) {
	r.t.Helper()
	r.append(s, protocol.EventStateChange, protocol.StateChangeData{From: &from, To: to, Reason: reason})
}

func (r *rig) labels(s *session.Session, from, to []string) {
	r.t.Helper()
	f, _ := json.Marshal(from)
	tt, _ := json.Marshal(to)
	r.append(s, protocol.EventOptionsChange, protocol.OptionsChangeData{Key: "labels", From: f, To: tt, Source: "client"})
}

// sentSince waits until everything told so far has been decided, and
// returns what was sent since the last call. A sentinel request goes in last
// and the queue is in order, so once it is out, all before it is too.
func (r *rig) sentSince() []map[string]string {
	r.t.Helper()
	r.n.RequestClosed("SENTINEL", "x")
	deadline := time.After(5 * time.Second)
	for {
		all := r.sender.all()
		if len(all) > r.seen && all[len(all)-1]["session_id"] == "SENTINEL" {
			out := all[r.seen : len(all)-1]
			r.seen = len(all)
			return out
		}
		select {
		case <-deadline:
			r.t.Fatal("the notifier never caught up")
		case <-time.After(2 * time.Millisecond):
		}
	}
}

func kinds(sent []map[string]string) []string {
	var out []string
	for _, d := range sent {
		k := d["kind"]
		if d["label"] != "" {
			k += " " + d["label"]
		}
		out = append(out, k)
	}
	return out
}

func TestWhatNotifies(t *testing.T) {
	r := newRig(t)
	idle, running := protocol.StateIdle, protocol.StateRunning

	home := r.create("")
	step := r.create(home.ID())
	r.sentSince()

	// A step's turns and labels are its root's to report.
	r.state(step, idle, running, "prompt")
	r.state(step, running, protocol.StateError, "provider")
	r.labels(step, nil, []string{"run:done"})
	if got := r.sentSince(); len(got) != 0 {
		t.Errorf("a session with a parent notified: %v", kinds(got))
	}

	// A short turn is one somebody watched.
	r.state(home, idle, running, "prompt")
	r.state(home, running, idle, "turn_complete")
	if got := r.sentSince(); len(got) != 0 {
		t.Errorf("a short turn notified: %v", kinds(got))
	}

	// Each way of stopping does, whatever the turn's length.
	for _, to := range []protocol.SessionState{protocol.StateBlocked, protocol.StateError, protocol.StatePaused} {
		r.state(home, idle, running, "prompt")
		r.state(home, running, to, "x")
		if got := kinds(r.sentSince()); len(got) != 1 || got[0] != KindStopped {
			t.Errorf("running → %s: %v", to, got)
		}
		r.state(home, to, idle, "resume")
	}

	// A stop is asked for, so whoever asked already knows.
	r.state(home, idle, running, "prompt")
	r.append(home, protocol.EventStateChange, protocol.StateChangeData{From: &running, To: protocol.StateCompleted, Reason: "stopped"})
	if got := r.sentSince(); len(got) != 0 {
		t.Errorf("a stopped session notified: %v", kinds(got))
	}

	// A label from the list does; an unlisted one, or one already there, does not.
	r.labels(home, []string{"guard:no-push"}, []string{"guard:no-push", "run:plan"})
	r.labels(home, []string{"run:plan"}, []string{"run:failed"})
	r.labels(home, []string{"run:failed"}, []string{"run:failed", "mine"})
	if got := kinds(r.sentSince()); len(got) != 1 || got[0] != "label run:failed" {
		t.Errorf("labels: %v", got)
	}

	// A question waits on a person wherever it is asked, and goes away when answered.
	r.n.RequestOpened(step.ID(), "REQ1", "nabu.rpc.ui.ask")
	r.n.RequestClosed(step.ID(), "REQ1")
	got := r.sentSince()
	if k := kinds(got); len(k) != 2 || k[0] != KindQuestion || k[1] != KindResolved || got[0]["request_id"] != "REQ1" {
		t.Errorf("a question: %v", got)
	}
}

// A turn that ran long enough notifies when it ends, with the task counts.
// The clock is the events': the turn below is two minutes long by its
// timestamps, so it is set up through the notifier directly.
func TestALongTurnNotifies(t *testing.T) {
	r := newRig(t)
	home := r.create("")
	r.append(home, protocol.EventTasks, protocol.TasksData{Revision: 1, Source: "model", Tasks: []protocol.Task{
		{ID: "t1", Title: "a", Status: protocol.TaskDone, BlockedBy: []string{}},
		{ID: "t2", Title: "b", Status: protocol.TaskPending, BlockedBy: []string{}}}})
	r.sentSince()

	idle, running := protocol.StateIdle, protocol.StateRunning
	start := time.Now()
	end := start.Add(2 * time.Minute)
	sc := func(from, to protocol.SessionState, reason string, at time.Time) protocol.Event {
		b, _ := json.Marshal(protocol.StateChangeData{From: &from, To: to, Reason: reason})
		return protocol.Event{ID: protocol.NewULID(), Type: protocol.EventStateChange, Timestamp: at, Data: b}
	}
	r.n.Observe(home.ID(), sc(idle, running, "prompt", start))
	r.n.Observe(home.ID(), sc(running, idle, "turn_complete", end))
	got := r.sentSince()
	if len(got) != 1 || got[0]["kind"] != KindDone || got[0]["tasks_done"] != "1" || got[0]["tasks_total"] != "2" {
		t.Errorf("a long turn: %v", got)
	}
}

// Architecture §16: identifiers and status only. No value sent may be text
// the session's log carries.
func TestNoTextLeavesTheDaemon(t *testing.T) {
	r := newRig(t)
	home := r.create("")
	secret := "the database password is hunter2"
	r.append(home, protocol.EventMessage, protocol.MessageData{Role: "user", Content: secret})
	r.state(home, protocol.StateIdle, protocol.StateRunning, "prompt")
	r.state(home, protocol.StateRunning, protocol.StateError, "x")
	r.labels(home, nil, []string{"goal:blocked"})
	r.n.RequestOpened(home.ID(), "REQ", "nabu.rpc.ui.ask")
	got := r.sentSince()
	if len(got) != 3 {
		t.Fatalf("sent %v", kinds(got))
	}
	for _, d := range got {
		for k, v := range d {
			if strings.Contains(secret, v) && len(v) > 3 || strings.Contains(v, "hunter2") {
				t.Errorf("%s = %q comes from the log", k, v)
			}
		}
	}
}

func TestAStaleDeviceIsDropped(t *testing.T) {
	r := newRig(t)
	if err := r.n.Register("GONE", "old phone"); err != nil {
		t.Fatal(err)
	}
	home := r.create("")
	r.labels(home, nil, []string{"goal:done"})
	r.sentSince()
	for _, tok := range r.n.devices.Tokens() {
		if tok == "GONE" {
			t.Error("a device FCM no longer knows was kept")
		}
	}
}
