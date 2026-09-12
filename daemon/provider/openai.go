package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

// OpenAI speaks the OpenAI chat-completions streaming API, which llama.cpp,
// Ollama, vLLM, OpenRouter, Groq, DeepSeek and Together all implement.
type OpenAI struct {
	cfg Config
	hc  *http.Client
	sem chan struct{}
}

// NewOpenAI builds a client. hc may be nil.
func NewOpenAI(cfg Config, hc *http.Client) *OpenAI {
	cfg = cfg.withDefaults()
	if hc == nil {
		hc = &http.Client{}
	}
	return &OpenAI{cfg: cfg, hc: hc, sem: make(chan struct{}, cfg.MaxInFlight)}
}

// Config returns the effective configuration.
func (p *OpenAI) Config() Config { return p.cfg }

// httpError is a non-2xx response.
type httpError struct {
	Status int
	Body   string
}

func (e *httpError) Error() string { return fmt.Sprintf("provider returned %d: %s", e.Status, e.Body) }

func (e *httpError) retryable() bool { return e.Status == 429 || e.Status >= 500 }

// Complete implements Provider: acquire a slot, then attempt with retries.
// A failure after the first delta was delivered is never retried, because the
// caller has already seen partial output.
func (p *OpenAI) Complete(ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	select {
	case p.sem <- struct{}{}:
		defer func() { <-p.sem }()
	case <-ctx.Done():
		return Response{}, ctx.Err()
	}
	attempts := p.cfg.Retries + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		started := false
		wrapped := func(s string) {
			started = true
			if onDelta != nil {
				onDelta(s)
			}
		}
		resp, err := p.once(ctx, req, wrapped)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if ctx.Err() != nil || started || !isRetryable(err) || attempt == attempts-1 {
			break
		}
		backoff := time.Duration(1<<attempt) * time.Second
		if backoff > 8*time.Second {
			backoff = 8 * time.Second
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
	}
	return Response{}, lastErr
}

func isRetryable(err error) bool {
	var he *httpError
	if errors.As(err, &he) {
		return he.retryable()
	}
	// Connection-level failures before any byte arrived.
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

// ---- wire types -----------------------------------------------------------

type wireToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type wireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type wireStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireRequest struct {
	Model         string            `json:"model"`
	Messages      []wireMessage     `json:"messages"`
	Tools         []wireTool        `json:"tools,omitempty"`
	Stream        bool              `json:"stream"`
	StreamOptions wireStreamOptions `json:"stream_options"`
	MaxTokens     int               `json:"max_tokens,omitempty"`
}

type wireChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func toWire(req Request) wireRequest {
	w := wireRequest{Model: req.Model, Stream: true, MaxTokens: req.MaxTokens}
	w.StreamOptions.IncludeUsage = true
	for _, m := range req.Messages {
		wm := wireMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			var wtc wireToolCall
			wtc.ID, wtc.Type = tc.ID, "function"
			wtc.Function.Name = tc.Name
			wtc.Function.Arguments = string(tc.Arguments)
			if wtc.Function.Arguments == "" {
				wtc.Function.Arguments = "{}"
			}
			wm.ToolCalls = append(wm.ToolCalls, wtc)
		}
		w.Messages = append(w.Messages, wm)
	}
	for _, t := range req.Tools {
		var wt wireTool
		wt.Type = "function"
		wt.Function.Name, wt.Function.Description = t.Name, t.Description
		wt.Function.Parameters = t.Parameters
		if len(wt.Function.Parameters) == 0 {
			wt.Function.Parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		w.Tools = append(w.Tools, wt)
	}
	return w
}

// once performs a single streaming attempt.
func (p *OpenAI) once(ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()
	body, err := json.Marshal(toWire(req))
	if err != nil {
		return Response{}, err
	}
	url := strings.TrimRight(p.cfg.BaseURL, "/") + "/chat/completions"
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "text/event-stream")
	if p.cfg.APIKey != "" {
		hreq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}
	hresp, err := p.hc.Do(hreq)
	if err != nil {
		return Response{}, fmt.Errorf("provider %s: %w", p.cfg.Name, err)
	}
	defer hresp.Body.Close()
	if hresp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(hresp.Body, 4096))
		return Response{}, &httpError{Status: hresp.StatusCode, Body: strings.TrimSpace(string(b))}
	}
	return readStream(hresp.Body, onDelta)
}

type partialCall struct {
	id, name string
	args     strings.Builder
}

// readStream parses SSE chunks into a Response.
func readStream(r io.Reader, onDelta func(string)) (Response, error) {
	var resp Response
	var content strings.Builder
	var calls []*partialCall
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	done := false
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			done = true
			break
		}
		var ch wireChunk
		if err := json.Unmarshal([]byte(payload), &ch); err != nil {
			return Response{}, fmt.Errorf("provider stream: bad chunk: %w", err)
		}
		if ch.Error != nil {
			return Response{}, fmt.Errorf("provider error: %s", ch.Error.Message)
		}
		if ch.Usage != nil {
			resp.Usage = protocol.Usage{InputTokens: ch.Usage.PromptTokens, OutputTokens: ch.Usage.CompletionTokens}
			if ch.Usage.PromptTokensDetails != nil {
				resp.Usage.CachedTokens = ch.Usage.PromptTokensDetails.CachedTokens
			}
		}
		for _, c := range ch.Choices {
			if c.Delta.Content != "" {
				content.WriteString(c.Delta.Content)
				onDelta(c.Delta.Content)
			}
			for _, tc := range c.Delta.ToolCalls {
				for len(calls) <= tc.Index {
					calls = append(calls, &partialCall{})
				}
				pc := calls[tc.Index]
				if tc.ID != "" {
					pc.id = tc.ID
				}
				if tc.Function.Name != "" {
					pc.name += tc.Function.Name
				}
				pc.args.WriteString(tc.Function.Arguments)
			}
			if c.FinishReason != nil && *c.FinishReason != "" {
				resp.FinishReason = *c.FinishReason
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Response{}, fmt.Errorf("provider stream: %w", err)
	}
	if !done && resp.FinishReason == "" && content.Len() == 0 && len(calls) == 0 {
		return Response{}, errors.New("provider stream ended without data")
	}
	resp.Content = content.String()
	for i, pc := range calls {
		id := pc.id
		if id == "" {
			id = fmt.Sprintf("call_%d", i+1)
		}
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: id, Name: pc.name, Arguments: normalizeArgs(pc.args.String())})
	}
	if resp.FinishReason == "" {
		if len(resp.ToolCalls) > 0 {
			resp.FinishReason = "tool_calls"
		} else {
			resp.FinishReason = "stop"
		}
	}
	return resp, nil
}

// normalizeArgs guarantees a JSON object.
func normalizeArgs(s string) json.RawMessage {
	s = strings.TrimSpace(s)
	if s == "" {
		return json.RawMessage(`{}`)
	}
	if json.Valid([]byte(s)) && strings.HasPrefix(s, "{") {
		return json.RawMessage(s)
	}
	b, _ := json.Marshal(map[string]string{"_malformed": s})
	return b
}
