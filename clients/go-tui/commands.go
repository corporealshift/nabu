package tui

import "strings"

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
)

// commandResult is what a line of composer input means: something to do, or
// something to tell the user.
type commandResult struct {
	act *action
	// note is shown in the transcript instead of acting.
	note string
	// openPicker asks the view for the session list.
	openPicker bool
}

const helpText = "commands: /goal <text> set a goal · /goal clear it · " +
	"/compact summarise the history now · /stop end the session · " +
	"/archive put this session away · /sessions switch · /help this list"

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
	case "/help":
		return commandResult{note: helpText}
	default:
		return commandResult{note: "unknown command " + name + " — " + helpText}
	}
}
