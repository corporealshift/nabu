package tui

import (
	"fmt"
	"strings"

	"github.com/corporealshift/nabu/protocol"
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

	// text carries a prompt or a brief
	text string
	// labels are the whole new label list a /run or /goal sets.
	labels []string
}

type actionKind int

const (
	actAnswer actionKind = iota
	actAnswerAsk
	actPrompt
	actInterrupt
	actStop
	actCompact
	actAttach
	actListSessions
	actListArchived
	actArchive
	actRestore
	actStats
	actRun
	actGoal
	actContext
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
	{"/run [text]", "hand this session to the runner, the text as its brief"},
	{"/goal [text]", "hand the runner a broad goal to work as many runs; bare /goal resumes a blocked one"},
	{"/compact", "summarise the history now"},
	{"/context [normal|large]", "how much of the model's window this session fills before it is summarised"},
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
	case "/run":
		return commandResult{act: &action{kind: actRun, text: rest}}
	case "/goal":
		return commandResult{act: &action{kind: actGoal, text: rest}}
	case "/context":
		switch size := strings.ToLower(rest); size {
		case "", protocol.ContextNormal, protocol.ContextLarge:
			return commandResult{act: &action{kind: actContext, text: size}}
		}
		return commandResult{note: "/context takes normal or large"}
	case "/help":
		return commandResult{note: helpText}
	default:
		return commandResult{note: "unknown command " + name + "\n" + helpText}
	}
}

// runPrefix starts every label the runner reads or sets on a run's home
// (docs/specs/2026-09-30-orchestrated-runs-design.md).
const runPrefix = "run:"

// runLabels is a session's labels once /run hands it to the runner: whatever
// run:* label it had is replaced by run:requested, and the rest are kept. On a
// failed run that is also how it is resumed.
func runLabels(current []string) []string {
	out := []string{}
	for _, l := range current {
		if !strings.HasPrefix(l, runPrefix) {
			out = append(out, l)
		}
	}
	return append(out, runPrefix+"requested")
}

// goalPrefix starts every label the runner reads or sets on a goal's home
// (docs/specs/2026-10-06-goals-design.md).
const goalPrefix = "goal:"

// goalLabels is a session's labels once /goal hands it to the runner: every
// goal:* label is replaced by goal:requested, which is also how a blocked
// goal is resumed.
func goalLabels(current []string) []string {
	out := []string{}
	for _, l := range current {
		if !strings.HasPrefix(l, goalPrefix) {
			out = append(out, l)
		}
	}
	return append(out, goalPrefix+"requested")
}

// goalStatus is how far a goal is, from its home's labels, as "goal: runs,
// round 2". Empty for a session that is not a goal's home.
func goalStatus(labels []string) string {
	var step, round string
	for _, l := range labels {
		rest, ok := strings.CutPrefix(l, goalPrefix)
		if !ok {
			continue
		}
		if r, ok := strings.CutPrefix(rest, "round:"); ok {
			round = r
		} else {
			step = rest
		}
	}
	if step == "" {
		return ""
	}
	if round != "" {
		return fmt.Sprintf("goal: %s, round %s", step, round)
	}
	return "goal: " + step
}

// runStatus is how far a run is, read from its home's labels, as "run: fix
// (3/10)". Empty for a session that is not a run's home.
func runStatus(labels []string) string {
	var step, attempt string
	for _, l := range labels {
		rest, ok := strings.CutPrefix(l, runPrefix)
		if !ok {
			continue
		}
		if a, ok := strings.CutPrefix(rest, "attempt:"); ok {
			attempt = a
		} else {
			step = rest
		}
	}
	if step == "" {
		return ""
	}
	if attempt != "" {
		return fmt.Sprintf("run: %s (%s)", step, attempt)
	}
	return "run: " + step
}
