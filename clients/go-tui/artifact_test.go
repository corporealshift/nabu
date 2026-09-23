package tui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/corporealshift/nabu/daemon/modules/artifact"
	"github.com/corporealshift/nabu/protocol"
)

func made(id, name, title, html string) protocol.Event {
	args, _ := json.Marshal(artifact.Args{Name: name, Title: title, HTML: html})
	return event(id, protocol.EventToolCall, protocol.ToolCallData{CallID: "c-" + id, Tool: artifact.Tool, Arguments: args})
}

// Issue 41: a page the agent made shows in the transcript, and o opens the
// latest version of it, sandboxed.
func TestOOpensTheNewestPageSandboxed(t *testing.T) {
	var opened string
	was := openInBrowser
	openInBrowser = func(path string) error { opened = path; return nil }
	defer func() { openInBrowser = was }()

	m := sized(t, nil)
	m, _ = send(m, eventMsg{ev: made("e1", "timings", "Test timings", "<p>first</p>")})
	m, _ = send(m, eventMsg{ev: made("e2", "timings", "Test timings", "<p>second</p>")})
	if !strings.Contains(stripANSI(m.body()), "made a page: Test timings") {
		t.Fatalf("the transcript should say a page was made:\n%s", stripANSI(m.body()))
	}

	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd == nil {
		t.Fatal("o should open the page")
	}
	if _, ok := cmd().(noteMsg); !ok || opened == "" {
		t.Fatalf("nothing was opened (%q)", opened)
	}
	page, err := os.ReadFile(opened)
	if err != nil {
		t.Fatal(err)
	}
	got := string(page)
	if !strings.Contains(got, "second") || strings.Contains(got, "first") {
		t.Errorf("o should open the latest version:\n%s", got)
	}
	if strings.Index(got, "Content-Security-Policy") > strings.Index(got, "<p>") {
		t.Errorf("the sandbox must come before the page:\n%s", got)
	}

	m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = typeIn(m, "/open nope")
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.body(), "no page called nope") {
		t.Error("an unknown page should be named as unknown")
	}
}
