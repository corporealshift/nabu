package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/protocol"
)

// mklog builds a chained, valid log from (type, data) pairs.
func mklog(t *testing.T, items ...any) []protocol.Event {
	t.Helper()
	var log []protocol.Event
	var last *string
	for i := 0; i+1 < len(items); i += 2 {
		typ := items[i].(protocol.EventType)
		b, _ := json.Marshal(items[i+1])
		id := protocol.NewULID()
		if last != nil {
			id = protocol.NewULIDAfter(*last)
		}
		e := protocol.Event{ID: id, ParentID: last, Type: typ, Data: b}
		log = append(log, e)
		cur := e.ID
		last = &cur
	}
	if err := protocol.ValidateLog(log); err != nil {
		t.Fatal(err)
	}
	return log
}

var sess = protocol.SessionData{Workspace: "C:/w", WorkspaceKey: "w-1",
	Options: protocol.Options{Model: "m", CompactionEnabled: true, PermissionMode: protocol.PermissionAsk}}

func TestBuildRequestFoldsToolCallsAndContexts(t *testing.T) {
	log := mklog(t,
		protocol.EventSession, sess,
		protocol.EventContext, protocol.ContextData{Source: "module:skills", Slot: "prefix", Content: "# Skills\n- x"},
		protocol.EventMessage, protocol.MessageData{Role: "user", Content: "list files"},
		protocol.EventMessage, protocol.MessageData{Role: "assistant", Content: ""},
		protocol.EventToolCall, protocol.ToolCallData{CallID: "c1", Tool: "bash", Arguments: json.RawMessage(`{"command":"ls"}`), Source: "model"},
		protocol.EventToolResult, protocol.ToolResultData{CallID: "c1", Tool: "bash", Content: "a.go", Status: "ok"},
		protocol.EventMessage, protocol.MessageData{Role: "assistant", Content: "One file."},
		protocol.EventContext, protocol.ContextData{Source: "module:memory", Slot: "suffix", Content: "maybe relevant: foo"},
		protocol.EventStopVeto, protocol.StopVetoData{Module: "verify", Reason: "tree dirty"},
	)
	req := buildRequest(log, "SYS", "qwen", []provider.ToolSpec{{Name: "bash"}}, 0)
	if req.Model != "qwen" || len(req.Tools) != 1 {
		t.Fatalf("model/tools: %+v", req)
	}
	m := req.Messages
	if m[0].Role != "system" || !strings.HasPrefix(m[0].Content, "SYS") || !strings.Contains(m[0].Content, "# Skills") {
		t.Fatalf("system: %+v", m[0])
	}
	if m[1].Role != "user" || m[1].Content != "list files" {
		t.Fatalf("m1: %+v", m[1])
	}
	if m[2].Role != "assistant" || len(m[2].ToolCalls) != 1 || m[2].ToolCalls[0].ID != "c1" || m[2].ToolCalls[0].Name != "bash" {
		t.Fatalf("m2 must fold the tool call: %+v", m[2])
	}
	if m[3].Role != "tool" || m[3].ToolCallID != "c1" || m[3].Content != "a.go" {
		t.Fatalf("m3: %+v", m[3])
	}
	if m[4].Role != "assistant" || m[4].Content != "One file." {
		t.Fatalf("m4: %+v", m[4])
	}
	last := m[len(m)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "maybe relevant: foo") ||
		!strings.Contains(last.Content, "Before stopping") || !strings.Contains(last.Content, "(verify) tree dirty") {
		t.Fatalf("suffix+vetoes must be one trailing user message: %+v", last)
	}
	if len(m) != 6 {
		t.Fatalf("message count %d: %+v", len(m), m)
	}
}

func TestBuildRequestAfterSummarize(t *testing.T) {
	log := mklog(t,
		protocol.EventSession, sess,
		protocol.EventContext, protocol.ContextData{Source: "module:skills", Slot: "prefix", Content: "OLD PREFIX"},
		protocol.EventMessage, protocol.MessageData{Role: "user", Content: "old"},
		protocol.EventMessage, protocol.MessageData{Role: "assistant", Content: "old reply"},
		protocol.EventTasks, protocol.TasksData{Revision: 1, Source: "model", Tasks: []protocol.Task{
			{ID: "t1", Title: "Do it", Status: protocol.TaskInProgress, BlockedBy: []string{}}}},
		protocol.EventCompaction, protocol.CompactionData{Mode: protocol.CompactionSummarize,
			RangeStart: "01JEVENT000000000000000001", RangeEnd: "01JEVENT000000000000000002", Summary: "THE SUMMARY"},
		protocol.EventContext, protocol.ContextData{Source: "module:skills", Slot: "prefix", Content: "NEW PREFIX"},
		protocol.EventMessage, protocol.MessageData{Role: "user", Content: "continue"},
	)
	// Point the compaction range at this log's own ids.
	cd := protocol.CompactionData{Mode: protocol.CompactionSummarize,
		RangeStart: log[1].ID, RangeEnd: log[4].ID, Summary: "THE SUMMARY"}
	log[5].Data, _ = json.Marshal(cd)

	req := buildRequest(log, "SYS", "m", nil, 0)
	sys := req.Messages[0].Content
	if strings.Contains(sys, "OLD PREFIX") || !strings.Contains(sys, "NEW PREFIX") {
		t.Fatalf("prefix must be the post-compaction one: %q", sys)
	}
	if !strings.Contains(sys, "THE SUMMARY") || !strings.Contains(sys, "## Current state") || !strings.Contains(sys, "- [>] t1 Do it") {
		t.Fatalf("summary and state block missing: %q", sys)
	}
	if len(req.Messages) != 2 || req.Messages[1].Content != "continue" {
		t.Fatalf("only post-compaction messages: %+v", req.Messages)
	}
}
