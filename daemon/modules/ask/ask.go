// Package ask lets the agent put a question to the person watching.
//
// The daemon could already ask — permission prompts use the same path — but
// nothing let the model start the conversation. Without it an agent that is
// unsure has two options, both bad: guess, and be wrong in a way nobody sees
// until later; or stop, and hand back a question dressed as a failure.
package ask

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/corporealshift/nabu/daemon/module"
)

// maxChoices keeps a question answerable on a phone. A list longer than this
// is a decision the agent should be making itself.
const maxChoices = 6

// Module is the ask module.
type Module struct {
	enabled bool
	host    module.Host
}

func (m *Module) Name() string { return "ask" }

func (m *Module) Init(h module.Host, cfg module.Config) error {
	m.enabled = cfg.Enabled()
	m.host = h
	return nil
}

// Tools implements module.ToolProvider.
func (m *Module) Tools() []module.Tool {
	if !m.enabled || m.host == nil {
		return nil
	}
	return []module.Tool{{
		Name: "ask",
		Description: "Ask the person watching a question, and wait for their answer. " +
			"Use it when the work genuinely forks and the right branch is theirs to " +
			"choose: which of two designs they want, which of several files they meant, " +
			"whether to go ahead with something that cannot be undone. " +
			"Ask exactly one question that resolves exactly one decision per call; " +
			"never bundle several questions or decisions together. If more input is " +
			"needed, ask the most important question first, then ask another only after " +
			"receiving its answer. " +
			"Do not use it for anything you can find out by reading the repository, " +
			"and do not use it to ask permission for a tool call — that is asked for " +
			"you. They may be asleep, so a question costs them an interruption and " +
			"costs you the wait.",
		Schema: json.RawMessage(`{"type":"object","required":["question"],"properties":{` +
			`"question":{"type":"string","description":"one clear question about one decision, in plain words"},` +
			`"choices":{"type":"array","items":{"type":"string"},` +
			`"description":"up to 6 mutually exclusive options for that one decision"}}}`),
		Run: m.run,
	}}
}

func (m *Module) run(ctx context.Context, s module.Session, args json.RawMessage) (string, error) {
	var a struct {
		Question string   `json:"question"`
		Choices  []string `json:"choices"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("ask: %w", err)
	}

	question := strings.TrimSpace(a.Question)
	if question == "" {
		return "", fmt.Errorf("ask: a question is required")
	}

	choices := cleanChoices(a.Choices)
	if len(choices) > maxChoices {
		return "", fmt.Errorf("ask: %d choices is too many to answer; offer at most %d",
			len(choices), maxChoices)
	}

	answer, err := m.host.UI().Ask(ctx, s, question, choices)
	if err != nil {
		// Nobody was there, or nobody answered in time. That is a fact about
		// the situation rather than a broken tool, and the agent should be
		// told plainly so it can decide what to do without a human.
		return "", fmt.Errorf("ask: %w", err)
	}

	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "they gave no answer", nil
	}
	return answer, nil
}

// cleanChoices drops blanks and duplicates, which a model offers more often
// than it means to and which make a picker confusing to answer.
func cleanChoices(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range in {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}
