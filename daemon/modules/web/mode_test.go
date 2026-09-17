package web

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
)

// stub records which service a call reached.
type stub struct {
	name string
	seen *string
}

func (s *stub) Name() string { return s.name }

func (s *stub) Search(_ context.Context, query string, _ int) (Results, error) {
	*s.seen = s.name
	return Results{Results: []Result{{Title: "t", URL: "u", Snippet: query}}}, nil
}

// bothConfigured returns a module wired to two recording stubs.
func bothConfigured(t *testing.T, provider string) (*Module, *string) {
	t.Helper()
	var seen string
	m := &Module{}
	cfg := module.Config{"brave_api_key": "b", "tavily_api_key": "t"}
	if provider != "" {
		cfg["provider"] = provider
	}
	if err := m.Init(nil, cfg); err != nil {
		t.Fatalf("init: %v", err)
	}
	m.services = map[string]Searcher{
		"brave":  &stub{name: "brave", seen: &seen},
		"tavily": &stub{name: "tavily", seen: &seen},
	}
	m.fallback = m.services[m.defaultService]
	return m, &seen
}

func search(t *testing.T, m *Module, args string) {
	t.Helper()
	if _, err := m.runSearch(context.Background(), nil, json.RawMessage(args)); err != nil {
		t.Fatalf("search: %v", err)
	}
}

// The model picks by what it wants back, not by brand.
func TestModeRoutesToTheRightService(t *testing.T) {
	tests := []struct {
		name string
		args string
		want string
	}{
		{"links go to the index", `{"query":"q","mode":"links"}`, "brave"},
		{"an answer goes to the reader", `{"query":"q","mode":"answer"}`, "tavily"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, seen := bothConfigured(t, "")
			search(t, m, tt.args)
			if *seen != tt.want {
				t.Errorf("mode %s reached %s, want %s", tt.args, *seen, tt.want)
			}
		})
	}
}

// Saying nothing means the configured default, so config still decides when
// the model has no opinion.
func TestNoModeUsesTheConfiguredDefault(t *testing.T) {
	for _, provider := range []string{"brave", "tavily"} {
		t.Run(provider, func(t *testing.T) {
			m, seen := bothConfigured(t, provider)
			search(t, m, `{"query":"q"}`)
			if *seen != provider {
				t.Errorf("default reached %s, want %s", *seen, provider)
			}
		})
	}
}

// One key configured: a mode the other service would have served still works.
func TestAModeWithoutItsServiceFallsBack(t *testing.T) {
	var seen string
	m := &Module{}
	if err := m.Init(nil, module.Config{"brave_api_key": "b"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	m.services = map[string]Searcher{"brave": &stub{name: "brave", seen: &seen}}
	m.fallback = m.services["brave"]

	search(t, m, `{"query":"q","mode":"answer"}`)

	if seen != "brave" {
		t.Errorf("reached %q, want the only service configured", seen)
	}
}

// A choice is only offered when there is one to make.
func TestModeIsInTheSchemaOnlyWhenBothExist(t *testing.T) {
	both := &Module{}
	if err := both.Init(nil, module.Config{"brave_api_key": "b", "tavily_api_key": "t"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(string(both.Tools()[0].Schema), `"mode"`) {
		t.Error("with two services the model should be offered the choice")
	}

	one := &Module{}
	if err := one.Init(nil, module.Config{"brave_api_key": "b"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if strings.Contains(string(one.Tools()[0].Schema), `"mode"`) {
		t.Error("with one service there is no choice to offer")
	}
}

// The description has to tell the model what the modes are for, or it will
// pick by the first word it recognises.
func TestTheDescriptionExplainsTheModes(t *testing.T) {
	m := &Module{}
	if err := m.Init(nil, module.Config{"brave_api_key": "b", "tavily_api_key": "t"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	desc := m.Tools()[0].Description

	for _, want := range []string{"links", "answer"} {
		if !strings.Contains(desc, want) {
			t.Errorf("the description never mentions %q: %s", want, desc)
		}
	}
	// The model should not be choosing a vendor.
	for _, unwanted := range []string{"Brave", "Tavily"} {
		if strings.Contains(desc, unwanted) {
			t.Errorf("the description names a service (%s): %s", unwanted, desc)
		}
	}
}

func TestAnUnknownModeIsNotAnError(t *testing.T) {
	m, seen := bothConfigured(t, "brave")
	search(t, m, `{"query":"q","mode":"whatever"}`)
	if *seen != "brave" {
		t.Errorf("an unknown mode should fall back to the default, reached %q", *seen)
	}
}
