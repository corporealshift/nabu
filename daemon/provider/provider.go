package provider

import (
	"context"
	"encoding/json"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

// Message is one chat message in provider-neutral form.
type Message struct {
	Role       string // "system" | "user" | "assistant" | "tool"
	Content    string
	ToolCalls  []ToolCall // assistant messages only
	ToolCallID string     // tool messages only
}

// ToolCall is a model-requested tool invocation. Arguments is always a JSON
// object; malformed model output is wrapped as {"_malformed": "<raw>"} so the
// tool fails visibly instead of the loop crashing.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// ToolSpec describes a tool to the model.
type ToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema object
}

// Request is one model round trip.
type Request struct {
	Model     string
	Messages  []Message
	Tools     []ToolSpec
	MaxTokens int // 0 = provider default
}

// Response is the completed assistant turn.
type Response struct {
	Content string
	// Reasoning is what the model thought before answering, when the
	// provider reports any. It is shown to the reader and never sent back.
	Reasoning    string
	ToolCalls    []ToolCall
	Usage        protocol.Usage
	FinishReason string // "stop" | "tool_calls" | "length" | provider-specific
	// Recovered counts tool calls parsed back out of Reasoning because the
	// server reported them as thinking rather than as calls. Zero is the
	// normal case. It is non-zero only when something upstream is misreading
	// the model, which is worth saying out loud rather than silently fixing.
	Recovered int
}

// Provider streams one completion. onDelta and onThinking (either may be nil)
// receive the answer and the reasoning as they arrive, separately, because a
// reader showing thinking live must not splice it into the answer. The
// returned Response holds both in full. With an error, it holds whatever had
// arrived before the failure, which may be nothing.
type Provider interface {
	Complete(ctx context.Context, req Request, onDelta, onThinking func(string)) (Response, error)
}

// Config describes one configured provider (spec §4).
type Config struct {
	Name          string
	BaseURL       string // e.g. http://localhost:8033/v1
	APIKey        string
	MaxInFlight   int // 0 = 1
	ContextWindow int // tokens; 0 = unknown (size-based compaction off)
	// TasksEnabled offers the task tools to this provider's model. Spec 8
	// makes it per provider because frontier models spend about a quarter more
	// tokens on them. nil means on.
	TasksEnabled *bool
	Timeout      time.Duration // per attempt; 0 = 10 minutes
	Retries      int           // retries after the first attempt; 0 = 2
	// MaxTokens caps one reply. 0 means DefaultMaxTokens. Left uncapped, a
	// local model that falls into a loop writes until the timeout kills the
	// call, and the whole turn is lost with it.
	MaxTokens int
}

// DefaultMaxTokens is the reply cap when a provider sets none. The longest
// reply in the logs that finished on its own was 13,366 tokens, a large file
// write; the one that prompted the cap was past 20,000 and still going.
const DefaultMaxTokens = 16384

func (c Config) withDefaults() Config {
	if c.MaxInFlight < 1 {
		c.MaxInFlight = 1
	}
	if c.Timeout == 0 {
		c.Timeout = 10 * time.Minute
	}
	if c.Retries == 0 {
		c.Retries = 2
	}
	if c.MaxTokens == 0 {
		c.MaxTokens = DefaultMaxTokens
	}
	return c
}
