package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/protocol"
)

var opts = protocol.Options{Model: "local/qwen", CompactionEnabled: true, PermissionMode: protocol.PermissionAsk}

func newStore(t *testing.T) *Store {
	t.Helper()
	return openAt(t, t.TempDir())
}

// openAt opens a store rooted at dir and closes it on cleanup. Closing matters
// on Windows: an open handle blocks TempDir removal.
func openAt(t *testing.T, dir string) *Store {
	t.Helper()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestCreateWritesSessionEvent(t *testing.T) {
	st := newStore(t)
	s, err := st.Create("C:/w", "w-1", opts)
	if err != nil {
		t.Fatal(err)
	}
	ev := s.Events()
	if len(ev) != 1 || ev[0].Type != protocol.EventSession || ev[0].ParentID != nil {
		t.Fatalf("events: %+v", ev)
	}
	b, err := os.ReadFile(filepath.Join(st.root, s.ID()+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), "\n") != 1 {
		t.Fatalf("file should hold one line: %q", b)
	}
	path, key := s.Workspace()
	if path != "C:/w" || key != "w-1" {
		t.Fatalf("workspace: %s %s", path, key)
	}
}

func TestAppendChainsAndPersists(t *testing.T) {
	st := newStore(t)
	s, _ := st.Create("C:/w", "w-1", opts)
	e1, err := s.Append(protocol.EventMessage, protocol.MessageData{Role: "user", Content: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	e2, _ := s.Append(protocol.EventStateChange, protocol.StateChangeData{To: protocol.StateRunning, Reason: "prompt"})
	if *e2.ParentID != e1.ID || !(e1.ID < e2.ID) {
		t.Fatalf("chain/order: %s -> %s", e1.ID, e2.ID)
	}
	st2 := openAt(t, filepath.Dir(st.root))
	r, err := st2.Get(s.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Events(); len(got) != 3 || got[2].ID != e2.ID {
		t.Fatalf("reloaded: %d events", len(got))
	}
	if r.State().State != protocol.StateRunning {
		t.Fatalf("state: %s", r.State().State)
	}
	e3, err := r.Append(protocol.EventNotice, protocol.NoticeData{Source: "daemon", Level: "info", Message: "x"})
	if err != nil || *e3.ParentID != e2.ID {
		t.Fatalf("append after reload: %v %+v", err, e3)
	}
}

func TestAppendRejectsInvalidAndWritesNothing(t *testing.T) {
	st := newStore(t)
	s, _ := st.Create("C:/w", "w-1", opts)
	_, err := s.Append(protocol.EventMessage, protocol.MessageData{Role: "system", Content: "no"})
	if err == nil || !strings.Contains(err.Error(), `role "system" invalid`) {
		t.Fatalf("want validation error, got %v", err)
	}
	if len(s.Events()) != 1 {
		t.Fatal("invalid event must not be appended")
	}
	b, _ := os.ReadFile(filepath.Join(st.root, s.ID()+".jsonl"))
	if strings.Count(string(b), "\n") != 1 {
		t.Fatal("invalid event must not be written")
	}
}

func TestEventsAfterUnknownCursor(t *testing.T) {
	st := newStore(t)
	s, _ := st.Create("C:/w", "w-1", opts)
	bad := "01JEVENT000000000000000099"
	_, _, err := s.EventsAfter(&bad)
	var rpc *protocol.RPCError
	if !errors.As(err, &rpc) || rpc.Code != protocol.CodeCursorUnknown {
		t.Fatalf("want cursor_unknown, got %v", err)
	}
	last := s.Events()[0].ID
	got, synced, err := s.EventsAfter(&last)
	if err != nil || len(got) != 0 || !synced {
		t.Fatalf("at last: %v %d %v", err, len(got), synced)
	}
}

func TestGetUnknownAndCorrupt(t *testing.T) {
	st := newStore(t)
	if _, err := st.Get("01JEVENT000000000000000001"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := st.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-ULID id must be not found, got %v", err)
	}
	s, _ := st.Create("C:/w", "w-1", opts)
	s.Append(protocol.EventMessage, protocol.MessageData{Role: "user", Content: "hi"})
	path := filepath.Join(st.root, s.ID()+".jsonl")
	b, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	corrupt := strings.Replace(lines[1], `"parent_id":"`+s.Events()[0].ID+`"`, `"parent_id":"01JEVENT000000000000000077"`, 1)
	os.WriteFile(path, []byte(lines[0]+"\n"+corrupt+"\n"), 0o644)
	st2 := openAt(t, filepath.Dir(st.root))
	if _, err := st2.Get(s.ID()); err == nil || !strings.Contains(err.Error(), "broken chain") {
		t.Fatalf("want broken chain, got %v", err)
	}
}
