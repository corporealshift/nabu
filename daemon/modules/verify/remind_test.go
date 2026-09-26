package verify

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// working is a session in a fresh repository whose turn, prompted by asked,
// wrote the given files. It starts before the files exist, so they are the
// session's own.
func working(t *testing.T, m *Module, asked string, files ...string) *fakeSession {
	t.Helper()
	s := turn(t, gitRepo(t), asked, protocol.ToolCallData{
		CallID: "w1", Tool: "write", Arguments: json.RawMessage(`{"path":"x","content":"x"}`), Source: "model"})
	m.SessionStart(context.Background(), s)
	for _, f := range files {
		writeAt(t, s.workspace, f)
	}
	return s
}

// stopAgain logs the veto a stop produced, as the agent does, then asks again.
func stopAgain(t *testing.T, m *Module, s *fakeSession, v module.StopVerdict, info module.StopInfo) module.StopVerdict {
	t.Helper()
	s.events = append(s.events, logged(t, "V", protocol.EventStopVeto, protocol.StopVetoData{Module: "verify", Reason: v.Reason}))
	return m.BeforeStop(context.Background(), s, info)
}

// 01M3DQZR: told "don't add extra things. just hello world is fine for now",
// the model met three vetoes in a row, lost the instruction, and started on a
// data layer. One reminder, carrying the instruction, and then the stop.
func TestOneReminderThenTheStop(t *testing.T) {
	m := newVerify(t, module.Config{})
	asked := "don't add extra things. just hello world is fine for now"
	s := working(t, m, asked, "android/app/src/main/MainActivity.kt", "android/app/build/out.bin")
	info := module.StopInfo{Tasks: []protocol.Task{
		{ID: "t5", Title: "Write AndroidManifest.xml and Activity stubs", Status: protocol.TaskInProgress},
		{ID: "t6", Title: "Verify: assembleDebug succeeds", Status: protocol.TaskPending},
		{ID: "t1", Title: "versions", Status: protocol.TaskDone},
	}}

	v := m.BeforeStop(context.Background(), s, info)
	if v.Allow {
		t.Fatal("work left uncommitted should get its one reminder")
	}
	for _, want := range []string{
		"not from the person", "you may stop after answering it",
		`"don't add extra things. just hello world is fine for now"`, "start nothing it did not ask for",
		"not committed: android/app/src/main/MainActivity.kt",
		"build output that git does not ignore: 1 file under android/app/build/",
		"t5 Write AndroidManifest.xml and Activity stubs (in_progress)", "t6 Verify: assembleDebug succeeds (pending)",
		"If the work they asked for is finished", "If it is not finished: do not push",
	} {
		if !strings.Contains(v.Reason, want) {
			t.Errorf("reminder is missing %q:\n%s", want, v.Reason)
		}
	}
	if strings.Contains(v.Reason, "t1 versions") {
		t.Errorf("a finished task is not open:\n%s", v.Reason)
	}

	// Once. Whatever the model says or does next, it may stop.
	if again := stopAgain(t, m, s, v, info); !again.Allow {
		t.Errorf("a second reminder in one turn: %q", again.Reason)
	}
}

func TestNoReminderWhenTheTurnChangedNothing(t *testing.T) {
	m := newVerify(t, module.Config{"command": "exit 1"})
	s := turn(t, gitRepo(t), "create a README")
	m.SessionStart(context.Background(), s)
	writeAt(t, s.workspace, "left-from-before.txt")
	info := module.StopInfo{Tasks: []protocol.Task{{ID: "t1", Title: "open", Status: protocol.TaskPending}}}

	if v := m.BeforeStop(context.Background(), s, info); !v.Allow {
		t.Errorf("a turn that changed nothing was reminded: %q", v.Reason)
	}
	if got := s.checkEvents(); len(got) != 0 {
		t.Errorf("the gate ran for a turn that changed nothing: %+v", got)
	}
}

func TestNoReminderWhenNothingIsOutstanding(t *testing.T) {
	m := newVerify(t, module.Config{})
	s := working(t, m, "add a feature branch file")
	run(t, s.workspace, "checkout", "-q", "-b", "feature")
	if v := m.BeforeStop(context.Background(), s, module.StopInfo{}); !v.Allow {
		t.Errorf("nothing outstanding, yet reminded: %q", v.Reason)
	}
}

func TestTheReminderNamesWorkOnMain(t *testing.T) {
	m := newVerify(t, module.Config{})
	s := working(t, m, "fix the typo")
	branch := currentBranch(t, s.workspace)
	if branch != "main" && branch != "master" {
		t.Skipf("the test repository starts on %q", branch)
	}

	writeAt(t, s.workspace, "fix.txt")
	v := m.BeforeStop(context.Background(), s, module.StopInfo{})
	if v.Allow || !strings.Contains(v.Reason, "you are on "+branch+": commit on a new branch") {
		t.Errorf("uncommitted work on %s should say so, got allow=%v:\n%s", branch, v.Allow, v.Reason)
	}

	s2 := working(t, m, "fix the typo")
	writeAt(t, s2.workspace, "fix.txt")
	run(t, s2.workspace, "add", "fix.txt")
	run(t, s2.workspace, "commit", "-q", "-m", "fix")
	v2 := m.BeforeStop(context.Background(), s2, module.StopInfo{})
	if v2.Allow || !strings.Contains(v2.Reason, "this session committed on "+branch) {
		t.Errorf("a commit on %s should say so, got allow=%v:\n%s", branch, v2.Allow, v2.Reason)
	}
}

// Each message from the person starts a turn, and a turn gets its own reminder.
func TestANewMessageIsANewTurn(t *testing.T) {
	m := newVerify(t, module.Config{})
	s := working(t, m, "first request", "a.txt")
	v := m.BeforeStop(context.Background(), s, module.StopInfo{})
	if v.Allow {
		t.Fatal("setup: expected a reminder")
	}
	s.events = append(s.events, logged(t, "V", protocol.EventStopVeto, protocol.StopVetoData{Module: "verify", Reason: v.Reason}))
	s.events = append(s.events,
		logged(t, "U2", protocol.EventMessage, protocol.MessageData{Role: "user", Content: "now the second thing"}),
		logged(t, "w2c", protocol.EventToolCall, protocol.ToolCallData{CallID: "w2", Tool: "edit", Arguments: json.RawMessage(`{"path":"a.txt"}`)}),
		logged(t, "w2r", protocol.EventToolResult, protocol.ToolResultData{CallID: "w2", Tool: "edit", Status: "ok"}))

	v2 := m.BeforeStop(context.Background(), s, module.StopInfo{})
	if v2.Allow || !strings.Contains(v2.Reason, `"now the second thing"`) {
		t.Errorf("a new turn should get its own reminder quoting the new message, got allow=%v:\n%s", v2.Allow, v2.Reason)
	}
}

// The reminder is read from the log, so a daemon restarted mid-turn does not
// send a second one.
func TestARestartDoesNotRemindAgain(t *testing.T) {
	m := newVerify(t, module.Config{})
	s := working(t, m, "do it", "a.txt")
	v := m.BeforeStop(context.Background(), s, module.StopInfo{})
	s.events = append(s.events, logged(t, "V", protocol.EventStopVeto, protocol.StopVetoData{Module: "verify", Reason: v.Reason}))

	fresh := newVerify(t, module.Config{})
	fresh.SessionResume(context.Background(), s)
	if again := fresh.BeforeStop(context.Background(), s, module.StopInfo{}); !again.Allow {
		t.Errorf("reminded again after a restart: %q", again.Reason)
	}
}

func currentBranch(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}
