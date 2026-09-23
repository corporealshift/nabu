package tui

import (
	"fmt"
	"strings"
)

// action is something the human asked for that needs the network. Update is
// pure, so it produces one of these and the connection goroutine carries it
// out.
type action struct {
	kind      actionKind
	sessionID string

	// permission reply
	id      any
	approve bool
	reason  string

	// text carries a prompt or a goal condition
	text string
}

type actionKind int

const (
	actAnswer actionKind = iota
	actAnswerAsk
	actPrompt
	actInterrupt
	actStop
	actCompact
	actSetGoal
	actClearGoal
	actAttach
	actListSessions
	actListArchived
	actArchive
	actRestore
	actStats
)

// commandResult is what a line of composer input means: something to do, or
// something to tell the user.
type commandResult struct {
	act *action
	// note is shown in the transcript instead of acting.
	note string
	// openPicker asks the view for the session list.
	openPicker bool
	// open names a page the agent made, to open.
	open string
}

// commands is every / command, for /help.
var commands = []struct{ name, what string }{
	{"/goal <text>", "set a goal"},
	{"/goal", "clear it"},
	{"/compact", "summarise the history now"},
	{"/stop", "end the session"},
	{"/archive", "put this session away"},
	{"/stats", "how much work this session was"},
	{"/open <name>", "open a page the agent made"},
	{"/sessions", "switch"},
	{"/help", "this list"},
}

// helpText lists the commands one to a line, names aligned: run together on
// one line they wrapped into a paragraph nobody could scan (issue 106).
var helpText = func() string {
	width := 0
	for _, c := range commands {
		width = max(width, len(c.name))
	}
	lines := []string{"commands"}
	for _, c := range commands {
		lines = append(lines, fmt.Sprintf("  %-*s  %s", width, c.name, c.what))
	}
	return strings.Join(lines, "\n")
}()

// parseCommand turns a line of input into what should happen. Text without a
// leading slash is a prompt, verbatim.
//
// An unknown command is a note, never a prompt: sending "/gaol fix the tests"
// to the model as though it were an instruction is worse than saying it is not
// a command.
func parseCommand(line string) commandResult {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return commandResult{}
	}
	if !strings.HasPrefix(trimmed, "/") {
		return commandResult{act: &action{kind: actPrompt, text: line}}
	}

	name, rest, _ := strings.Cut(trimmed, " ")
	rest = strings.TrimSpace(rest)

	switch strings.ToLower(name) {
	case "/goal":
		if rest == "" {
			return commandResult{act: &action{kind: actClearGoal}}
		}
		return commandResult{act: &action{kind: actSetGoal, text: rest}}
	case "/stop":
		return commandResult{act: &action{kind: actStop}}
	case "/compact":
		return commandResult{act: &action{kind: actCompact}}
	case "/archive":
		return commandResult{act: &action{kind: actArchive}}
	case "/sessions":
		return commandResult{openPicker: true}
	case "/open":
		if rest == "" {
			return commandResult{note: "/open needs the name of a page — o opens the newest"}
		}
		return commandResult{open: rest}
	case "/stats":
		return commandResult{act: &action{kind: actStats}}
	case "/help":
		return commandResult{note: helpText}
	default:
		return commandResult{note: "unknown command " + name + "\n" + helpText}
	}
}
