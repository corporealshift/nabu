package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/corporealshift/rho/daemon/provider"
	"github.com/corporealshift/rho/protocol"
)

// inSession is a provider that asks for summaries as the session's next turn,
// written without thinking (docs/specs/2026-10-16-summary-in-session-design.md).
func inSession() provider.Config {
	off := false
	return provider.Config{Name: "fake", ContextWindow: 8000, SummaryThinking: &off, SummaryInSession: true}
}

func toolNames(specs []provider.ToolSpec) []string {
	var out []string
	for _, s := range specs {
		out = append(out, s.Name)
	}
	return out
}

// The summary request is the last turn's request and what followed it, so a
// server's prompt cache holds all but its end.
func TestASummaryInSessionContinuesTheLastRequest(t *testing.T) {
	h := newHarnessWithProvider(t, inSession(), []provider.Response{
		{ToolCalls: globCall("c1"), Usage: bigUsage(0.90)}, // crosses 0.85
		{Content: "THE SUMMARY"},
		{Content: "done", Usage: bigUsage(0.10)},
	})
	s, err := h.m.Create(context.Background(), h.dir, CreateOptions{Effort: protocol.EffortLow})
	if err != nil {
		t.Fatal(err)
	}
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())

	turn, sum := h.fake.Calls[0], h.fake.Calls[1]
	if len(sum.Messages) != len(turn.Messages)+3 {
		t.Fatalf("summary messages: %d, want the turn's %d + reply, result and instruction", len(sum.Messages), len(turn.Messages))
	}
	if !reflect.DeepEqual(sum.Messages[:len(turn.Messages)], turn.Messages) {
		t.Errorf("the summary request must start with the last turn's messages:\n%+v\n%+v", sum.Messages[:len(turn.Messages)], turn.Messages)
	}
	if got, want := toolNames(sum.Tools), toolNames(turn.Tools); !reflect.DeepEqual(got, want) || len(got) == 0 {
		t.Errorf("summary tools %v, want the turn's %v", got, want)
	}
	if r := sum.Messages[len(turn.Messages)]; r.Role != "assistant" || len(r.ToolCalls) != 1 {
		t.Errorf("after the turn's messages comes its reply: %+v", r)
	}
	last := sum.Messages[len(sum.Messages)-1]
	if last.Role != "user" || !strings.HasPrefix(last.Content, "Stop the work here") ||
		!strings.Contains(last.Content, "under about 2,000 words") || !strings.HasSuffix(last.Content, summarizeTurnClosing) {
		t.Errorf("the summary instruction must come last:\n%+v", last)
	}
	if sum.Effort != protocol.EffortLow || !sum.NoThinking || sum.MaxTokens <= 0 || sum.MaxTokens > summaryCap {
		t.Errorf("effort %q, no thinking %v, max tokens %d: want low, true, 1..%d", sum.Effort, sum.NoThinking, sum.MaxTokens, summaryCap)
	}

	var cd *protocol.CompactionData
	for _, e := range s.Events() {
		if e.Type == protocol.EventCompaction {
			cd = protocol.MustData[protocol.CompactionData](e)
		}
	}
	if cd == nil || cd.Summary != "THE SUMMARY" {
		t.Fatalf("summarize event: %+v", cd)
	}
	if sys := h.fake.Calls[2].Messages[0].Content; !strings.Contains(sys, "THE SUMMARY") {
		t.Errorf("the next turn must carry the summary: %q", sys)
	}
}

// Anything but a summary falls back to the transcript request, which is what
// every summary was before, and says why.
func TestASummaryInSessionFallsBackToATranscript(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply provider.Response
		err   error
		why   string
	}{
		{"error", provider.Response{}, errors.New("the server refused the size"), "the server refused the size"},
		{"empty", provider.Response{Content: "   "}, nil, "returned nothing"},
		{"tool call", provider.Response{ToolCalls: globCall("c9")}, nil, "called glob instead"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarnessWithProvider(t, inSession(), []provider.Response{
				{Content: "done"}, tc.reply, {Content: "THE SUMMARY"},
			})
			if tc.err != nil {
				h.fake.Errors = map[int]error{1: tc.err}
			}
			s := h.primed(t)
			ev, err := h.m.Compact(context.Background(), s.ID())
			if err != nil {
				t.Fatal(err)
			}
			if d := protocol.MustData[protocol.CompactionData](ev); d.Mode != protocol.CompactionSummarize || d.Summary != "THE SUMMARY" {
				t.Fatalf("compaction: %+v", d)
			}
			if n := len(h.fake.Calls); n != 3 {
				t.Fatalf("calls: %d, want the turn, the summary turn and the transcript", n)
			}
			fallback := h.fake.Calls[2]
			if user := fallback.Messages[len(fallback.Messages)-1].Content; !strings.HasPrefix(user, "<conversation>\n") {
				t.Errorf("the fallback must be the transcript request:\n%s", user)
			}
			var said bool
			for _, e := range s.Events() {
				if e.Type == protocol.EventNotice {
					said = said || strings.Contains(protocol.MustData[protocol.NoticeData](e).Message, tc.why)
				}
			}
			if !said {
				t.Errorf("no notice saying %q", tc.why)
			}
		})
	}
}

// Off, a summary is the transcript request alone.
func TestASummaryIsATranscriptByDefault(t *testing.T) {
	cfg := inSession()
	cfg.SummaryInSession = false
	h := newHarnessWithProvider(t, cfg, []provider.Response{{Content: "done"}, {Content: "THE SUMMARY"}})
	s := h.primed(t)
	if _, err := h.m.Compact(context.Background(), s.ID()); err != nil {
		t.Fatal(err)
	}
	if n := len(h.fake.Calls); n != 2 {
		t.Fatalf("calls: %d, want the turn and one summary", n)
	}
	sum := h.fake.Calls[1]
	if len(sum.Messages) != 2 || len(sum.Tools) != 0 || !strings.HasPrefix(sum.Messages[1].Content, "<conversation>\n") {
		t.Errorf("summary request: %+v", sum)
	}
}
