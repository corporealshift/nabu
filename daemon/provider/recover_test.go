package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

// The blocks below are copied verbatim out of session logs where the agent
// stopped mid-task (issue 80). They are not invented: the shape, the newline
// around every value and the indentation inside `old` are what the model
// actually emitted.
const (
	leakedBash = "<tool_call>\n<function=bash>\n<parameter=command>\n" +
		"find crates -name \"*.rs\" | sort\n</parameter>\n</function>\n</tool_call>"

	leakedEdit = "<tool_call>\n<function=edit>\n<parameter=path>\n" +
		"crates/breezeway-core/src/engine.rs\n</parameter>\n<parameter=old>\n" +
		"    // Stage 5: apply quiet hours\n" +
		"    let after_quiet = apply_quiet_hours(intent, &actions, settings, now_local.naive_local(), tz);\n" +
		"</parameter>\n<parameter=new>\n" +
		"    // Stage 5: apply quiet hours\n" +
		"    let after_quiet = apply_quiet_hours(intent, &actions, settings, now_local, tz);\n" +
		"</parameter>\n</function>\n</tool_call>"

	leakedRead = "<tool_call>\n<function=read>\n<parameter=path>\n" +
		"crates/breezeway-core/src/engine.rs\n</parameter>\n<parameter=limit>\n50\n" +
		"</parameter>\n<parameter=offset>\n365\n</parameter>\n</function>\n</tool_call>"
)

// The tools as the daemon describes them, which is what gives the values types.
var testTools = []ToolSpec{
	{Name: "read", Parameters: json.RawMessage(
		`{"type":"object","properties":{"path":{"type":"string"},` +
			`"offset":{"type":"integer"},"limit":{"type":"integer"}}}`)},
	{Name: "edit", Parameters: json.RawMessage(
		`{"type":"object","properties":{"path":{"type":"string"},` +
			`"old":{"type":"string"},"new":{"type":"string"}}}`)},
	{Name: "bash", Parameters: json.RawMessage(
		`{"type":"object","properties":{"command":{"type":"string"},` +
			`"timeout_seconds":{"type":"integer"}}}`)},
}

func args(t *testing.T, c ToolCall) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(c.Arguments, &m); err != nil {
		t.Fatalf("arguments are not an object: %v (%s)", err, c.Arguments)
	}
	return m
}

func TestALeakedCallIsRecovered(t *testing.T) {
	resp := recoverLeakedCalls(Response{
		Reasoning:    "Let me look at the tree.\n\n" + leakedBash,
		FinishReason: "stop",
	}, testTools)

	if len(resp.ToolCalls) != 1 {
		t.Fatalf("calls: %+v", resp.ToolCalls)
	}
	if resp.ToolCalls[0].Name != "bash" {
		t.Fatalf("name: %q", resp.ToolCalls[0].Name)
	}
	if got := args(t, resp.ToolCalls[0])["command"]; got != `find crates -name "*.rs" | sort` {
		t.Fatalf("command: %q", got)
	}
	// The turn asked for a tool, whatever the server called it. A "stop" here
	// is what let the loop end in the first place.
	if resp.FinishReason != "tool_calls" {
		t.Fatalf("finish reason: %q", resp.FinishReason)
	}
	if resp.Recovered != 1 {
		t.Fatalf("recovered: %d", resp.Recovered)
	}
}

func TestTheCallIsStrippedFromTheThinking(t *testing.T) {
	resp := recoverLeakedCalls(Response{
		Reasoning: "Let me look at the tree.\n\n" + leakedBash,
	}, testTools)

	if strings.Contains(resp.Reasoning, "<tool_call>") {
		t.Fatalf("the block is a call, not a thought: %q", resp.Reasoning)
	}
	if resp.Reasoning != "Let me look at the tree." {
		t.Fatalf("the thought itself must survive: %q", resp.Reasoning)
	}
}

// The whole point: a value the tool will accept, not the text of one.
func TestDeclaredNumbersAreNumbersNotStrings(t *testing.T) {
	resp := recoverLeakedCalls(Response{Reasoning: leakedRead}, testTools)

	a := args(t, resp.ToolCalls[0])
	if a["limit"] != float64(50) || a["offset"] != float64(365) {
		t.Fatalf("limit and offset must be numbers, got %#v and %#v", a["limit"], a["offset"])
	}
	if a["path"] != "crates/breezeway-core/src/engine.rs" {
		t.Fatalf("path: %#v", a["path"])
	}
}

// `edit` matches `old` against the file byte for byte. Trimming the value would
// make every recovered edit miss.
func TestIndentationInsideAValueIsKept(t *testing.T) {
	resp := recoverLeakedCalls(Response{Reasoning: leakedEdit}, testTools)

	a := args(t, resp.ToolCalls[0])
	want := "    // Stage 5: apply quiet hours\n" +
		"    let after_quiet = apply_quiet_hours(intent, &actions, settings, now_local.naive_local(), tz);"
	if a["old"] != want {
		t.Fatalf("old:\n%q\nwant:\n%q", a["old"], want)
	}
}

// Only the single newline the syntax adds comes off; a blank line at the end of
// a value is content.
func TestOnlyOneNewlineIsRemovedFromEachEnd(t *testing.T) {
	leak := "<tool_call>\n<function=edit>\n<parameter=old>\n\nspaced\n\n</parameter>\n</function>\n</tool_call>"
	resp := recoverLeakedCalls(Response{Reasoning: leak}, testTools)

	if got := args(t, resp.ToolCalls[0])["old"]; got != "\nspaced\n" {
		t.Fatalf("old: %q, want %q", got, "\nspaced\n")
	}
}

// A model that called a tool properly and also wrote about one in its reasoning
// must not have the description executed as well.
func TestAProperCallIsNeverSecondGuessed(t *testing.T) {
	real := []ToolCall{{ID: "c1", Name: "glob", Arguments: json.RawMessage(`{}`)}}
	resp := recoverLeakedCalls(Response{
		Reasoning: "I could have run " + leakedBash,
		ToolCalls: real,
	}, testTools)

	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "c1" {
		t.Fatalf("calls: %+v", resp.ToolCalls)
	}
	if resp.Recovered != 0 {
		t.Fatalf("recovered: %d", resp.Recovered)
	}
	if !strings.Contains(resp.Reasoning, "<tool_call>") {
		t.Fatal("reasoning must be left alone when nothing was recovered")
	}
}

func TestOrdinaryThinkingIsUntouched(t *testing.T) {
	const thought = "The merge cannot unify those two router types."
	resp := recoverLeakedCalls(Response{Reasoning: thought, FinishReason: "stop"}, testTools)

	if len(resp.ToolCalls) != 0 || resp.Recovered != 0 {
		t.Fatalf("invented a call from prose: %+v", resp.ToolCalls)
	}
	if resp.Reasoning != thought || resp.FinishReason != "stop" {
		t.Fatalf("response was altered: %+v", resp)
	}
}

func TestSeveralCallsInOneThoughtAreAllRecovered(t *testing.T) {
	resp := recoverLeakedCalls(Response{
		Reasoning: leakedRead + "\n\nthen\n\n" + leakedBash,
	}, testTools)

	if len(resp.ToolCalls) != 2 || resp.Recovered != 2 {
		t.Fatalf("calls: %+v", resp.ToolCalls)
	}
	if resp.ToolCalls[0].Name != "read" || resp.ToolCalls[1].Name != "bash" {
		t.Fatalf("order must follow the text: %s, %s",
			resp.ToolCalls[0].Name, resp.ToolCalls[1].Name)
	}
	if resp.ToolCalls[0].ID == resp.ToolCalls[1].ID {
		t.Fatal("two calls in one turn need distinct ids")
	}
}

// An unknown tool is still rebuilt: the agent answers "unknown tool" and the
// model can correct itself, which beats stopping with no explanation.
func TestAnUnknownToolIsStillRebuilt(t *testing.T) {
	leak := "<tool_call>\n<function=teleport>\n<parameter=where>\nhome\n</parameter>\n</function>\n</tool_call>"
	resp := recoverLeakedCalls(Response{Reasoning: leak}, testTools)

	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "teleport" {
		t.Fatalf("calls: %+v", resp.ToolCalls)
	}
	if got := args(t, resp.ToolCalls[0])["where"]; got != "home" {
		t.Fatalf("where: %#v", got)
	}
}

// A stream cut off mid-call leaves no closing tag. There is nothing safe to
// reconstruct, so the response is left exactly as it arrived.
func TestAnUnterminatedBlockIsNotGuessedAt(t *testing.T) {
	leak := "thinking...\n<tool_call>\n<function=bash>\n<parameter=command>\nrm -rf"
	resp := recoverLeakedCalls(Response{Reasoning: leak}, testTools)

	if len(resp.ToolCalls) != 0 {
		t.Fatalf("half a call must not be run: %+v", resp.ToolCalls)
	}
	if resp.Reasoning != leak {
		t.Fatalf("reasoning: %q", resp.Reasoning)
	}
}

// A declared integer that is not one is handed over as text, so the tool can
// refuse it and say why. Inventing a number would be worse.
func TestAValueThatDoesNotMatchItsTypeIsPassedAsText(t *testing.T) {
	leak := "<tool_call>\n<function=read>\n<parameter=path>\nx.txt\n</parameter>\n" +
		"<parameter=limit>\nall of it\n</parameter>\n</function>\n</tool_call>"
	resp := recoverLeakedCalls(Response{Reasoning: leak}, testTools)

	if got := args(t, resp.ToolCalls[0])["limit"]; got != "all of it" {
		t.Fatalf("limit: %#v", got)
	}
}

// Nothing to recover from, and nothing to do.
func TestNoReasoningIsNotTouched(t *testing.T) {
	resp := recoverLeakedCalls(Response{Content: "done", FinishReason: "stop"}, testTools)
	if resp.Recovered != 0 || resp.FinishReason != "stop" {
		t.Fatalf("response: %+v", resp)
	}
}
