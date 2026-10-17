package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/corporealshift/rho/daemon/provider"
	"github.com/corporealshift/rho/protocol"
)

// Each assistant turn goes back with the thinking logged before it, for a
// provider that keeps thinking, and no other
// (docs/specs/2026-10-17-thinking-goes-back-design.md).
func TestBuildRequestKeepsThinking(t *testing.T) {
	log := mklog(t,
		protocol.EventSession, sess,
		protocol.EventMessage, protocol.MessageData{Role: "user", Content: "list files"},
		protocol.EventThinking, protocol.ThinkingData{Content: "I should run ls.", Source: "model"},
		protocol.EventMessage, protocol.MessageData{Role: "assistant", Content: ""},
		protocol.EventToolCall, protocol.ToolCallData{CallID: "c1", Tool: "bash", Arguments: json.RawMessage(`{"command":"ls"}`), Source: "model"},
		protocol.EventToolResult, protocol.ToolResultData{CallID: "c1", Tool: "bash", Content: "a.go", Status: "ok"},
		protocol.EventThinking, protocol.ThinkingData{Content: "One file.", Source: "model"},
		protocol.EventNotice, protocol.NoticeData{Source: "daemon", Level: "info", Message: "a passage repeated"},
		protocol.EventThinking, protocol.ThinkingData{Content: "Say so.", Source: "model"},
		protocol.EventMessage, protocol.MessageData{Role: "assistant", Content: "One file: a.go."},
		// A turn interrupted before its reply was logged: the person's
		// message comes next, and the thinking is no one's.
		protocol.EventThinking, protocol.ThinkingData{Content: "Orphaned.", Source: "model"},
		protocol.EventMessage, protocol.MessageData{Role: "user", Content: "and tests?"},
		protocol.EventMessage, protocol.MessageData{Role: "assistant", Content: "None."},
	)
	type turn struct{ role, content, reasoning string }
	for _, tc := range []struct {
		name string
		keep bool
		want []turn
	}{
		{"kept", true, []turn{
			{"user", "list files", ""},
			{"assistant", "", "I should run ls."},
			{"tool", "a.go", ""},
			{"assistant", "One file: a.go.", "One file.\n\nSay so."},
			{"user", "and tests?", ""},
			{"assistant", "None.", ""},
		}},
		{"not kept", false, []turn{
			{"user", "list files", ""},
			{"assistant", "", ""},
			{"tool", "a.go", ""},
			{"assistant", "One file: a.go.", ""},
			{"user", "and tests?", ""},
			{"assistant", "None.", ""},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msgs := buildRequest(log, "SYS", "qwen", nil, 0, tc.keep).Messages[1:]
			if len(msgs) != len(tc.want) {
				t.Fatalf("messages: %+v", msgs)
			}
			for i, w := range tc.want {
				if got := (turn{msgs[i].Role, msgs[i].Content, msgs[i].Reasoning}); got != w {
					t.Errorf("message %d: %+v, want %+v", i, got, w)
				}
			}
			if !strings.Contains(msgs[1].ToolCalls[0].Name, "bash") {
				t.Errorf("the tool call left its message: %+v", msgs[1])
			}
		})
	}
}

// Through the session: the second request carries the first reply's thinking.
func TestASessionSendsItsThinkingBack(t *testing.T) {
	for _, keep := range []bool{true, false} {
		h := newHarnessWithProvider(t, provider.Config{Name: "fake", ContextWindow: 8000, KeepThinking: keep},
			[]provider.Response{
				{Reasoning: "Look for Go files first.", ToolCalls: globCall("c1")},
				{Content: "none"},
			})
		s := h.create(t)
		h.m.Prompt(context.Background(), s.ID(), "go")
		h.m.WaitIdle(s.ID())
		second := h.fake.Calls[1].Messages
		var got string
		for _, m := range second {
			if m.Role == "assistant" {
				got = m.Reasoning
			}
		}
		want := ""
		if keep {
			want = "Look for Go files first."
		}
		if got != want {
			t.Errorf("keep %v: the second request's assistant turn carries %q, want %q", keep, got, want)
		}
	}
}

// The estimate counts the last reply's thinking only when it goes back.
func TestNextRequestTokensCountsKeptThinking(t *testing.T) {
	ev := func(typ protocol.EventType, data any) protocol.Event {
		raw, _ := json.Marshal(data)
		return protocol.Event{Type: typ, Data: raw}
	}
	thought := ev(protocol.EventThinking, protocol.ThinkingData{Content: strings.Repeat("t", 4000), Source: "model"})
	notice := ev(protocol.EventNotice, protocol.NoticeData{Source: "daemon", Level: "info", Message: "repeated"})
	reply := ev(protocol.EventMessage, protocol.MessageData{Role: "assistant", Content: "ok",
		Usage: &protocol.Usage{InputTokens: 47000, OutputTokens: 3000}})
	log := []protocol.Event{thought, notice, reply}
	if got, want := nextRequestTokens(log, false), 47000+len("ok")/charsPerToken+requestMargin; got != want {
		t.Errorf("not kept: %d, want %d", got, want)
	}
	if got, want := nextRequestTokens(log, true), 47000+(4000+len("ok"))/charsPerToken+requestMargin; got != want {
		t.Errorf("kept: %d, want %d", got, want)
	}
}
