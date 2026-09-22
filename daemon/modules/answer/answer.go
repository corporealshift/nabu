// Package answer keeps a question from being taken as a request for work.
//
// Asked "what's the status here?", the agent ran the tests, said "3 failures
// left. Let me fix them.", and started editing. Asked "why did you stop
// here?", it wrote a plan file and committed it to main (issue 76). A line in
// the system prompt saying not to did not hold: the model reads it once, at
// the top of a long context, and the reminder it needs is at the bottom.
//
// So on a turn where the person only asked something, this module puts that
// reminder where the model reads last, and holds anything that would change
// the workspace for the person to approve. Reading and running things to find
// the answer stays free. It does not deny: a request that merely looked like a
// question costs one approval, not the work.
package answer

import (
	"context"
	"encoding/json"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// reminder is the suffix block on a turn that asked a question.
const reminder = `The person's last message asks a question. Answer it, then stop.

Read files and run commands if you need to in order to answer accurately. Do not edit files, commit, or pick up earlier work: they have not asked for that. If you think something should change, say what and why, and let them decide.`

// Module is the answer module.
type Module struct {
	enabled bool
}

func (m *Module) Name() string { return "answer" }

func (m *Module) Init(_ module.Host, cfg module.Config) error {
	m.enabled = cfg.Enabled()
	return nil
}

// questionTurn reports whether the session is answering a question.
func (m *Module) questionTurn(s module.Session) bool {
	if !m.enabled || s == nil {
		return false
	}
	log, err := s.Events(nil)
	if err != nil {
		return false
	}
	return module.QuestionTurn(log, s.State())
}

// BeforeRequest implements module.RequestHook. The reminder rides at the end
// of the request, where it is read last, as a logged context event (invariant
// 3). It is repeated on every request of the turn, because after the first tool
// call the previous one is no longer at the end.
func (m *Module) BeforeRequest(_ context.Context, s module.Session) ([]module.ContextBlock, error) {
	if !m.questionTurn(s) {
		return nil, nil
	}
	return []module.ContextBlock{{Slot: "suffix", Content: reminder}}, nil
}

// GateTool implements module.ToolGate: a change to the workspace, made while
// answering a question, waits for the person to say yes.
func (m *Module) GateTool(_ context.Context, s module.Session, call protocol.ToolCallData) module.Verdict {
	if !module.ChangesWorkspace(call) || !m.questionTurn(s) {
		return module.Verdict{Decision: module.Allow}
	}
	return module.Verdict{
		Decision: module.Ask,
		Summary:  "you asked a question, and answering it wants to change the workspace: " + describe(call),
		Risk:     "medium",
	}
}

// describe names the change in a few words, for the approval prompt.
func describe(call protocol.ToolCallData) string {
	d := call.Tool
	if arg := firstArg(call); arg != "" {
		d += " " + arg
	}
	return d
}

// firstArg is the argument that says what a call touches: a path, a command,
// or a git operation.
func firstArg(call protocol.ToolCallData) string {
	var args map[string]any
	if json.Unmarshal(call.Arguments, &args) != nil {
		return ""
	}
	for _, key := range []string{"path", "command", "op"} {
		if v, ok := args[key].(string); ok && v != "" {
			if r := []rune(v); len(r) > 120 {
				v = string(r[:120]) + "…"
			}
			return v
		}
	}
	return ""
}
