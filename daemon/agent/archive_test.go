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
