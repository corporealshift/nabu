package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// withTasks gives the fake session a task list.
func withTasks(s *fakeSession, tasks ...protocol.Task) *fakeSession {
	s.state.Tasks = tasks
	return s
}

func TestOpenTasksWriteTheInProgressMemory(t *testing.T) {
	m, h := curatorModule(t, `[]`, nil)
	s := withTasks(newSession(t, "farthing-947553cd"),
		protocol.Task{ID: "t1", Title: "port the importer", Status: protocol.TaskInProgress},
		protocol.Task{ID: "t2", Title: "add the CSV parser", Status: protocol.TaskPending},
		protocol.Task{ID: "t3", Title: "ship it", Status: protocol.TaskBlocked, Note: "waiting on the schema"},
		protocol.Task{ID: "t4", Title: "already done", Status: protocol.TaskDone},
	)

	m.recordProgress(context.Background(), s)

	if len(h.tools.calls) != 1 || !strings.HasPrefix(h.tools.calls[0], "memory.save ") {
		t.Fatalf("the in-progress memory did not go through the tool API: %+v", h.tools.calls)
	}
	mems, err := m.WorkspaceStore(s).Load()
	if err != nil || len(mems) != 1 {
		t.Fatalf("Load: %v %+v", err, mems)
	}
	got := mems[0]
	if got.Name != "farthing-947553cd-in-progress" {
		t.Errorf("name = %q, want the workspace key plus -in-progress", got.Name)
	}
	if got.Type != "project" {
		t.Errorf("type = %q, want project", got.Type)
	}
	if got.Scope != ScopeWorkspace {
		t.Errorf("scope = %q, want workspace", got.Scope)
	}
	for _, want := range []string{"port the importer", "add the CSV parser", "ship it"} {
		if !strings.Contains(got.Body, want) {
			t.Errorf("body is missing %q:\n%s", want, got.Body)
		}
	}
	// A blocked task's note is the reason it stopped, which is the thing the
	// next session most needs.
	if !strings.Contains(got.Body, "waiting on the schema") {
		t.Errorf("a blocked task's note did not reach the body:\n%s", got.Body)
	}
	if strings.Contains(got.Body, "already done") {
		t.Errorf("a finished task is not outstanding work:\n%s", got.Body)
	}
}

func TestNoOpenTasksForgetsAStaleInProgressMemory(t *testing.T) {
	m, h := curatorModule(t, `[]`, nil)
	s := withTasks(newSession(t, "repo-abc123"),
		protocol.Task{ID: "t1", Title: "something", Status: protocol.TaskInProgress})
	m.recordProgress(context.Background(), s)
	if n := len(m.mustLoad(t, s)); n != 1 {
		t.Fatalf("setup: %d memories", n)
	}

	// The next session finishes the work.
	done := withTasks(newSession(t, "repo-abc123"),
		protocol.Task{ID: "t1", Title: "something", Status: protocol.TaskDone})
	h.tools.calls = nil
	m.recordProgress(context.Background(), done)

	if len(m.mustLoad(t, done)) != 0 {
		t.Error("a stale in-progress memory survived: it tells the next session to resume finished work")
	}
	if len(h.tools.calls) != 1 || !strings.HasPrefix(h.tools.calls[0], "memory.forget ") {
		t.Errorf("the deletion did not go through the tool API: %+v", h.tools.calls)
	}
}

func TestNoOpenTasksAndNoMemoryDoesNothing(t *testing.T) {
	m, h := curatorModule(t, `[]`, nil)
	s := withTasks(newSession(t, "repo-abc123"),
		protocol.Task{ID: "t1", Title: "done", Status: protocol.TaskDone})

	m.recordProgress(context.Background(), s)

	if len(h.tools.calls) != 0 {
		t.Errorf("nothing to do, yet it called %+v", h.tools.calls)
	}
}

func TestTwoRepositoriesDoNotShareAnInProgressMemory(t *testing.T) {
	m, _ := curatorModule(t, `[]`, nil)
	one := withTasks(newSession(t, "repo-aaaaaaaa"),
		protocol.Task{ID: "t1", Title: "one's work", Status: protocol.TaskInProgress})
	two := newSession(t, "repo-bbbbbbbb")

	m.recordProgress(context.Background(), one)

	if n := len(m.mustLoad(t, two)); n != 0 {
		t.Errorf("repo two sees repo one's in-progress memory: %d memories", n)
	}
}

func TestSessionEndRecordsProgress(t *testing.T) {
	m, _ := curatorModule(t, `[]`, nil)
	s := withTasks(newSession(t, "repo-abc123"),
		protocol.Task{ID: "t1", Title: "outstanding", Status: protocol.TaskPending})
	s.events = []protocol.Event{userMsg("01A", "do some work")}

	m.SessionEnd(context.Background(), s)

	mems := m.mustLoad(t, s)
	if len(mems) != 1 || !strings.HasSuffix(mems[0].Name, "-in-progress") {
		t.Fatalf("SessionEnd did not record progress: %+v", mems)
	}
}

func TestProgressIsSkippedWhenTheCuratorIsOff(t *testing.T) {
	m, h := curatorModule(t, `[]`, module.Config{"curator": false})
	s := withTasks(newSession(t, "repo-abc123"),
		protocol.Task{ID: "t1", Title: "outstanding", Status: protocol.TaskPending})

	m.recordProgress(context.Background(), s)

	if len(h.tools.calls) != 0 {
		t.Errorf("a disabled curator wrote %+v", h.tools.calls)
	}
}

// The breezeway session resumed "Phase 9: tests" from this memory, never put
// it on a task list, gave up on half the tests, and ended. No tasks read as
// nothing open, and the only record of what was left went with it.
func TestASessionWithNoTasksLeavesTheInProgressMemoryAlone(t *testing.T) {
	m, h := curatorModule(t, `[]`, nil)
	earlier := withTasks(newSession(t, "breezeway-bdbc0f9b"),
		protocol.Task{ID: "t9", Title: "Phase 9: tests", Status: protocol.TaskInProgress,
			DoneWhen: "all JVM tests pass (outbox collapse, concurrent 401s)"})
	m.recordProgress(context.Background(), earlier)

	later := newSession(t, "breezeway-bdbc0f9b") // no task list at all
	h.tools.calls = nil
	m.recordProgress(context.Background(), later)

	if len(h.tools.calls) != 0 {
		t.Errorf("a session with no tasks touched the in-progress memory: %+v", h.tools.calls)
	}
	mems := m.mustLoad(t, later)
	if len(mems) != 1 || !strings.Contains(mems[0].Body, "concurrent 401s") {
		t.Fatalf("the unfinished work was lost: %+v", mems)
	}
}

// The next session is told to put the work back on its task list, so the stop
// gate holds it to the done-when rather than to its own opinion.
func TestTheInProgressMemorySaysToResumeAsTasks(t *testing.T) {
	m, _ := curatorModule(t, `[]`, nil)
	s := withTasks(newSession(t, "repo-abc123"),
		protocol.Task{ID: "t1", Title: "outstanding", Status: protocol.TaskPending})
	m.recordProgress(context.Background(), s)
	mems := m.mustLoad(t, s)
	if len(mems) != 1 || !strings.Contains(mems[0].Body, "task.update") {
		t.Fatalf("the memory does not say how to resume: %+v", mems)
	}
}

// The curator read a transcript in which the model declared "Phase 9 is
// COMPLETE" and wrote that over the in-progress memory. That memory comes from
// the task list only.
func TestTheCuratorCannotWriteTheInProgressMemory(t *testing.T) {
	reply := `[{"scope":"workspace","name":"breezeway-bdbc0f9b-in-progress","type":"project",
	  "description":"where work left off","body":"Phase 9 (tests) is COMPLETE."},
	 {"scope":"workspace","name":"gradle-wrapper","type":"project",
	  "description":"run gradle through gradlew.sh","body":"Use ./gradlew.sh, not gradle."}]`
	m, _ := curatorModule(t, reply, nil)
	s := newSession(t, "breezeway-bdbc0f9b")
	s.events = []protocol.Event{userMsg("01A", "can you finish the work here")}

	m.curate(context.Background(), s)

	for _, mem := range m.mustLoad(t, s) {
		if strings.HasSuffix(mem.Name, "-in-progress") {
			t.Errorf("the curator wrote the in-progress memory: %q", mem.Body)
		}
	}
	if len(m.mustLoad(t, s)) != 1 {
		t.Errorf("the curator's other proposals should still land: %+v", m.mustLoad(t, s))
	}
}
