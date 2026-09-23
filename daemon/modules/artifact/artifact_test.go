package artifact

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

type fakeSession struct{ log []protocol.Event }

func (f *fakeSession) ID() string                               { return "01ARZ3NDEKTSV4RRFFQ69G5FAV" }
func (f *fakeSession) Workspace() module.Workspace              { return module.Workspace{Path: "/w"} }
func (f *fakeSession) State() protocol.State                    { return protocol.State{} }
func (f *fakeSession) Events(*string) ([]protocol.Event, error) { return f.log, nil }
func (f *fakeSession) Append(protocol.EventType, any) (protocol.Event, error) {
	return protocol.Event{}, nil
}

// made logs a call the way the core does before running it.
func (f *fakeSession) made(a Args) json.RawMessage {
	raw, _ := json.Marshal(a)
	data, _ := json.Marshal(protocol.ToolCallData{CallID: "c", Tool: Tool, Arguments: raw})
	f.log = append(f.log, protocol.Event{Type: protocol.EventToolCall, Data: data})
	return raw
}

func enabled(t *testing.T) *Module {
	t.Helper()
	m := &Module{}
	if err := m.Init(nil, module.Config{}); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAPageIsMadeAndVersioned(t *testing.T) {
	m, s := enabled(t), &fakeSession{}
	page := Args{Name: "test-timings", Title: "Test timings", HTML: "<svg></svg>"}

	got, err := m.run(context.Background(), s, s.made(page))
	if err != nil || !strings.Contains(got, "version 1") {
		t.Fatalf("first: %q, %v", got, err)
	}
	got, err = m.run(context.Background(), s, s.made(page))
	if err != nil || !strings.Contains(got, "version 2") {
		t.Fatalf("second: %q, %v", got, err)
	}
}

func TestBadPagesAreRefused(t *testing.T) {
	m := enabled(t)
	for name, a := range map[string]Args{
		"a name with a slash": {Name: "../x", Title: "t", HTML: "<p>"},
		"an upper-case name":  {Name: "Timings", Title: "t", HTML: "<p>"},
		"no title":            {Name: "x", HTML: "<p>"},
		"no page":             {Name: "x", Title: "t", HTML: "  "},
		"too big":             {Name: "x", Title: "t", HTML: strings.Repeat("a", maxHTML+1)},
	} {
		raw, _ := json.Marshal(a)
		if _, err := m.run(context.Background(), &fakeSession{}, raw); err == nil {
			t.Errorf("%s: should be refused", name)
		}
	}
}

// The policy has to come before anything in the page that could run.
func TestWrapPutsThePolicyFirst(t *testing.T) {
	cases := map[string]string{
		"a whole document":         "<!DOCTYPE html><html><head><title>t</title></head><body>x</body></html>",
		"a fragment":               "<svg><circle r=\"4\"/></svg>",
		"a script before the html": "<script>new WebSocket('ws://127.0.0.1:8737')</script><html><body>x</body></html>",
		"a byte-order mark":        "\ufeff  <!doctype html><p>x</p>",
	}
	for name, page := range cases {
		got := Wrap(page)
		policy := strings.Index(got, "Content-Security-Policy")
		if policy < 0 {
			t.Errorf("%s: no policy:\n%s", name, got)
			continue
		}
		for _, later := range []string{"<script", "<html", "<head", "<svg", "<p>", "<body"} {
			if i := strings.Index(strings.ToLower(got), later); i >= 0 && i < policy {
				t.Errorf("%s: %s comes before the policy:\n%s", name, later, got)
			}
		}
		if !strings.HasPrefix(strings.ToLower(got), "<!doctype html") {
			t.Errorf("%s: should start with a doctype, got %q", name, got[:20])
		}
	}
	if !strings.Contains(Sandbox, "default-src 'none'") {
		t.Error("the sandbox must refuse everything it does not name, connections included")
	}
}
