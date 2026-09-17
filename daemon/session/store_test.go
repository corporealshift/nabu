package session

import (
	"testing"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

func TestSubscribeReceivesAppends(t *testing.T) {
	st := newStore(t)
	s, _ := st.Create("C:/w", "w-1", opts, 0)
	ch, cancel := s.Subscribe(8)
	defer cancel()
	e, _ := s.Append(protocol.EventMessage, protocol.MessageData{Role: "user", Content: "hi"})
	select {
	case got := <-ch:
		if got.ID != e.ID {
			t.Fatalf("got %s want %s", got.ID, e.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("no event delivered")
	}
}

func TestSlowSubscriberIsDropped(t *testing.T) {
	st := newStore(t)
	s, _ := st.Create("C:/w", "w-1", opts, 0)
	ch, cancel := s.Subscribe(1)
	defer cancel()
	s.Append(protocol.EventMessage, protocol.MessageData{Role: "user", Content: "1"})
	s.Append(protocol.EventMessage, protocol.MessageData{Role: "user", Content: "2"}) // overflows
	<-ch                                                                              // the first event
	if _, ok := <-ch; ok {
		t.Fatal("overflowed subscriber must be closed")
	}
}

func TestListAndRecover(t *testing.T) {
	st := newStore(t)
	a, _ := st.Create("C:/a", "a-1", opts, 0)
	b, _ := st.Create("C:/b", "b-1", opts, 0)
	b.Append(protocol.EventMessage, protocol.MessageData{Role: "user", Content: "go"})
	b.Append(protocol.EventStateChange, protocol.StateChangeData{To: protocol.StateRunning, Reason: "prompt"})
	b.Append(protocol.EventGoal, protocol.GoalData{Condition: "tests pass", State: "set", Source: "client"})

	sums, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	// Newest first: b was created after a, so it leads.
	if len(sums) != 2 || sums[0].SessionID != b.ID() || sums[1].SessionID != a.ID() {
		t.Fatalf("list: %+v", sums)
	}
	if sums[0].State != protocol.StateRunning || sums[0].EventCount != 4 || sums[0].Goal == nil {
		t.Fatalf("summary: %+v", sums[0])
	}

	paused, err := st.RecoverInterrupted()
	if err != nil {
		t.Fatal(err)
	}
	if len(paused) != 1 || paused[0] != b.ID() {
		t.Fatalf("paused: %v", paused)
	}
	if b.State().State != protocol.StatePaused {
		t.Fatalf("state: %s", b.State().State)
	}
	if a.State().State != protocol.StateIdle {
		t.Fatal("idle session must be untouched")
	}
	ev := b.Events()
	if ev[len(ev)-2].Type != protocol.EventNotice {
		t.Fatal("recovery must append a notice before the state change")
	}
}

// A list of sessions is read newest first: the one you were just working in
// belongs at the top, and clients take the head when given no session id.
func TestListIsNewestFirst(t *testing.T) {
	st := newStore(t)
	var ids []string
	for i := 0; i < 4; i++ {
		s, err := st.Create("C:/w", "w-1", opts, 0)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, s.ID())
	}

	sums, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != len(ids) {
		t.Fatalf("sessions: got %d, want %d", len(sums), len(ids))
	}
	for i, sum := range sums {
		want := ids[len(ids)-1-i]
		if sum.SessionID != want {
			t.Fatalf("position %d: got %s, want %s", i, sum.SessionID, want)
		}
	}
}
