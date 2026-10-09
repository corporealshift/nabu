package claude

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/corporealshift/nabu/protocol"
)

func ev(typ protocol.EventType, data any) protocol.Event {
	raw, _ := json.Marshal(data)
	return protocol.Event{Type: typ, Data: raw}
}

func user(content string) protocol.Event {
	return ev(protocol.EventMessage, protocol.MessageData{Role: "user", Content: content})
}

func said(content string) protocol.Event {
	return ev(protocol.EventMessage, protocol.MessageData{Role: "assistant", Content: content})
}

func call(id, tool, source string, args any) protocol.Event {
	raw, _ := json.Marshal(args)
	return ev(protocol.EventToolCall, protocol.ToolCallData{CallID: id, Tool: tool, Arguments: raw, Source: source})
}

func result(id, tool, status, content string) protocol.Event {
	return ev(protocol.EventToolResult, protocol.ToolResultData{CallID: id, Tool: tool, Status: status, Content: content})
}

func TestTheStretchStartsAfterThePersonLastSpoke(t *testing.T) {
	log := []protocol.Event{
		user("first task"),
		said("on it"),
		call("a1", "claude.ask", selfSource, map[string]string{"prompt": "old"}),
		user("now this"),
		said("one"),
		said("two"),
		call("m1", "claude.ask", "model", map[string]string{"prompt": "mine"}),
		call("a2", "claude.ask", selfSource, map[string]string{"prompt": "auto"}),
	}
	task, since := stretchOf(log)
	if task != "now this" || len(since) != 4 {
		t.Errorf("stretch = %q and %d events, want the second task and the 4 after it", task, len(since))
	}
	if n := turns(since); n != 2 {
		t.Errorf("turns = %d, want 2: the first task's turn is before the stretch", n)
	}
	if n := asked(since); n != 1 {
		t.Errorf("asked = %d, want 1: the model's own ask and the earlier stretch's do not count", n)
	}

	task, since = stretchOf([]protocol.Event{said("a"), said("b")})
	if task != "" || turns(since) != 2 {
		t.Errorf("with no user message the whole log is the stretch, got %q and %d turns", task, turns(since))
	}
}

func TestTheQuestionSaysWhatClaudeNeeds(t *testing.T) {
	log := []protocol.Event{user("Build Mission Control.\nIt edits every setting.")}
	for i := range 12 {
		log = append(log, said(fmt.Sprintf("attempt %d:\n  rewriting the test", i)))
	}
	for i := range 6 {
		id := fmt.Sprintf("w%d", i)
		log = append(log, call(id, "write", "model", map[string]string{"path": "SettingsViewModelTest.kt"}), result(id, "write", "ok", "wrote"))
	}
	log = append(log,
		call("e1", "edit", "model", map[string]string{"path": "MissionControlScreenTest.kt"}), result("e1", "edit", "ok", "replaced"),
		call("b1", "bash", "model", map[string]string{"command": "gradlew test"}),
		result("b1", "bash", "ok", "noise\nSettingsViewModelTest > savedSettingsSurviveRestart FAILED\nexpected:<[persist-host]> but was:<[]>"),
		call("b2", "bash", "model", map[string]string{"command": "gradlew compile"}),
		result("b2", "bash", "ok", "e: file:///Test.kt:34 unresolved reference: close"),
		call("b3", "bash", "model", map[string]string{"command": "git status"}),
		result("b3", "bash", "ok", "nothing to commit"),
		call("r1", "read", "model", map[string]string{"path": "gone.kt"}),
		result("r1", "read", "error", "no such file"),
	)

	q := question(log, nil)
	for _, want := range []string{
		"spent 12 turns",
		"Build Mission Control.\nIt edits every setting.",
		"attempt 11: rewriting the test", // whitespace folded to one line
		"savedSettingsSurviveRestart FAILED",
		"unresolved reference: close",
		"no such file",
		"SettingsViewModelTest.kt (6 times)",
		"MissionControlScreenTest.kt (1 times)",
		"Read the repository.",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("the question is missing %q", want)
		}
	}
	if strings.Contains(q, "attempt 1:") {
		t.Error("only the last 10 messages belong in it")
	}
	if strings.Contains(q, "nothing to commit") {
		t.Error("a result that did not fail does not belong in it")
	}
	if strings.Index(q, "SettingsViewModelTest.kt") > strings.Index(q, "MissionControlScreenTest.kt (1") {
		t.Error("the busiest file comes first")
	}
}

func TestAHugeLogStillGivesAShortQuestion(t *testing.T) {
	huge := strings.Repeat("é", 20_000) // two bytes each
	log := []protocol.Event{user(huge)}
	for i := range 50 {
		id := fmt.Sprint(i)
		log = append(log, said(huge), call(id, "bash", "model", map[string]string{"command": huge}),
			result(id, "bash", "error", strings.Repeat(huge+"\n", 50)))
	}
	q := question(log, nil)
	if len(q) > questionMax {
		t.Errorf("the question is %d bytes, over %d", len(q), questionMax)
	}
	if !utf8.ValidString(q) {
		t.Error("a cut split a character")
	}
}

func TestCutsKeepWholeCharactersWithinTheLimit(t *testing.T) {
	s := strings.Repeat("é", 10) // 20 bytes
	for n := 0; n <= 22; n++ {
		for name, got := range map[string]string{"cut": cut(s, n), "cutFront": cutFront(s, n)} {
			if !utf8.ValidString(got) {
				t.Errorf("%s(%d) = %q splits a character", name, n, got)
			}
			if len(got) > max(n, len(ellipsis)) {
				t.Errorf("%s(%d) is %d bytes", name, n, len(got))
			}
		}
	}
	if cut("short", 10) != "short" || cutFront("short", 10) != "short" {
		t.Error("what fits is left alone")
	}
	if got := cutFront("aaaa\nend of the build", 15); !strings.HasSuffix(got, "end of the build"[4:]) {
		t.Errorf("cutFront keeps the end, got %q", got)
	}
}
