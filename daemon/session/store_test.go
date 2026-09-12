package session

import (
	"testing"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

func TestSubscribeReceivesAppends(t *testing.T) {
	st := newStore(t)
	s, _ := st.Create("C:/w", "w-1", opts)
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
	s, _ := st.Create("C:/w", "w-1", opts)
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
	a, _ := st.Create("C:/a", "a-1", opts)
	b, _ := st.Create("C:/b", "b-1", opts)
	b.Append(protocol.EventMessage, protocol.MessageData{Role: "user", Content: "go"})
	b.Append(protocol.EventStateChange, protocol.StateChangeData{To: protocol.StateRunning, Reason: "prompt"})
	b.Append(protocol.EventGoal, protocol.GoalData{Condition: "tests pass", State: "set", Source: "client"})

	sums, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 2 || sums[0].SessionID != a.ID() || sums[1].SessionID != b.ID() {
		t.Fatalf("list: %+v", sums)
	}
	if sums[1].State != protocol.StateRunning || sums[1].EventCount != 4 || sums[1].Goal == nil {
		t.Fatalf("summary: %+v", sums[1])
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
