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
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/corporealshift/nabu/daemon/module"
)

// maxChoices keeps a question answerable on a phone. A list longer than this
// is a decision the agent should be making itself.
const maxChoices = 6

// maxQuestions bounds one call. Each is its own interruption, answered in turn;
// more than this is a questionnaire, and the agent should be deciding some.
const maxQuestions = 4

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
		Description: "Ask the person watching, and wait for their answer. " +
			"Use it when the work genuinely forks and the right branch is theirs to " +
			"choose: which of two designs they want, which of several files they meant, " +
			"whether to go ahead with something that cannot be undone. " +
			"Each question resolves exactly one decision, and its choices are the answers " +
			"to that decision alone — never combinations like \"both\" or \"just the first\". " +
			"If you need several decisions, put each in its own entry of `questions`: they " +
			"are asked one at a time, each with its own choices, and the answers come back " +
			"together. Never number several questions inside one question. " +
			"Do not use it for anything you can find out by reading the repository, " +
			"and do not use it to ask permission for a tool call — that is asked for " +
			"you. They may be asleep, so a question costs them an interruption and " +
			"costs you the wait. If nobody answers, do not ask again this turn.",
		Schema: json.RawMessage(`{"type":"object","properties":{` +
			`"question":{"type":"string","description":"one clear question about one decision, in plain words"},` +
			`"choices":{"type":"array","items":{"type":"string"},` +
			`"description":"up to 6 mutually exclusive answers to that one question"},` +
			`"questions":{"type":"array","minItems":1,"maxItems":4,"description":"several separate decisions, asked one at a time in this order; use instead of question and choices",` +
			`"items":{"type":"object","required":["question"],"properties":{` +
			`"question":{"type":"string","description":"one clear question about one decision"},` +
			`"choices":{"type":"array","items":{"type":"string"},"description":"up to 6 mutually exclusive answers to it"}}}}}}`),
		Run: m.run,
	}}
}

// item is one question and the answers offered to it.
type item struct {
	Question string   `json:"question"`
	Choices  []string `json:"choices"`
}

func (m *Module) run(ctx context.Context, s module.Session, args json.RawMessage) (string, error) {
	var a struct {
		item
		Questions []item `json:"questions"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("ask: %w", err)
	}

	items := a.Questions
	single := strings.TrimSpace(a.Question) != ""
	switch {
	case single && len(items) > 0:
		return "", fmt.Errorf("ask: give either question or questions, not both")
	case single:
		items = []item{a.item}
	case len(items) == 0:
		return "", fmt.Errorf("ask: a question is required")
	case len(items) > maxQuestions:
		return "", fmt.Errorf("ask: %d questions is too many at once; ask at most %d and decide the rest yourself",
			len(items), maxQuestions)
	}

	for i := range items {
		items[i].Question = strings.TrimSpace(items[i].Question)
		items[i].Choices = cleanChoices(items[i].Choices)
		if items[i].Question == "" {
			return "", fmt.Errorf("ask: question %d is empty", i+1)
		}
		if len(items[i].Choices) > maxChoices {
			return "", fmt.Errorf("ask: %d choices is too many to answer; offer at most %d",
				len(items[i].Choices), maxChoices)
		}
		if n := bundled(items[i].Question); n > 1 {
			// Issue 70: three questions numbered inside one, answered with one
			// pick from choices that could only fit them all by combining them.
			return "", fmt.Errorf("ask: that is %d questions in one, which cannot be answered one at a time. "+
				"Put each in its own entry of questions, each with its own choices", n)
		}
	}

	var answers []string
	for i, it := range items {
		q := it.Question
		if len(items) > 1 {
			q = fmt.Sprintf("(%d of %d) %s", i+1, len(items), q)
		}
		answer, err := m.host.UI().Ask(ctx, s, q, it.Choices)
		if err != nil {
			// Nobody was there, or nobody answered in time. That is a fact about
			// the situation rather than a broken tool. Asking again straight
			// away only repeats it: a session has asked one question six times
			// running into nobody (issue 70).
			msg := fmt.Sprintf("ask: %v. Nobody is answering right now, so do not ask again this turn: "+
				"decide for yourself if the choice is easy to undo, and otherwise finish what you can and "+
				"put the question in your reply", err)
			if len(answers) > 0 {
				msg += ". Answered before that:\n" + strings.Join(answers, "\n")
			}
			return "", errors.New(msg)
		}
		answer = strings.TrimSpace(answer)
		if answer == "" {
			answer = "they gave no answer"
		}
		if len(items) == 1 {
			return answer, nil
		}
		answers = append(answers, fmt.Sprintf("%d. %s\n   → %s", i+1, firstLine(it.Question), answer))
	}
	return strings.Join(answers, "\n"), nil
}

// listedQuestion is a list item that asks something: "1. Should I …?".
var listedQuestion = regexp.MustCompile(`(?m)^\s*(\d+[.)]|[-*•])\s+.*\?`)

// bundled counts the questions packed into one: list items that each ask
// something, or failing that, question marks. One decision can take two
// question marks ("Should I A? Or would you rather B?"), so under three is
// read as one.
func bundled(q string) int {
	if n := len(listedQuestion.FindAllString(q, -1)); n >= 2 {
		return n
	}
	if n := strings.Count(q, "?"); n >= 3 {
		return n
	}
	return 1
}

// firstLine is a question's opening, to label its answer by.
func firstLine(q string) string {
	if i := strings.IndexAny(q, "\r\n"); i >= 0 {
		q = q[:i]
	}
	if r := []rune(q); len(r) > 100 {
		q = string(r[:100]) + "…"
	}
	return q
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
