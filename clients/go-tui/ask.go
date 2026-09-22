package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/corporealshift/nabu/clients/goclient"
)

// question is something the agent asked that is waiting on an answer.
type question struct {
	id  any // the JSON-RPC id to answer with
	req goclient.AskRequest
	// typed is the free-text answer being composed. A question with choices
	// still allows one: the offered options are rarely the whole truth.
	typed string
}

// askPanel draws the question below the transcript. Unlike a permission prompt,
// the context that led to a question helps the person give a useful answer.
func (m model) askPanel() string {
	q := m.asking

	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("The agent is asking") + "\n\n")
	b.WriteString(wrap(q.req.Question, 68) + "\n\n")

	for i, choice := range q.req.Choices {
		b.WriteString(fmt.Sprintf("%s %s\n", badgeOK.Render(fmt.Sprint(i+1)), choice))
	}
	if len(q.req.Choices) > 0 {
		b.WriteString("\n")
	}

	b.WriteString(userStyle.Render("› ") + q.typed + badgeOK.Render("▌") + "\n\n")

	help := "type an answer · enter to send"
	if len(q.req.Choices) > 0 {
		help = "press a number, or type an answer · enter to send"
	}
	b.WriteString(dim.Render(help))

	return overlayBox.Render(b.String())
}

// onAskKey edits or sends the answer. Every printable key belongs to the
// answer while a question is on screen, so q types a letter rather than
// quitting and the agent is not left waiting on a keystroke that went
// somewhere else.
func (m model) onAskKey(key string, runes []rune) (model, string, bool) {
	switch key {
	case "enter":
		answer := strings.TrimSpace(m.asking.typed)
		if answer == "" {
			return m, "", false // an empty answer is not an answer
		}
		return m, answer, true

	case "backspace":
		if r := []rune(m.asking.typed); len(r) > 0 {
			m.asking.typed = string(r[:len(r)-1])
		}
		return m, "", false

	case " ", "space":
		m.asking.typed += " "
		return m, "", false
	}

	// A number picks the choice it labels, but only while nothing has been
	// typed: once an answer is being composed, digits belong to it.
	if m.asking.typed == "" && len(runes) == 1 {
		if i := int(runes[0] - '1'); i >= 0 && i < len(m.asking.req.Choices) {
			return m, m.asking.req.Choices[i], true
		}
	}

	if len(runes) > 0 {
		m.asking.typed += string(runes)
	}
	return m, "", false
}
