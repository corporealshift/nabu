package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const tavilyEndpoint = "https://api.tavily.com/search"

// tavily is the Tavily API, which is built for agents: it returns cleaned page
// content rather than link lists, and often a direct answer, so a search costs
// fewer follow-up fetches than an index does.
type tavily struct {
	key      string
	endpoint string
	client   *http.Client
}

func (t *tavily) Name() string { return "tavily" }

func (t *tavily) Search(ctx context.Context, query string, max int) (Results, error) {
	endpoint := t.endpoint
	if endpoint == "" {
		endpoint = tavilyEndpoint
	}

	payload, err := json.Marshal(map[string]any{
		"query":          query,
		"max_results":    max,
		"search_depth":   "basic",
		"include_answer": true,
	})
	if err != nil {
		return Results{}, fmt.Errorf("tavily: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return Results{}, fmt.Errorf("tavily: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.key)

	resp, err := t.client.Do(req)
	if err != nil {
		return Results{}, fmt.Errorf("tavily: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return Results{}, fmt.Errorf("tavily: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Results{}, httpError("tavily", resp.StatusCode, body)
	}

	var wire struct {
		Answer  string `json:"answer"`
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return Results{}, fmt.Errorf("tavily: unreadable response: %w", err)
	}

	out := Results{Answer: wire.Answer}
	for _, r := range wire.Results {
		out.Results = append(out.Results, Result{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Content,
		})
	}
	return out, nil
}
