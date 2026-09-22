package provider

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/corporealshift/nabu/protocol"
)

// Recovering a tool call the model wrote into its reasoning.
//
// llama-server splits a reasoning model's output into `reasoning_content` and
// `content` by looking for the thinking delimiters. When the model closes that
// section badly — or does not close it at all — everything after it, the tool
// call included, is reported as reasoning. The call never reaches `tool_calls`,
// so the turn ends with no call and an empty answer, and the agent stops in the
// middle of the work (issue 80).
//
// The call itself is well formed; only its routing is wrong. Parsing it back
// out turns a stalled session into one that carries on.
//
// Deliberately only from the reasoning stream, never from the answer. Reasoning
// is never sent back to the model and never shown as the reply, so text that
// looks like a call almost certainly is one. An answer is different: a model
// explaining tool syntax to a reader would have its explanation executed.
var (
	callBlock = regexp.MustCompile(`(?s)<tool_call>(.*?)</tool_call>`)
	callName  = regexp.MustCompile(`<function=([^>\s]+)\s*>`)
	callParam = regexp.MustCompile(`(?s)<parameter=([^>\s]+)\s*>(.*?)</parameter>`)
)

// recoverLeakedCalls repairs a response whose tool calls went to the wrong
// stream. It does nothing when the provider reported calls properly: a model
// that both called a tool and described one in its reasoning must not have the
// description run as well.
func recoverLeakedCalls(resp Response, tools []ToolSpec) Response {
	if len(resp.ToolCalls) > 0 || resp.Reasoning == "" {
		return resp
	}
	calls, cleaned := parseLeakedCalls(resp.Reasoning, tools)
	if len(calls) == 0 {
		return resp
	}
	resp.ToolCalls = calls
	resp.Reasoning = cleaned
	resp.Recovered = len(calls)
	// The turn did request tools, whatever the server decided to call it.
	resp.FinishReason = "tool_calls"
	return resp
}

// parseLeakedCalls returns the calls found in text, and text with those blocks
// removed. The blocks are stripped because they are not a thought: leaving them
// shows the reader the same call twice, once as prose and once as the call.
func parseLeakedCalls(text string, tools []ToolSpec) ([]ToolCall, string) {
	blocks := callBlock.FindAllStringSubmatch(text, -1)
	if len(blocks) == 0 {
		return nil, text
	}

	var calls []ToolCall
	for _, block := range blocks {
		name := callName.FindStringSubmatch(block[1])
		if name == nil {
			continue // a block with no function names nothing to run
		}
		args := map[string]json.RawMessage{}
		for _, p := range callParam.FindAllStringSubmatch(block[1], -1) {
			args[p[1]] = coerce(unwrap(p[2]), propertyType(tools, name[1], p[1]))
		}
		raw, err := json.Marshal(args)
		if err != nil {
			continue
		}
		calls = append(calls, ToolCall{ID: protocol.NewULID(), Name: name[1], Arguments: raw})
	}
	if len(calls) == 0 {
		return nil, text
	}
	return calls, strings.TrimSpace(callBlock.ReplaceAllString(text, ""))
}

// unwrap removes the single newline this syntax puts either side of a value,
// and no more. The `edit` tool matches its `old` against the file exactly, so a
// trailing blank line inside the value is content, not padding.
func unwrap(v string) string {
	for _, nl := range []string{"\r\n", "\n"} {
		if strings.HasPrefix(v, nl) {
			v = v[len(nl):]
			break
		}
	}
	for _, nl := range []string{"\r\n", "\n"} {
		if strings.HasSuffix(v, nl) {
			return v[:len(v)-len(nl)]
		}
	}
	return v
}

// coerce turns a parameter's text into JSON of the type the tool declares.
// Every value in this syntax arrives as text, and a tool that wants an integer
// will not take "50".
//
// A value that does not parse as the declared type is passed through as a
// string rather than dropped: the tool then refuses it and says why, which the
// model can act on. Guessing a substitute would be worse.
func coerce(text, kind string) json.RawMessage {
	switch kind {
	case "integer", "number", "boolean", "array", "object":
		if trimmed := strings.TrimSpace(text); json.Valid([]byte(trimmed)) {
			return json.RawMessage(trimmed)
		}
	}
	b, err := json.Marshal(text)
	if err != nil {
		return json.RawMessage(`""`)
	}
	return b
}

// propertyType reads one parameter's declared type out of a tool's schema, or
// "" when the tool or the parameter is not described — an unknown tool still
// gets its call rebuilt, so the agent can answer with "unknown tool" instead of
// stopping silently.
func propertyType(tools []ToolSpec, tool, param string) string {
	for _, t := range tools {
		if t.Name != tool {
			continue
		}
		var s struct {
			Properties map[string]struct {
				Type string `json:"type"`
			} `json:"properties"`
		}
		if json.Unmarshal(t.Parameters, &s) != nil {
			return ""
		}
		return s.Properties[param].Type
	}
	return ""
}
