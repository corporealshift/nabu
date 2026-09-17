package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// braveEndpoint is the web search endpoint. The field is overridable so tests
// can point it at a local server.
const braveEndpoint = "https://api.search.brave.com/res/v1/web/search"

// brave is the Brave Search API: an independent index, returning links and
// snippets for the model to follow with web.fetch.
type brave struct {
	key      string
	endpoint string
	client   *http.Client
}

func (b *brave) Name() string { return "brave" }

func (b *brave) Search(ctx context.Context, query string, max int) (Results, error) {
	endpoint := b.endpoint
	if endpoint == "" {
		endpoint = braveEndpoint
	}

	q := url.Values{}
	q.Set("q", query)
	q.Set("count", strconv.Itoa(max))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return Results{}, fmt.Errorf("brave: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", b.key)

	resp, err := b.client.Do(req)
	if err != nil {
		return Results{}, fmt.Errorf("brave: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Results{}, fmt.Errorf("brave: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Results{}, httpError("brave", resp.StatusCode, body)
	}

	var wire struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return Results{}, fmt.Errorf("brave: unreadable response: %w", err)
	}

	out := Results{}
	for _, r := range wire.Web.Results {
		out.Results = append(out.Results, Result{
			Title: textOf(r.Title),
			URL:   r.URL,
			// Brave wraps the matched words in <strong>, which is noise here.
			Snippet: textOf(r.Description),
		})
	}
	return out, nil
}
