package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sse writes one SSE data line.
func sse(w http.ResponseWriter, v string) {
	fmt.Fprintf(w, "data: %s\n\n", v)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func TestOpenAIStreamsContentToolCallsAndUsage(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("auth header: %q", r.Header.Get("Authorization"))
		}
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, `{"choices":[{"delta":{"role":"assistant","content":"Hel"}}]}`)
		sse(w, `{"choices":[{"delta":{"content":"lo"}}]}`)
		sse(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"bash","arguments":"{\"comm"}}]}}]}`)
		sse(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"and\":\"ls\"}"}}]}}]}`)
		sse(w, `{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_2","function":{"name":"read","arguments":"not json"}}]}}]}`)
		sse(w, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
		sse(w, `{"choices":[],"usage":{"prompt_tokens":120,"completion_tokens":9,"prompt_tokens_details":{"cached_tokens":100}}}`)
		sse(w, "[DONE]")
	}))
	defer srv.Close()

	p := NewOpenAI(Config{Name: "t", BaseURL: srv.URL + "/v1", APIKey: "sk-test"}, srv.Client())
	var deltas []string
	resp, err := p.Complete(context.Background(), Request{
		Model: "m",
		Messages: []Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "c0", Name: "bash", Arguments: []byte(`{"command":"pwd"}`)}}},
			{Role: "tool", ToolCallID: "c0", Content: "/w"},
		},
		Tools:     []ToolSpec{{Name: "bash", Description: "run", Parameters: []byte(`{"type":"object"}`)}},
		MaxTokens: 256,
	}, func(s string) { deltas = append(deltas, s) }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "Hello" || strings.Join(deltas, "|") != "Hel|lo" {
		t.Fatalf("content: %q deltas: %v", resp.Content, deltas)
	}
	if len(resp.ToolCalls) != 2 || resp.ToolCalls[0].ID != "call_1" || string(resp.ToolCalls[0].Arguments) != `{"command":"ls"}` {
		t.Fatalf("tool calls: %+v", resp.ToolCalls)
	}
	if !strings.Contains(string(resp.ToolCalls[1].Arguments), `"_malformed"`) {
		t.Fatalf("malformed args must be wrapped: %s", resp.ToolCalls[1].Arguments)
	}
	if resp.FinishReason != "tool_calls" || resp.Usage.InputTokens != 120 || resp.Usage.OutputTokens != 9 || resp.Usage.CachedTokens != 100 {
		t.Fatalf("finish/usage: %+v", resp)
	}
	if gotBody["stream"] != true || gotBody["max_tokens"].(float64) != 256 || gotBody["model"] != "m" {
		t.Fatalf("body: %v", gotBody)
	}
	msgs := gotBody["messages"].([]any)
	asst := msgs[2].(map[string]any)
	tc := asst["tool_calls"].([]any)[0].(map[string]any)
	if tc["type"] != "function" || tc["function"].(map[string]any)["arguments"] != `{"command":"pwd"}` {
		t.Fatalf("assistant tool_calls wire shape: %v", asst)
	}
	tool := msgs[3].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "c0" {
		t.Fatalf("tool message: %v", tool)
	}
	tools := gotBody["tools"].([]any)[0].(map[string]any)
	if tools["type"] != "function" || tools["function"].(map[string]any)["name"] != "bash" {
		t.Fatalf("tools: %v", tools)
	}
}

func TestOpenAINonStreamErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"message":"bad tool schema"}}`)
	}))
	defer srv.Close()
	p := NewOpenAI(Config{Name: "t", BaseURL: srv.URL + "/v1", Retries: -1}, srv.Client())
	_, err := p.Complete(context.Background(), Request{Model: "m"}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "bad tool schema") || !strings.Contains(err.Error(), "400") {
		t.Fatalf("err: %v", err)
	}
}

func TestOpenAIErrorEventMidStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, `{"choices":[{"delta":{"content":"par"}}]}`)
		sse(w, `{"error":{"message":"context length exceeded"}}`)
	}))
	defer srv.Close()
	p := NewOpenAI(Config{Name: "t", BaseURL: srv.URL + "/v1"}, srv.Client())
	_, err := p.Complete(context.Background(), Request{Model: "m"}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "context length exceeded") {
		t.Fatalf("err: %v", err)
	}
}

// The call that prompted this: a reply still streaming when the per-attempt
// timeout ends it. What had arrived comes back with the error, rather than
// being lost with the call.
func TestOpenAITimeoutMidStreamReturnsWhatArrived(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, `{"choices":[{"delta":{"reasoning_content":"let me write it"}}]}`)
		sse(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"write","arguments":"{\"path\":\"a.go\",\"content\":\"same"}}]}}]}`)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	p := NewOpenAI(Config{Name: "t", BaseURL: srv.URL + "/v1", Timeout: 200 * time.Millisecond}, srv.Client())
	resp, err := p.Complete(context.Background(), Request{Model: "m"}, nil, nil)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err: %v", err)
	}
	if resp.Reasoning != "let me write it" {
		t.Errorf("reasoning: %q", resp.Reasoning)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "write" ||
		!strings.Contains(string(resp.ToolCalls[0].Arguments), "_malformed") {
		t.Errorf("tool calls: %+v", resp.ToolCalls)
	}
}

// With no cap given, a request still carries one: the provider's default.
func TestOpenAIRequestsAreCappedByDefault(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		sse(w, `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
		sse(w, "[DONE]")
	}))
	defer srv.Close()
	p := NewOpenAI(Config{Name: "t", BaseURL: srv.URL + "/v1"}, srv.Client())
	if got := p.Config().MaxTokens; got != DefaultMaxTokens {
		t.Fatalf("default cap: %d", got)
	}
	if _, err := p.Complete(context.Background(), Request{Model: "m", MaxTokens: p.Config().MaxTokens}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if body["max_tokens"] != float64(DefaultMaxTokens) {
		t.Errorf("max_tokens on the wire: %v", body["max_tokens"])
	}
}
