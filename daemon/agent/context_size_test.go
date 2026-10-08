package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/protocol"
)

// Which compaction a request size calls for, by the provider's settings
// (docs/specs/2026-10-08-context-size-design.md).
func TestTheStageFollowsTheProvidersWindowAndThresholds(t *testing.T) {
	daemon := Config{}.withDefaults().Compaction
	local := provider.Config{ContextWindow: 128000, NormalWindow: 64000}
	noClear := provider.Config{ContextWindow: 128000, NormalWindow: 64000, ClearAt: -1}
	for _, tc := range []struct {
		name  string
		pcfg  provider.Config
		input int
		want  protocol.CompactionMode
	}{
		{"nothing set: below 0.70 of the model's window", provider.Config{ContextWindow: 128000}, 80000, ""},
		{"nothing set: clears at 0.70 of it", provider.Config{ContextWindow: 128000}, 90000, protocol.CompactionClearResults},
		{"nothing set: summarizes at 0.85 of it", provider.Config{ContextWindow: 128000}, 109000, protocol.CompactionSummarize},
		{"a normal window: nothing below its 0.70", local, 44000, ""},
		{"a normal window: clears at its 0.70", local, 46000, protocol.CompactionClearResults},
		{"a normal window: summarizes at its 0.85", local, 55000, protocol.CompactionSummarize},
		{"clearing off: never clears", noClear, 50000, ""},
		{"clearing off: still summarizes", noClear, 55000, protocol.CompactionSummarize},
		{"its own summarize_at", provider.Config{ContextWindow: 100000, SummarizeAt: 0.5, ClearAt: -1}, 50000, protocol.CompactionSummarize},
		{"its own clear_at", provider.Config{ContextWindow: 100000, ClearAt: 0.4}, 41000, protocol.CompactionClearResults},
		{"a normal window not below the model's is ignored", provider.Config{ContextWindow: 64000, NormalWindow: 128000}, 50000, protocol.CompactionClearResults},
		{"no window, no compaction", provider.Config{NormalWindow: 64000}, 1000000, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stageFor(tc.input, tc.pcfg, daemon); got != tc.want {
				t.Errorf("stageFor(%d) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// With clearing off, a session that would have cleared carries on, and one
// over the summarize line summarizes.
func TestASessionWithClearingOffOnlySummarizes(t *testing.T) {
	h := newHarnessWithProvider(t, provider.Config{Name: "fake", ContextWindow: 16000, NormalWindow: 8000, ClearAt: -1},
		[]provider.Response{
			{ToolCalls: globCall("c1"), Usage: bigUsage(0.10)},
			{ToolCalls: globCall("c2"), Usage: bigUsage(0.75)}, // would clear at 0.70 of 8000
			{ToolCalls: globCall("c3"), Usage: bigUsage(0.90)}, // over 0.85 of 8000
			{Content: "the summary"},
			{Content: "done", Usage: bigUsage(0.20)},
		})
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())

	var modes []protocol.CompactionMode
	for _, e := range s.Events() {
		if e.Type == protocol.EventCompaction {
			modes = append(modes, protocol.MustData[protocol.CompactionData](e).Mode)
		}
	}
	if len(modes) != 1 || modes[0] != protocol.CompactionSummarize {
		t.Fatalf("compactions = %v, want one summarize and no clear", modes)
	}
}

// The reply cap protects the server's real room, which a normal window does
// not change.
func TestTheReplyCapIgnoresTheNormalWindow(t *testing.T) {
	data, _ := json.Marshal(protocol.MessageData{Role: "assistant", Usage: &protocol.Usage{InputTokens: 60000}})
	log := []protocol.Event{{Type: protocol.EventMessage, Data: data}}
	got := replyCap(log, provider.Config{MaxTokens: 16384, ContextWindow: 128000, NormalWindow: 64000}, 0)
	if got != 16384 {
		t.Errorf("reply cap = %d, want the provider's 16384: 68K of room remains", got)
	}
}
