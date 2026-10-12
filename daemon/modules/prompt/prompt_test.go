package prompt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/corporealshift/rho/daemon/module"
	"github.com/corporealshift/rho/protocol"
)

type fakeSession struct{ log []protocol.Event }

func (f fakeSession) ID() string                               { return "s" }
func (f fakeSession) Workspace() module.Workspace              { return module.Workspace{Path: "/w"} }
func (f fakeSession) State() protocol.State                    { return protocol.State{} }
func (f fakeSession) Events(*string) ([]protocol.Event, error) { return f.log, nil }
func (f fakeSession) Append(protocol.EventType, any) (protocol.Event, error) {
	return protocol.Event{}, nil
}

func message(role, content string) protocol.Event {
	data, _ := json.Marshal(protocol.MessageData{Role: role, Content: content})
	return protocol.Event{Type: protocol.EventMessage, Data: data}
}

func toolResult(content string) protocol.Event {
	data, _ := json.Marshal(protocol.ToolResultData{Tool: "read", Status: "ok", Content: content})
	return protocol.Event{Type: protocol.EventToolResult, Data: data}
}

func TestAfterCompaction(t *testing.T) {
	step := "Write the plan to plan.md. Do only this planning step."
	long := "x" + strings.Repeat("é", promptMax) // after the x, promptMax falls inside an é
	for _, tc := range []struct {
		name    string
		log     []protocol.Event
		want    string // in the block; "" means no block
		notWant string
	}{
		{name: "the step's prompt, word for word",
			log:  []protocol.Event{message("user", step), message("assistant", "Implementing the plan now."), toolResult("ok")},
			want: step},
		{name: "the latest prompt, not an earlier one",
			log:     []protocol.Event{message("user", "first request"), message("assistant", "done"), message("user", "second request")},
			want:    "second request",
			notWant: "first request"},
		{name: "no prompt, no block",
			log: []protocol.Event{message("assistant", "hello"), toolResult("ok")}},
		{name: "a long prompt is cut on a rune boundary and says so",
			log:  []protocol.Event{message("user", long)},
			want: "more bytes of the prompt are left out"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Module{}
			if err := m.Init(nil, module.Config{}); err != nil {
				t.Fatal(err)
			}
			blocks, err := m.AfterCompaction(context.Background(), fakeSession{tc.log})
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(blocks) != 0 {
					t.Fatalf("want no block, got %+v", blocks)
				}
				return
			}
			if len(blocks) != 1 || blocks[0].Slot != "prefix" {
				t.Fatalf("want one prefix block, got %+v", blocks)
			}
			got := blocks[0].Content
			if !strings.HasPrefix(got, header) || !strings.Contains(got, tc.want) {
				t.Errorf("block %q does not hold %q under the header", got, tc.want)
			}
			if tc.notWant != "" && strings.Contains(got, tc.notWant) {
				t.Errorf("block %q holds %q", got, tc.notWant)
			}
			if len(got) > len(header)+promptMax+80 {
				t.Errorf("block is %d bytes", len(got))
			}
			if !utf8.ValidString(got) {
				t.Errorf("block is not valid UTF-8")
			}
		})
	}
}

func TestDisabledKeepsNothing(t *testing.T) {
	m := &Module{}
	if err := m.Init(nil, module.Config{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	blocks, _ := m.AfterCompaction(context.Background(), fakeSession{[]protocol.Event{message("user", "x")}})
	if len(blocks) != 0 {
		t.Fatalf("a disabled module should keep nothing, got %+v", blocks)
	}
}
