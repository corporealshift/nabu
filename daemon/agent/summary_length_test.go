package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/corporealshift/rho/daemon/provider"
	"github.com/corporealshift/rho/protocol"
)

// Summaries name a length and, written without thinking, have a cap
// (docs/specs/2026-10-16-summary-length-design.md).
func TestASummaryWithoutThinkingIsCapped(t *testing.T) {
	off := false
	for _, tc := range []struct {
		name     string
		thinking *bool
		want     int
	}{
		{"thinking off", &off, summaryCap},
		{"thinking left to the server", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarnessWithProvider(t, provider.Config{Name: "fake", ContextWindow: 8000, SummaryThinking: tc.thinking},
				[]provider.Response{{Content: "done"}, {Content: "THE SUMMARY"}})
			s := h.primed(t)
			if _, err := h.m.Compact(context.Background(), s.ID()); err != nil {
				t.Fatal(err)
			}
			req := h.fake.Calls[len(h.fake.Calls)-1]
			if req.MaxTokens != tc.want {
				t.Errorf("summary max_tokens = %d, want %d", req.MaxTokens, tc.want)
			}
			if !strings.Contains(req.Messages[0].Content, "under about 2,000 words") {
				t.Errorf("the summary prompt names no length:\n%s", req.Messages[0].Content)
			}
		})
	}
}

// A summary the cap cut off still lands, and says so.
func TestASummaryCutOffByTheCapIsKeptWithAWarning(t *testing.T) {
	off := false
	h := newHarnessWithProvider(t, provider.Config{Name: "fake", ContextWindow: 8000, SummaryThinking: &off},
		[]provider.Response{{Content: "done"}, {Content: "THE SUMMARY, CUT", FinishReason: "length"}})
	s := h.primed(t)
	ev, err := h.m.Compact(context.Background(), s.ID())
	if err != nil {
		t.Fatal(err)
	}
	d := protocol.MustData[protocol.CompactionData](ev)
	if d.Mode != protocol.CompactionSummarize || d.Summary != "THE SUMMARY, CUT" {
		t.Fatalf("compaction: %+v", d)
	}
	var warned bool
	for _, e := range s.Events() {
		if e.Type == protocol.EventNotice {
			n := protocol.MustData[protocol.NoticeData](e)
			warned = warned || (n.Level == "warn" && strings.Contains(n.Message, "6144-token cap"))
		}
	}
	if !warned {
		t.Error("no warning that the summary was cut off")
	}
}
