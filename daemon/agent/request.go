package agent

import (
	"strings"

	"github.com/corporealshift/rho/daemon/provider"
	"github.com/corporealshift/rho/protocol"
)

// buildRequest turns the log into a provider request (protocol spec §6).
// The system message is systemPrompt + prefix context blocks + (after a
// summarize) the summary and Current state block. Message and tool events
// follow, with tool calls folded into their assistant message. A suffix block
// is a user message where it was logged, joined with any suffix block next to
// it; outstanding vetoes are one trailing user message. With keepThinking,
// an assistant message carries the thinking logged just before it.
func buildRequest(log []protocol.Event, systemPrompt, model string, tools []provider.ToolSpec, maxTokens int, keepThinking bool) provider.Request {
	byID := make(map[string]protocol.Event, len(log))
	for _, e := range log {
		byID[e.ID] = e
	}
	segs := protocol.Assemble(log)

	var sys strings.Builder
	sys.WriteString(systemPrompt)
	if d, ok := firstOf[protocol.SessionData](log); ok {
		sys.WriteString("\n\nWorkspace: " + d.Workspace)
	}
	var msgs []provider.Message
	var pending *provider.Message // assistant message collecting tool calls
	var suffix []string           // adjacent suffix blocks, one user message
	var thought []string          // thinking for the next message, if it is the assistant's
	flush := func() {
		if pending != nil {
			msgs = append(msgs, *pending)
			pending = nil
		}
		if len(suffix) > 0 {
			msgs = append(msgs, provider.Message{Role: "user", Content: strings.Join(suffix, "\n\n")})
			suffix = nil
		}
	}
	for _, s := range segs {
		switch s.Kind {
		case protocol.SegPrefixContext:
			sys.WriteString("\n\n" + s.Text)
		case protocol.SegSummary:
			sys.WriteString("\n\n## Earlier conversation (summarized)\n" + s.Text)
		case protocol.SegState:
			sys.WriteString("\n\n" + s.Text)
		case protocol.SegThinking:
			if keepThinking {
				thought = append(thought, protocol.MustData[protocol.ThinkingData](byID[s.ID]).Content)
			}
		case protocol.SegMessage:
			flush()
			d := protocol.MustData[protocol.MessageData](byID[s.ID])
			m := provider.Message{Role: d.Role, Content: d.Content}
			if d.Role == "assistant" {
				m.Reasoning = strings.Join(thought, "\n\n")
				if d.Interrupted && m.Content == "" {
					m.Content = "(interrupted)"
				}
				pending = &m
			} else {
				msgs = append(msgs, m)
			}
			// Thinking is the next message's only; before a person's, it is
			// from a turn that never logged its reply.
			thought = nil
		case protocol.SegToolCall:
			d := protocol.MustData[protocol.ToolCallData](byID[s.ID])
			if pending == nil {
				flush()
				pending = &provider.Message{Role: "assistant"}
			}
			pending.ToolCalls = append(pending.ToolCalls, provider.ToolCall{
				ID: d.CallID, Name: d.Tool, Arguments: d.Arguments})
		case protocol.SegToolResult:
			flush()
			d := protocol.MustData[protocol.ToolResultData](byID[s.ID])
			msgs = append(msgs, provider.Message{Role: "tool", ToolCallID: d.CallID, Content: d.Content})
		case protocol.SegToolResultCleared:
			flush()
			d := protocol.MustData[protocol.ToolResultData](byID[s.ID])
			msgs = append(msgs, provider.Message{Role: "tool", ToolCallID: d.CallID, Content: s.Text})
		case protocol.SegSuffixContext:
			if pending != nil {
				msgs = append(msgs, *pending)
				pending = nil
			}
			suffix = append(suffix, s.Text)
		case protocol.SegVetoes:
			flush()
			msgs = append(msgs, provider.Message{Role: "user", Content: s.Text})
		}
	}
	flush()
	all := append([]provider.Message{{Role: "system", Content: sys.String()}}, msgs...)
	return provider.Request{Model: model, Messages: all, Tools: tools, MaxTokens: maxTokens}
}

// firstOf decodes the first event whose data has the given type.
func firstOf[T any](log []protocol.Event) (*T, bool) {
	for _, e := range log {
		if v, err := protocol.DecodeData(e); err == nil {
			if t, ok := v.(*T); ok {
				return t, true
			}
		}
	}
	return nil, false
}
