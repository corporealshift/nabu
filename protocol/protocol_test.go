package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestULIDRoundTrip(t *testing.T) {
	at := time.Date(2026, 9, 11, 12, 34, 56, 789_000_000, time.UTC)
	id := NewULIDAt(at)
	if err := ValidateULID(id); err != nil {
		t.Fatalf("generated ULID invalid: %s", id)
	}
	got, err := ULIDTime(id)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(at) {
		t.Fatalf("time: want %v, got %v", at, got)
	}
	a, b := NewULIDAt(at), NewULIDAt(at.Add(time.Millisecond))
	if !(a < b) {
		t.Fatalf("ULIDs must sort by time: %s !< %s", a, b)
	}
}

func TestValidateULIDRejects(t *testing.T) {
	for _, bad := range []string{"", "01JEVENT00000000000000000", "81JEVENT000000000000000001", "01JEVENT0000000000000000I1", "01jevent000000000000000001"} {
		if ValidateULID(bad) == nil {
			t.Errorf("expected %q to be invalid", bad)
		}
	}
}

func TestDecodeDataIgnoresUnknownFields(t *testing.T) {
	e := Event{ID: NewULID(), Type: EventNotice, Data: json.RawMessage(`{"source":"daemon","level":"info","message":"hi","extra":{"future":true}}`)}
	if err := ValidateEvent(e); err != nil {
		t.Fatal(err)
	}
}

func TestValidateLogChain(t *testing.T) {
	sess := Event{ID: NewULID(), Type: EventSession, Timestamp: time.Now(),
		Data: json.RawMessage(`{"workspace":"/w","workspace_key":"w-1","options":{"model":"m","compaction_enabled":true,"permission_mode":"ask"}}`)}
	wrongParent := "01JEVENT000000000000000009"
	msg := Event{ID: NewULID(), ParentID: &wrongParent, Type: EventMessage, Timestamp: time.Now(),
		Data: json.RawMessage(`{"role":"user","content":"x"}`)}
	err := ValidateLog([]Event{sess, msg})
	if err == nil || !strings.Contains(err.Error(), "broken chain") {
		t.Fatalf("want broken chain error, got %v", err)
	}
	msg.ParentID = &sess.ID
	if err := ValidateLog([]Event{sess, msg}); err != nil {
		t.Fatal(err)
	}
}

func TestRPCErrorCarriesName(t *testing.T) {
	e := NewRPCError(CodeCursorUnknown, "nope")
	b, _ := json.Marshal(e)
	if !strings.Contains(string(b), `"name":"nabu_cursor_unknown"`) {
		t.Fatalf("missing name: %s", b)
	}
}

func TestRenderTasksMatchesStateBlock(t *testing.T) {
	tasks := []Task{
		{ID: "t1", Title: "A", Status: TaskDone, BlockedBy: []string{}},
		{ID: "t2", Title: "B", Status: TaskBlocked, BlockedBy: []string{}, Note: "waiting"},
	}
	want := "Tasks (1/2 done):\n- [x] t1 A\n- [!] t2 B — blocked: waiting"
	if got := RenderTasks(tasks); got != want {
		t.Fatalf("RenderTasks:\n%s\nwant:\n%s", got, want)
	}
	if got := RenderTasks(nil); got != "Tasks: none" {
		t.Fatalf("empty: %q", got)
	}
}
