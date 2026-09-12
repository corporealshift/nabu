package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/protocol"
)

// bigUsage reports input token usage at the given fraction of the harness's
// 8000-token window, so a test can drive the thresholds.
func bigUsage(frac float64) protocol.Usage {
	return protocol.Usage{InputTokens: int(8000 * frac), OutputTokens: 10}
}

func globCall(id string) []provider.ToolCall {
	return []provider.ToolCall{{ID: id, Name: "glob", Arguments: json.RawMessage(`{"pattern":"*.none"}`)}}
}

func TestClearResultsFiresAtTheLowerThreshold(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{
		{ToolCalls: globCall("c1"), Usage: bigUsage(0.10)},
		{ToolCalls: globCall("c2"), Usage: bigUsage(0.75)}, // crosses 0.70
		{Content: "done", Usage: bigUsage(0.20)},
	})
	h.m.cfg.Compaction.KeepTurns = 1
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())

	var modes []string
	for _, e := range s.Events() {
		if e.Type == protocol.EventCompaction {
			modes = append(modes, string(protocol.MustData[protocol.CompactionData](e).Mode))
		}
	}
	if strings.Join(modes, ",") != "clear_results" {
		t.Fatalf("compactions: %v", modes)
	}
	last := h.fake.Calls[len(h.fake.Calls)-1]
	var stubs int
	for _, msg := range last.Messages {
		if msg.Role == "tool" && strings.HasPrefix(msg.Content, "[result cleared:") {
			stubs++
		}
	}
	if stubs != 1 {
		t.Fatalf("expected one cleared result, got %d: %+v", stubs, last.Messages)
	}
}

// preserver asks the summary to keep a string and re-injects a prefix block.
type preserver struct{ before, after int }

func (p *preserver) Name() string                          { return "preserver" }
func (p *preserver) Init(module.Host, module.Config) error { return nil }
func (p *preserver) BeforeCompaction(context.Context, module.Session, module.Range) []string {
	p.before++
	return []string{"the user's deploy key is never to be printed"}
}
func (p *preserver) AfterCompaction(context.Context, module.Session) ([]module.ContextBlock, error) {
	p.after++
	return []module.ContextBlock{{Slot: "prefix", Content: "# Skills (re-injected)"}}, nil
}

func TestSummarizeFiresAtTheUpperThreshold(t *testing.T) {
	p := &preserver{}
	h := newHarness(t, []module.Module{p}, []provider.Response{
		{ToolCalls: globCall("c1"), Usage: bigUsage(0.90)}, // crosses 0.85
		{Content: "THE SUMMARY"},                           // the summarizer call
		{Content: "done", Usage: bigUsage(0.10)},
	})
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())

	var cd *protocol.CompactionData
	for _, e := range s.Events() {
		if e.Type == protocol.EventCompaction {
			d := protocol.MustData[protocol.CompactionData](e)
			if d.Mode == protocol.CompactionSummarize {
				cd = d
			}
		}
	}
	if cd == nil || cd.Summary != "THE SUMMARY" {
		t.Fatalf("summarize event: %+v", cd)
	}
	if p.before != 1 || p.after != 1 {
		t.Fatalf("hooks: before=%d after=%d", p.before, p.after)
	}
	sum := h.fake.Calls[1]
	var joined strings.Builder
	for _, msg := range sum.Messages {
		joined.WriteString(msg.Content)
	}
	if !strings.Contains(joined.String(), "deploy key") {
		t.Fatalf("preserve string missing from the summary prompt: %s", joined.String())
	}
	last := h.fake.Calls[len(h.fake.Calls)-1]
	sys := last.Messages[0].Content
	if !strings.Contains(sys, "THE SUMMARY") || !strings.Contains(sys, "# Skills (re-injected)") ||
		!strings.Contains(sys, "## Current state") {
		t.Fatalf("final system message: %q", sys)
	}
	var msgs int
	for _, e := range s.Events() {
		if e.Type == protocol.EventMessage {
			msgs++
		}
	}
	if msgs < 3 {
		t.Fatalf("compaction must not delete events, found %d messages", msgs)
	}
}

func TestCompactionDisabledHardStops(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{
		{ToolCalls: globCall("c1"), Usage: bigUsage(0.95)},
	})
	s := h.create(t)
	if _, err := h.m.SetOption(context.Background(), s.ID(), "compaction_enabled", false); err != nil {
		t.Fatal(err)
	}
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())

	if st := s.State(); st.State != protocol.StateBlocked {
		t.Fatalf("state: %s", st.State)
	}
	var sawNotice bool
	for _, e := range s.Events() {
		if e.Type == protocol.EventNotice &&
			strings.Contains(protocol.MustData[protocol.NoticeData](e).Message, "context limit") {
			sawNotice = true
		}
	}
	if !sawNotice {
		t.Fatal("a hard stop must say why")
	}
}
