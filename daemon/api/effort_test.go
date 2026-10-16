package api

import (
	"testing"

	"github.com/corporealshift/rho/protocol"
)

// A session's effort is set at creation, recorded in its session event,
// changed and cleared with set_option; anything but a level or "" is refused
// and appends nothing (docs/specs/2026-10-16-session-effort-design.md).
func TestEffortRoundTrips(t *testing.T) {
	hn := newHarness(t)
	var out struct {
		SessionID string         `json:"session_id"`
		Event     protocol.Event `json:"event"`
	}
	result(t, hn.call(t, 1, "rho.session.create", map[string]any{"workspace": hn.dir,
		"options": map[string]any{"effort": "medium"}}), &out)
	if d := protocol.MustData[protocol.SessionData](out.Event); d.Options.Effort != protocol.EffortMedium {
		t.Errorf("session event effort = %q, want medium", d.Options.Effort)
	}

	var set struct {
		EventID string `json:"event_id"`
	}
	s, _ := hn.h.getSession(out.SessionID)
	for i, v := range []string{"max", ""} {
		result(t, hn.call(t, 2+i, "rho.session.set_option",
			map[string]any{"session_id": out.SessionID, "key": "effort", "value": v}), &set)
		if st := s.State(); st.Options.Effort != v || st.State != protocol.StateIdle {
			t.Errorf("after set_option %q: effort %q, state %q", v, st.Options.Effort, st.State)
		}
	}

	before := len(s.Events())
	for _, v := range []any{"extreme", "HIGH", 3, nil} {
		resp := hn.call(t, 5, "rho.session.set_option", map[string]any{"session_id": out.SessionID, "key": "effort", "value": v})
		if code := rpcCode(t, resp); code != protocol.CodeInvalidParams {
			t.Errorf("effort %v: code %d, want invalid params", v, code)
		}
	}
	if after := len(s.Events()); after != before {
		t.Errorf("refused efforts appended %d events", after-before)
	}

	resp := hn.call(t, 6, "rho.session.create", map[string]any{"workspace": hn.dir,
		"options": map[string]any{"effort": "extreme"}})
	if code := rpcCode(t, resp); code != protocol.CodeInvalidParams {
		t.Errorf("create with effort extreme: code %d, want invalid params", code)
	}
}
