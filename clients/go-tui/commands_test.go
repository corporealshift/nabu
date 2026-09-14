package main

import (
	"strings"
	"testing"
)

func TestTextWithoutASlashIsAPrompt(t *testing.T) {
	res := parseCommand("fix the failing test")
	if res.act == nil || res.act.kind != actPrompt {
		t.Fatalf("want a prompt, got %+v", res)
	}
	if res.act.text != "fix the failing test" {
		t.Errorf("text: got %q", res.act.text)
	}
}

// The prompt is passed verbatim: leading whitespace may be deliberate.
func TestPromptTextIsVerbatim(t *testing.T) {
	res := parseCommand("  indented on purpose")
	if res.act == nil || res.act.text != "  indented on purpose" {
		t.Errorf("text should be untouched, got %q", res.act.text)
	}
}

func TestGoalCommands(t *testing.T) {
	set := parseCommand("/goal the gate is green")
	if set.act == nil || set.act.kind != actSetGoal {
		t.Fatalf("want set-goal, got %+v", set)
	}
	if set.act.text != "the gate is green" {
		t.Errorf("condition: got %q", set.act.text)
	}

	for _, line := range []string{"/goal", "/goal   "} {
		clear := parseCommand(line)
		if clear.act == nil || clear.act.kind != actClearGoal {
			t.Errorf("%q should clear the goal, got %+v", line, clear)
		}
	}
}

func TestStopCommand(t *testing.T) {
	res := parseCommand("/stop")
	if res.act == nil || res.act.kind != actStop {
		t.Fatalf("want stop, got %+v", res)
	}
}

func TestSessionsCommandOpensThePicker(t *testing.T) {
	res := parseCommand("/sessions")
	if !res.openPicker {
		t.Fatal("/sessions should open the picker")
	}
	if res.act != nil {
		t.Error("opening the picker needs no network action")
	}
}

func TestHelpCommand(t *testing.T) {
	res := parseCommand("/help")
	if res.note == "" {
		t.Fatal("/help should produce a note")
	}
	if res.act != nil {
		t.Error("/help needs no network action")
	}
	for _, cmd := range []string{"/goal", "/stop", "/sessions"} {
		if !strings.Contains(res.note, cmd) {
			t.Errorf("help should mention %s", cmd)
		}
	}
}

// Sending "/gaol fix the tests" to the model as an instruction is worse than
// saying it is not a command.
func TestUnknownCommandIsNeverSentToTheModel(t *testing.T) {
	for _, line := range []string{"/gaol fix the tests", "/nope", "/"} {
		res := parseCommand(line)
		if res.act != nil {
			t.Errorf("%q produced an action %v; it must not reach the model", line, res.act.kind)
		}
		if res.note == "" {
			t.Errorf("%q should say it is not a command", line)
		}
	}
}

func TestUnknownCommandNamesItself(t *testing.T) {
	res := parseCommand("/gaol x")
	if !strings.Contains(res.note, "/gaol") {
		t.Errorf("the note should name the command, got %q", res.note)
	}
}

func TestEmptyInputDoesNothing(t *testing.T) {
	for _, line := range []string{"", "   ", "\t"} {
		res := parseCommand(line)
		if res.act != nil || res.note != "" || res.openPicker {
			t.Errorf("%q should do nothing, got %+v", line, res)
		}
	}
}

// Commands are recognised whatever case they are typed in.
func TestCommandsAreCaseInsensitive(t *testing.T) {
	if res := parseCommand("/GOAL something"); res.act == nil || res.act.kind != actSetGoal {
		t.Errorf("/GOAL should set a goal, got %+v", res)
	}
	if res := parseCommand("/Stop"); res.act == nil || res.act.kind != actStop {
		t.Errorf("/Stop should stop, got %+v", res)
	}
}
