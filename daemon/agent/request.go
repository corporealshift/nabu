package agent

import (
	"strings"

	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/protocol"
)

// buildRequest turns the log into a provider request (protocol spec §6).
// The system message is systemPrompt + prefix context blocks + (after a
// summarize) the summary and Current state block. Message and tool events
// follow, with tool calls folded into their assistant message. Suffix blocks
// and outstanding vetoes become one trailing user message.
func buildRequest(log []protocol.Event, systemPrompt, model string, tools []provider.ToolSpec, maxTokens int) provider.Request {
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
	var trailing []string
	flush := func() {
		if pending != nil {
			msgs = append(msgs, *pending)
			pending = nil
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
		case protocol.SegMessage:
			flush()
			d := protocol.MustData[protocol.MessageData](byID[s.ID])
			m := provider.Message{Role: d.Role, Content: d.Content}
			if d.Role == "assistant" {
				if d.Interrupted && m.Content == "" {
					m.Content = "(interrupted)"
				}
				pending = &m
			} else {
				msgs = append(msgs, m)
			}
		case protocol.SegToolCall:
			d := protocol.MustData[protocol.ToolCallData](byID[s.ID])
			if pending == nil {
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
		case protocol.SegSuffixContext, protocol.SegVetoes:
			trailing = append(trailing, s.Text)
		}
	}
	flush()
	if len(trailing) > 0 {
		msgs = append(msgs, provider.Message{Role: "user", Content: strings.Join(trailing, "\n\n")})
	}
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
