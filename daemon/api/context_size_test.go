package api

import (
	"testing"

	"github.com/corporealshift/nabu/protocol"
)

// A session's context size is set at creation, recorded in its session event,
// and changed with set_option; anything but normal or large is refused and
// appends nothing (docs/specs/2026-10-08-context-size-design.md).
func TestContextSizeRoundTrips(t *testing.T) {
	hn := newHarness(t)
	var out struct {
		SessionID string         `json:"session_id"`
		Event     protocol.Event `json:"event"`
	}
	result(t, hn.call(t, 1, "nabu.session.create", map[string]any{"workspace": hn.dir,
		"options": map[string]any{"context": "large"}}), &out)
	if d := protocol.MustData[protocol.SessionData](out.Event); d.Options.Context != protocol.ContextLarge {
		t.Errorf("session event context = %q, want large", d.Options.Context)
	}

	var set struct {
		EventID string `json:"event_id"`
	}
	result(t, hn.call(t, 2, "nabu.session.set_option",
		map[string]any{"session_id": out.SessionID, "key": "context", "value": "normal"}), &set)
	s, _ := hn.h.getSession(out.SessionID)
	if st := s.State(); st.Options.Context != protocol.ContextNormal || st.State != protocol.StateIdle {
		t.Errorf("after set_option: context %q, state %q", st.Options.Context, st.State)
	}

	before := len(s.Events())
	for _, v := range []any{"huge", "", 3} {
		resp := hn.call(t, 3, "nabu.session.set_option", map[string]any{"session_id": out.SessionID, "key": "context", "value": v})
		if code := rpcCode(t, resp); code != protocol.CodeInvalidParams {
			t.Errorf("context %v: code %d, want invalid params", v, code)
		}
	}
	if after := len(s.Events()); after != before {
		t.Errorf("refused sizes appended %d events", after-before)
	}

	resp := hn.call(t, 4, "nabu.session.create", map[string]any{"workspace": hn.dir,
		"options": map[string]any{"context": "huge"}})
	if code := rpcCode(t, resp); code != protocol.CodeInvalidParams {
		t.Errorf("create with context huge: code %d, want invalid params", code)
	}
}

// A session created without a size is normal, and says so.
func TestContextSizeDefaultsToNormal(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)
	s, _ := hn.h.getSession(id)
	if c := s.State().Options.Context; c != protocol.ContextNormal {
		t.Errorf("context = %q, want normal", c)
	}
}
