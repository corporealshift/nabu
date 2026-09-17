package bench

import (
	"testing"
	"time"
)

// The real chunk claude -p --output-format json printed on this machine, cut
// to the fields the benchmark reads.
const claudeJSON = `{"type":"result","subtype":"success","is_error":false,` +
	`"duration_ms":1011,"num_turns":3,"total_cost_usd":0.109611,` +
	`"usage":{"input_tokens":2,"output_tokens":4},"session_id":"abc"}`

func TestClaudeCostIsReadFromItsJSON(t *testing.T) {
	got := claudeCost("some progress\n"+claudeJSON, 99*time.Second)

	if got.Turns != 3 {
		t.Errorf("turns = %d, want 3", got.Turns)
	}
	if got.USD != 0.109611 {
		t.Errorf("usd = %v", got.USD)
	}
	if got.Duration != 1011*time.Millisecond {
		t.Errorf("duration = %v, want the reported one", got.Duration)
	}
	if got.OutputTokens != 4 {
		t.Errorf("output tokens = %d", got.OutputTokens)
	}
}

// Wall time is the one measurement always available; a harness that says
// nothing must not report a duration of zero.
func TestClaudeCostKeepsWallTimeWhenItSaysNothing(t *testing.T) {
	got := claudeCost("not json at all", 42*time.Second)

	if got.Duration != 42*time.Second {
		t.Errorf("duration = %v, want the measured fallback", got.Duration)
	}
	if got.Turns != 0 {
		t.Errorf("turns = %d, want 0 when unreported", got.Turns)
	}
}

// nabu's --json stream is one event per line. Assistant messages are turns.
func TestNabuUsageCountsAssistantTurns(t *testing.T) {
	stream := `{"type":"state_change","data":{"from":"idle","to":"running"}}
{"type":"message","data":{"role":"user","content":"go"}}
{"type":"thinking","data":{"content":"hmm","source":"model"}}
{"type":"message","data":{"role":"assistant","content":"one","usage":{"input_tokens":100,"output_tokens":10}}}
{"type":"tool_call","data":{"tool":"read"}}
{"type":"message","data":{"role":"assistant","content":"two","usage":{"input_tokens":150,"output_tokens":20}}}
`
	turns, in, out := nabuUsage(stream)

	if turns != 2 {
		t.Errorf("turns = %d, want 2 assistant messages", turns)
	}
	if in != 250 || out != 30 {
		t.Errorf("tokens = %d/%d, want 250/30", in, out)
	}
}

func TestNabuUsageIgnoresNoise(t *testing.T) {
	turns, in, out := nabuUsage("daemon starting\nnot json\n")
	if turns != 0 || in != 0 || out != 0 {
		t.Errorf("got %d %d %d, want zeros", turns, in, out)
	}
}

// pi's JSON shape is not pinned, so the reader accepts the plausible
// spellings and stays at zero rather than guessing.
func TestPiUsageAcceptsEitherSpelling(t *testing.T) {
	tests := []struct {
		name          string
		line          string
		turns, in, ou int
	}{
		{"openai spelling", `{"turns":4,"usage":{"prompt_tokens":10,"completion_tokens":5}}`, 4, 10, 5},
		{"plain spelling", `{"steps":2,"usage":{"input_tokens":7,"output_tokens":3}}`, 2, 7, 3},
		{"says nothing", `{"result":"done"}`, 0, 0, 0},
		{"not json", "just text", 0, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			turns, in, ou := piUsage(tt.line)
			if turns != tt.turns || in != tt.in || ou != tt.ou {
				t.Errorf("got %d %d %d, want %d %d %d", turns, in, ou, tt.turns, tt.in, tt.ou)
			}
		})
	}
}

func TestLastJSONObjectTakesTheLastOne(t *testing.T) {
	out := "noise\n{\"first\":1}\nmore noise\n{\"second\":2}\ntrailing\n"
	if got := lastJSONObject(out); got != `{"second":2}` {
		t.Errorf("got %q", got)
	}
}

func TestLastJSONObjectHandlesCRLF(t *testing.T) {
	if got := lastJSONObject("a\r\n{\"x\":1}\r\n"); got != `{"x":1}` {
		t.Errorf("got %q", got)
	}
}

// Preflight is where a harness refuses to run, rather than producing a result
// that looks like a score.
func TestPreflightRefusesWhatCannotBeMeasured(t *testing.T) {
	if err := (&Nabu{}).Preflight(t.Context()); err == nil {
		t.Error("nabu without an isolated root should refuse")
	}
	if err := (&Nabu{Root: "x"}).Preflight(t.Context()); err != nil {
		t.Errorf("nabu with a root should be fine: %v", err)
	}
	if err := (&Claude{}).Preflight(t.Context()); err == nil {
		t.Error("claude without a pinned model should refuse")
	}
	if err := (&Claude{Model: "sonnet"}).Preflight(t.Context()); err != nil {
		t.Errorf("claude with a pinned model should be fine: %v", err)
	}
}
