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

// A call ended by the provider's timeout says which limit it hit and the key
// that raises it. One ended by the caller's own deadline is not blamed on it.
func TestOpenAITimeoutNamesTheLimitAndKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, `{"choices":[{"delta":{"reasoning_content":"thinking"}}]}`)
		<-r.Context().Done()
	}))
	defer srv.Close()

	p := NewOpenAI(Config{Name: "strata", BaseURL: srv.URL + "/v1", Timeout: 200 * time.Millisecond}, srv.Client())
	_, err := p.Complete(context.Background(), Request{Model: "m"}, nil, nil)
	if err == nil {
		t.Fatal("want a timeout error")
	}
	for _, want := range []string{"200ms provider timeout", "providers.strata.timeout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain %q", err, want)
		}
	}

	p = NewOpenAI(Config{Name: "strata", BaseURL: srv.URL + "/v1", Timeout: time.Minute}, srv.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err = p.Complete(ctx, Request{Model: "m"}, nil, nil)
	if err == nil || strings.Contains(err.Error(), "provider timeout") {
		t.Errorf("a caller's deadline should not be reported as the provider timeout: %v", err)
	}
}

func TestLimitIsWrittenAsSet(t *testing.T) {
	for d, want := range map[time.Duration]string{
		10 * time.Minute:       "10m",
		30 * time.Minute:       "30m",
		time.Hour:              "1h",
		90 * time.Second:       "1m30s",
		200 * time.Millisecond: "200ms",
	} {
		if got := limit(d); got != want {
			t.Errorf("limit(%v) = %q, want %q", d, got, want)
		}
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

// NoThinking reaches the chat template as enable_thinking false; without it
// the request carries no template arguments at all, for servers that know
// none.
func TestOpenAINoThinkingAsksTheTemplate(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		json.Unmarshal(b, &body)
		bodies = append(bodies, body)
		sse(w, `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
		sse(w, "[DONE]")
	}))
	defer srv.Close()
	p := NewOpenAI(Config{Name: "t", BaseURL: srv.URL + "/v1"}, srv.Client())
	for _, off := range []bool{true, false} {
		if _, err := p.Complete(context.Background(), Request{Model: "m", NoThinking: off}, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	kw, _ := bodies[0]["chat_template_kwargs"].(map[string]any)
	if kw == nil || kw["enable_thinking"] != false {
		t.Errorf("with NoThinking: chat_template_kwargs = %v", bodies[0]["chat_template_kwargs"])
	}
	if _, ok := bodies[1]["chat_template_kwargs"]; ok {
		t.Errorf("without NoThinking: chat_template_kwargs = %v", bodies[1]["chat_template_kwargs"])
	}
}

// An effort reaches the chat template as written; none turns thinking off;
// NoThinking wins, so a summary never thinks when it was told not to
// (docs/specs/2026-10-16-session-effort-design.md).
func TestToWireEffort(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  Request
		want map[string]any
	}{
		{"unset", Request{}, nil},
		{"none", Request{Effort: "none"}, map[string]any{"enable_thinking": false}},
		{"low", Request{Effort: "low"}, map[string]any{"reasoning_effort": "low"}},
		{"medium", Request{Effort: "medium"}, map[string]any{"reasoning_effort": "medium"}},
		{"high", Request{Effort: "high"}, map[string]any{"reasoning_effort": "high"}},
		{"xhigh", Request{Effort: "xhigh"}, map[string]any{"reasoning_effort": "xhigh"}},
		{"max", Request{Effort: "max"}, map[string]any{"reasoning_effort": "max"}},
		{"no thinking wins", Request{Effort: "high", NoThinking: true}, map[string]any{"enable_thinking": false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := toWire(tc.req).ChatTemplateKwargs
			if len(got) != len(tc.want) {
				t.Fatalf("chat_template_kwargs = %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("chat_template_kwargs = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// An assistant turn's thinking goes out as reasoning_content, and a turn with
// none sends no field (docs/specs/2026-10-17-thinking-goes-back-design.md).
func TestToWireReasoningContent(t *testing.T) {
	body, err := json.Marshal(toWire(Request{Messages: []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello", Reasoning: "Greet back."},
		{Role: "assistant", Content: "again"},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(body, &w); err != nil {
		t.Fatal(err)
	}
	if got := w.Messages[1]["reasoning_content"]; got != "Greet back." {
		t.Errorf("reasoning_content = %v", got)
	}
	for _, i := range []int{0, 2} {
		if _, ok := w.Messages[i]["reasoning_content"]; ok {
			t.Errorf("message %d has reasoning_content: %s", i, body)
		}
	}
}
