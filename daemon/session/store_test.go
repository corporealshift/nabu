package session

import (
	"strings"
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

// Sessions in one workspace look alike in a list; what was last asked is what
// tells them apart (issue 101).
func TestSummaryCarriesTheLastPrompt(t *testing.T) {
	st := newStore(t)
	s, _ := st.Create("C:/w", "w-1", opts, 0)
	if got := s.Summary().LastPrompt; got != "" {
		t.Fatalf("nothing asked yet: got %q", got)
	}

	s.Append(protocol.EventMessage, protocol.MessageData{Role: "user", Content: "first"})
	s.Append(protocol.EventMessage, protocol.MessageData{Role: "user", Content: "  second\n"})
	s.Append(protocol.EventMessage, protocol.MessageData{Role: "assistant", Content: "a reply"})
	if got := s.Summary().LastPrompt; got != "second" {
		t.Errorf("last prompt: got %q, want %q", got, "second")
	}

	s.Append(protocol.EventMessage, protocol.MessageData{Role: "user", Content: strings.Repeat("é", 300)})
	if got := []rune(s.Summary().LastPrompt); len(got) != lastPromptRunes {
		t.Errorf("a long prompt should be cut to %d runes, got %d", lastPromptRunes, len(got))
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

// Issue 56: a session can be put away and brought back, and nothing in its log
// is lost on the way.
func TestArchiveAndRestore(t *testing.T) {
	st := newStore(t)
	keep, _ := st.Create("C:/w", "w-1", opts, 0)
	gone, _ := st.Create("C:/w", "w-1", opts, 0)
	gone.Append(protocol.EventMessage, protocol.MessageData{Role: "user", Content: "hi"})
	want := len(gone.Events())
	ch, cancel := gone.Subscribe(4)
	defer cancel()

	if err := st.Archive(gone.ID()); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-ch; ok {
		t.Error("archiving should end the session's subscriptions")
	}
	if _, err := gone.Append(protocol.EventMessage, protocol.MessageData{Role: "user", Content: "late"}); err != ErrClosed {
		t.Errorf("an archived session must not take appends, got %v", err)
	}
	sums, _ := st.List()
	if len(sums) != 1 || sums[0].SessionID != keep.ID() {
		t.Fatalf("active list = %+v, want only %s", sums, keep.ID())
	}
	if _, err := st.Get(gone.ID()); err != ErrNotFound {
		t.Errorf("an archived session should not load, got %v", err)
	}
	archived, err := st.ListArchived()
	if err != nil || len(archived) != 1 || archived[0].SessionID != gone.ID() || !archived[0].Archived {
		t.Fatalf("archived list = %+v (%v)", archived, err)
	}
	if err := st.Archive(gone.ID()); err != nil {
		t.Errorf("archiving twice should be a no-op, got %v", err)
	}

	if err := st.Restore(gone.ID()); err != nil {
		t.Fatal(err)
	}
	back, err := st.Get(gone.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(back.Events()); got != want {
		t.Errorf("restored with %d events, want %d", got, want)
	}
	if sums, _ := st.List(); len(sums) != 2 {
		t.Errorf("restored session should list again, got %d", len(sums))
	}
	if err := st.Archive("01ARZ3NDEKTSV4RRFFQ69G5FAV"); err != ErrNotFound {
		t.Errorf("an unknown id should be not found, got %v", err)
	}
}
