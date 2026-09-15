package agent

import (
	"context"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/daemon/session"
	"github.com/corporealshift/nabu/daemon/tools"
	"github.com/corporealshift/nabu/protocol"
)

// An offline client queues a prompt, sends it, loses the connection before the
// response arrives, and sends it again when connectivity returns. Without a
// client id the log ends up with the message twice, which is the outbox's
// central failure (spec 5, "Offline writes via outbox").
func TestARetriedPromptIsAppendedOnce(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{{Content: "ok"}, {Content: "ok"}})
	s := h.create(t)
	ctx := context.Background()

	first, err := h.m.PromptWithID(ctx, s.ID(), "deploy the thing", "outbox-01ARZ3ND")
	if err != nil {
		t.Fatal(err)
	}
	h.m.WaitIdle(s.ID())

	// The phone never saw the response, so it sends the same item again.
	second, err := h.m.PromptWithID(ctx, s.ID(), "deploy the thing", "outbox-01ARZ3ND")
	if err != nil {
		t.Fatalf("a retry must be accepted, not refused: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("retry produced a new event %s, want the original %s", second.ID, first.ID)
	}

	var prompts int
	for _, e := range s.Events() {
		if e.Type != protocol.EventMessage {
			continue
		}
		if d := protocol.MustData[protocol.MessageData](e); d.Role == "user" {
			prompts++
		}
	}
	if prompts != 1 {
		t.Errorf("the log holds %d copies of one prompt, want 1", prompts)
	}
}

// A different client id is a different prompt, even with identical text.
func TestTheSameTextWithADifferentIDIsANewPrompt(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{{Content: "ok"}, {Content: "ok"}})
	s := h.create(t)
	ctx := context.Background()

	first, err := h.m.PromptWithID(ctx, s.ID(), "run it again", "outbox-A")
	if err != nil {
		t.Fatal(err)
	}
	h.m.WaitIdle(s.ID())
	second, err := h.m.PromptWithID(ctx, s.ID(), "run it again", "outbox-B")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Error("two outbox items collapsed into one event")
	}
}

// Without a client id nothing is deduplicated: a person typing the same thing
// twice means it twice.
func TestPromptsWithoutAClientIDAreNeverDeduplicated(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{{Content: "ok"}, {Content: "ok"}})
	s := h.create(t)
	ctx := context.Background()

	if _, err := h.m.Prompt(ctx, s.ID(), "again"); err != nil {
		t.Fatal(err)
	}
	h.m.WaitIdle(s.ID())
	if _, err := h.m.Prompt(ctx, s.ID(), "again"); err != nil {
		t.Fatal(err)
	}

	var prompts int
	for _, e := range s.Events() {
		if e.Type != protocol.EventMessage {
			continue
		}
		if d := protocol.MustData[protocol.MessageData](e); d.Role == "user" {
			prompts++
		}
	}
	if prompts != 2 {
		t.Errorf("got %d prompts, want both kept", prompts)
	}
}

// The dedup table is the log itself, so it survives a daemon restart. An
// outbox that retries after a restart is the case that matters.
func TestDeduplicationSurvivesReloadingTheSession(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{{Content: "ok"}})
	s := h.create(t)
	ctx := context.Background()

	first, err := h.m.PromptWithID(ctx, s.ID(), "queued offline", "outbox-restart")
	if err != nil {
		t.Fatal(err)
	}
	h.m.WaitIdle(s.ID())

	// A fresh manager over the same store is what a restart looks like.
	again := newManagerOver(t, h.store)
	second, err := again.PromptWithID(ctx, s.ID(), "queued offline", "outbox-restart")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Errorf("after a restart the retry appended again: %s vs %s", second.ID, first.ID)
	}
}

// newManagerOver builds a second Manager over an existing store, which is what
// a daemon restart looks like to a session.
func newManagerOver(t *testing.T, store *session.Store) *Manager {
	t.Helper()
	pr := provider.NewRegistry()
	pr.Add(provider.Config{Name: "fake", ContextWindow: 8000}, &provider.Fake{}, true)
	builtins := &tools.Builtins{}
	mr := module.NewRegistry([]module.Module{builtins}, module.Options{Log: testLogger()})
	m, err := New(Deps{Store: store, Providers: pr, Modules: mr, Builtins: builtins,
		Root: t.TempDir(), Log: testLogger()}, Config{DefaultModel: "fake/m"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Shutdown(context.Background()) })
	return m
}
