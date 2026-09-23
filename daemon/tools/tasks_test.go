package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

func ev(t protocol.EventType, data any) protocol.Event {
	b, _ := json.Marshal(data)
	return protocol.Event{ID: protocol.NewULID(), Type: t, Data: b}
}

func TestMergeAssignsIdsAndRevision(t *testing.T) {
	out, err := Merge(protocol.TasksData{}, []protocol.Task{
		{Title: "First", Status: protocol.TaskPending},
		{ID: "t7", Title: "Named", Status: protocol.TaskInProgress},
		{Title: "Third", Status: protocol.TaskPending},
	}, nil, "model")
	if err != nil {
		t.Fatal(err)
	}
	if out.Revision != 1 || out.Source != "model" {
		t.Fatalf("meta: %+v", out)
	}
	if out.Tasks[0].ID != "t8" || out.Tasks[1].ID != "t7" || out.Tasks[2].ID != "t9" {
		t.Fatalf("ids: %s %s %s", out.Tasks[0].ID, out.Tasks[1].ID, out.Tasks[2].ID)
	}
	for _, task := range out.Tasks {
		if task.BlockedBy == nil {
			t.Fatal("blocked_by must be non-nil")
		}
	}
}

func TestMergeRejectsBadInput(t *testing.T) {
	if _, err := Merge(protocol.TasksData{}, []protocol.Task{{Title: "x", Status: "doing"}}, nil, "model"); err == nil || !strings.Contains(err.Error(), "status") {
		t.Fatalf("bad status: %v", err)
	}
	if _, err := Merge(protocol.TasksData{}, []protocol.Task{{ID: "a", Title: "x", Status: "pending"}, {ID: "a", Title: "y", Status: "pending"}}, nil, "model"); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("dup: %v", err)
	}
	if _, err := Merge(protocol.TasksData{}, []protocol.Task{{Status: "pending"}}, nil, "model"); err == nil || !strings.Contains(err.Error(), "title") {
		t.Fatalf("title: %v", err)
	}
}

func TestMergeCarriesFieldsAndSetsEvidence(t *testing.T) {
	prev := protocol.TasksData{Revision: 3, Tasks: []protocol.Task{
		{ID: "t1", Title: "A", Status: protocol.TaskInProgress, DoneWhen: "tests pass", Check: "go test ./...", BlockedBy: []string{}},
		{ID: "t2", Title: "B", Status: protocol.TaskDone, BlockedBy: []string{}, Evidence: "01JEVENT000000000000000005"},
	}}
	pass := ev(protocol.EventCheck, protocol.CheckData{Name: "task:t1", Kind: "command", TaskID: "t1", Status: "pass", Summary: "exit 0"})
	log := []protocol.Event{
		ev(protocol.EventCheck, protocol.CheckData{Name: "task:t1", Kind: "command", TaskID: "t1", Status: "fail", Summary: "exit 1"}),
		pass,
	}
	out, err := Merge(prev, []protocol.Task{
		{ID: "t1", Title: "A", Status: protocol.TaskDone, Evidence: "01JEVENT000000000000000099"}, // model-supplied evidence ignored
		{ID: "t2", Title: "B", Status: protocol.TaskDone},
		{ID: "t3", Title: "C", Status: protocol.TaskPending, DoneWhen: "x"},
	}, log, "model")
	if err != nil {
		t.Fatal(err)
	}
	if out.Revision != 4 {
		t.Fatalf("revision: %d", out.Revision)
	}
	t1 := out.Tasks[0]
	if t1.DoneWhen != "tests pass" || t1.Check != "go test ./..." {
		t.Fatalf("fields must carry over when omitted: %+v", t1)
	}
	if t1.Evidence != pass.ID {
		t.Fatalf("evidence must be the latest passing check: %q", t1.Evidence)
	}
	if out.Tasks[1].Evidence != "01JEVENT000000000000000005" {
		t.Fatal("already-done task keeps its evidence")
	}
	if out.Tasks[2].Evidence != "" {
		t.Fatal("pending task has no evidence")
	}
}

type recordingStore struct{ got protocol.TasksData }

func (r *recordingStore) UpdateTasks(_ context.Context, _ module.Session, incoming []protocol.Task, source string) (protocol.TasksData, error) {
	d, err := Merge(protocol.TasksData{}, incoming, nil, source)
	r.got = d
	return d, err
}

func TestTaskUpdateToolRendersList(t *testing.T) {
	store := &recordingStore{}
	b := &Builtins{Tasks: store}
	out, err := run(t, b, fakeSession{t.TempDir()}, "task.update",
		`{"tasks":[{"title":"Write test","status":"in_progress","done_when":"test fails"},{"title":"Make pass","status":"pending"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "Tasks (0/2 done):\n- [>] t1 Write test\n- [ ] t2 Make pass" {
		t.Fatalf("out: %q", out)
	}
	if store.got.Source != "model" {
		t.Fatalf("source: %q", store.got.Source)
	}
	if _, err := run(t, b, fakeSession{t.TempDir()}, "task.update", `{"tasks":[]}`); err == nil {
		t.Fatal("empty list must be rejected: use cancelled/done statuses instead")
	}
	if len((&Builtins{}).Tools()) != 7 {
		t.Fatal("without a TaskStore the task tool must not be offered")
	}
}
