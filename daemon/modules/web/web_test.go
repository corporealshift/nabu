package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
)

// The bodies below are the documented response shapes for each service. They
// are fixtures, not recordings: nothing here has met the live APIs.
const braveBody = `{
  "web": {"results": [
    {"title": "Go 1.26 release notes", "url": "https://go.dev/doc/go1.26",
     "description": "What changed in <strong>Go 1.26</strong>.", "age": "2 days ago"},
    {"title": "Download", "url": "https://go.dev/dl/", "description": "Binaries."}
  ]}
}`

const tavilyBody = `{
  "answer": "Go 1.26 added a new GC.",
  "results": [
    {"title": "Go 1.26 release notes", "url": "https://go.dev/doc/go1.26",
     "content": "The garbage collector was rewritten.", "score": 0.97},
    {"title": "Download", "url": "https://go.dev/dl/", "content": "Binaries.", "score": 0.4}
  ]
}`

func TestBraveParsesResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Subscription-Token"); got != "bk" {
			t.Errorf("token header = %q, want %q", got, "bk")
		}
		if got := r.URL.Query().Get("q"); got != "go 1.26" {
			t.Errorf("query = %q, want %q", got, "go 1.26")
		}
		_, _ = w.Write([]byte(braveBody))
	}))
	defer srv.Close()

	b := &brave{key: "bk", endpoint: srv.URL, client: srv.Client()}
	out, err := b.Search(context.Background(), "go 1.26", 2)
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if len(out.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(out.Results))
	}
	if out.Results[0].Title != "Go 1.26 release notes" {
		t.Errorf("title = %q", out.Results[0].Title)
	}
	// Brave marks the matched words; a model does not want the markup.
	if strings.Contains(out.Results[0].Snippet, "<strong>") {
		t.Errorf("snippet kept markup: %q", out.Results[0].Snippet)
	}
}

func TestTavilyParsesResultsAndAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tk" {
			t.Errorf("auth header = %q", got)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["query"] != "go 1.26" {
			t.Errorf("query = %v", body["query"])
		}
		_, _ = w.Write([]byte(tavilyBody))
	}))
	defer srv.Close()

	tv := &tavily{key: "tk", endpoint: srv.URL, client: srv.Client()}
	out, err := tv.Search(context.Background(), "go 1.26", 2)
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if out.Answer != "Go 1.26 added a new GC." {
		t.Errorf("answer = %q", out.Answer)
	}
	if len(out.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(out.Results))
	}
	// Tavily's value is the page text, not a link list.
	if out.Results[0].Snippet != "The garbage collector was rewritten." {
		t.Errorf("snippet = %q", out.Results[0].Snippet)
	}
}

func TestAnHTTPErrorIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer srv.Close()

	b := &brave{key: "bk", endpoint: srv.URL, client: srv.Client()}
	if _, err := b.Search(context.Background(), "q", 3); err == nil {
		t.Fatal("a 401 should be an error")
	} else if !strings.Contains(err.Error(), "401") {
		t.Errorf("the error should name the status, got %v", err)
	}
}

func TestProviderComesFromConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  module.Config
		want string
	}{
		{"brave when asked for", module.Config{"provider": "brave", "brave_api_key": "k"}, "brave"},
		{"tavily when asked for", module.Config{"provider": "tavily", "tavily_api_key": "k"}, "tavily"},
		{"the only configured key wins", module.Config{"tavily_api_key": "k"}, "tavily"},
		{"brave first when both are configured and neither is named",
			module.Config{"brave_api_key": "k", "tavily_api_key": "k"}, "brave"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Module{}
			if err := m.Init(nil, tt.cfg); err != nil {
				t.Fatalf("init: %v", err)
			}
			if got := m.providerName(); got != tt.want {
				t.Errorf("provider = %q, want %q", got, tt.want)
			}
		})
	}
}

// Without a key the module offers no tools at all, rather than a tool that
// fails the first time the model reaches for it.
func TestNoKeyNoTools(t *testing.T) {
	m := &Module{}
	if err := m.Init(nil, module.Config{}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if n := len(m.Tools()); n != 0 {
		t.Errorf("an unconfigured web module offered %d tools", n)
	}
}

func TestConfiguredModuleOffersSearchAndFetch(t *testing.T) {
	m := &Module{}
	if err := m.Init(nil, module.Config{"brave_api_key": "k"}); err != nil {
		t.Fatalf("init: %v", err)
	}

	var names []string
	for _, tool := range m.Tools() {
		names = append(names, tool.Name)
	}
	want := []string{"web.search", "web.fetch"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("tools = %v, want %v", names, want)
	}
}

// A disabled module is silent whatever else is configured.
func TestDisabledModuleOffersNothing(t *testing.T) {
	m := &Module{}
	if err := m.Init(nil, module.Config{"enabled": false, "brave_api_key": "k"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if n := len(m.Tools()); n != 0 {
		t.Errorf("a disabled web module offered %d tools", n)
	}
}
