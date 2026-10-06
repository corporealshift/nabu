package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/daemon/session"
	"github.com/corporealshift/nabu/protocol"
)

func TestArchiveIsRefusedWhileRunning(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{{Content: "done"}})
	block := make(chan struct{})
	h.fake.BlockOn = block
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "go")
	until(t, "the turn to start", func() bool { return s.State().State == protocol.StateRunning })

	err := h.m.Archive(context.Background(), s.ID(), "by request")
	if rpcCode(err) != protocol.CodeInvalidTransition {
		t.Fatalf("err: %v (code %d)", err, rpcCode(err))
	}
	close(block)
	h.m.WaitIdle(s.ID())

	if err := h.m.Archive(context.Background(), s.ID(), "by request"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Get(s.ID()); err != session.ErrNotFound {
		t.Fatalf("an archived session should not load, got %v", err)
	}

	if err := h.m.Restore(context.Background(), s.ID()); err != nil {
		t.Fatal(err)
	}
	back, err := h.store.Get(s.ID())
	if err != nil {
		t.Fatal(err)
	}
	var notes []string
	for _, e := range back.Events() {
		if e.Type == protocol.EventNotice {
			notes = append(notes, protocol.MustData[protocol.NoticeData](e).Message)
		}
	}
	if got := strings.Join(notes, " | "); !strings.Contains(got, "archived: by request") || !strings.Contains(got, "restored") {
		t.Errorf("the log should say it went and came back, got %q", got)
	}
}

// The sweep takes what has sat untouched, and leaves anything recent.
func TestTheIdleSweepArchivesOnlyWhatSat(t *testing.T) {
	h := newHarness(t, nil, nil)
	old := h.create(t)
	recent := h.create(t)
	last := func(s *session.Session) time.Time { return s.Events()[len(s.Events())-1].Timestamp }
	// Timestamps are to the millisecond, and both sessions can be made within
	// one: touch the recent one until its last event is strictly later.
	for !last(recent).After(last(old)) {
		time.Sleep(time.Millisecond)
		recent.Append(protocol.EventNotice, protocol.NoticeData{Source: "daemon", Level: "info", Message: "touched"})
	}

	// Exactly the old session's age: it is at the cutoff, the recent one short of it.
	now := last(recent).Add(3 * 24 * time.Hour)
	cutoff := now.Sub(last(old))

	got := h.m.ArchiveIdle(context.Background(), now, cutoff)
	if len(got) != 1 || got[0] != old.ID() {
		t.Fatalf("archived %v, want only %s", got, old.ID())
	}
	sums, _ := h.store.List()
	if len(sums) != 1 || sums[0].SessionID != recent.ID() {
		t.Fatalf("still listed: %+v", sums)
	}
}

// until polls cond, failing the test if it never holds.
func until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; !cond(); i++ {
		if i > 2500 {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// child makes a session under parent.
func (h *harness) child(t *testing.T, parent string) *session.Session {
	t.Helper()
	s, err := h.m.Create(context.Background(), h.dir, CreateOptions{Parent: parent})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func listedIDs(t *testing.T, h *harness) map[string]bool {
	t.Helper()
	sums, err := h.store.List()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, s := range sums {
		out[s.SessionID] = true
	}
	return out
}

func lastNotice(t *testing.T, h *harness, id string) string {
	t.Helper()
	events, err := h.store.ArchivedEvents(id)
	if err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	last := events[len(events)-1]
	if last.Type != protocol.EventNotice {
		t.Fatalf("%s: last event is %s", id, last.Type)
	}
	return protocol.MustData[protocol.NoticeData](last).Message
}

// Issue 135: a goal, its run and the run's step go away together, and come
// back together. A step archived on its own before stays where it was.
func TestArchivingTakesTheFamily(t *testing.T) {
	h := newHarness(t, nil, nil)
	goal := h.create(t)
	run := h.child(t, goal.ID())
	step := h.child(t, run.ID())
	early := h.child(t, run.ID())
	other := h.create(t)
	ctx := context.Background()

	if err := h.m.Archive(ctx, early.ID(), "by request"); err != nil {
		t.Fatal(err)
	}
	if err := h.m.Archive(ctx, goal.ID(), "by request"); err != nil {
		t.Fatal(err)
	}
	if got := listedIDs(t, h); len(got) != 1 || !got[other.ID()] {
		t.Fatalf("still listed: %v", got)
	}
	if n := lastNotice(t, h, step.ID()); n != "archived with "+goal.ID()+": by request" {
		t.Errorf("the step's notice: %q", n)
	}
	if n := lastNotice(t, h, goal.ID()); n != "archived: by request" {
		t.Errorf("the goal's notice: %q", n)
	}

	if err := h.m.Restore(ctx, goal.ID()); err != nil {
		t.Fatal(err)
	}
	got := listedIDs(t, h)
	for _, s := range []*session.Session{goal, run, step, other} {
		if !got[s.ID()] {
			t.Errorf("%s is not back", s.ID())
		}
	}
	if got[early.ID()] {
		t.Error("a step archived on its own came back with the goal")
	}
}

func TestArchivingIsRefusedWhileADescendantRuns(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{{Content: "done"}})
	block := make(chan struct{})
	h.fake.BlockOn = block
	goal := h.create(t)
	run := h.child(t, goal.ID())
	step := h.child(t, run.ID())
	h.m.Prompt(context.Background(), step.ID(), "go")
	until(t, "the turn to start", func() bool { return step.State().State == protocol.StateRunning })

	err := h.m.Archive(context.Background(), goal.ID(), "by request")
	if rpcCode(err) != protocol.CodeInvalidTransition || !strings.Contains(err.Error(), step.ID()) {
		t.Fatalf("err: %v", err)
	}
	if got := listedIDs(t, h); len(got) != 3 {
		t.Errorf("a refused archive put something away: %v", got)
	}
	close(block)
	h.m.WaitIdle(step.ID())
}

// The sweep takes a family only when all of it has sat: a run home untouched
// for days stays while one of its steps is recent.
func TestTheIdleSweepTakesWholeFamilies(t *testing.T) {
	h := newHarness(t, nil, nil)
	home := h.create(t)
	step := h.child(t, home.ID())
	last := func(s *session.Session) time.Time { return s.Events()[len(s.Events())-1].Timestamp }
	for !last(step).After(last(home)) {
		time.Sleep(time.Millisecond)
		step.Append(protocol.EventNotice, protocol.NoticeData{Source: "daemon", Level: "info", Message: "touched"})
	}
	after := 3 * 24 * time.Hour

	if got := h.m.ArchiveIdle(context.Background(), last(home).Add(after), after); len(got) != 0 {
		t.Fatalf("archived %v while a step was recent", got)
	}
	got := h.m.ArchiveIdle(context.Background(), last(step).Add(after), after)
	if len(got) != 1 || got[0] != home.ID() {
		t.Fatalf("archived %v, want the home alone, its step with it", got)
	}
	if left := listedIDs(t, h); len(left) != 0 {
		t.Errorf("still listed: %v", left)
	}
}
