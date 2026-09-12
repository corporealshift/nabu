package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	}, func(s string) { deltas = append(deltas, s) })
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
	_, err := p.Complete(context.Background(), Request{Model: "m"}, nil)
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
	_, err := p.Complete(context.Background(), Request{Model: "m"}, nil)
	if err == nil || !strings.Contains(err.Error(), "context length exceeded") {
		t.Fatalf("err: %v", err)
	}
}
