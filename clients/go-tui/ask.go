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

// maxAskWidth keeps a question readable on a wide terminal: past this, lines
// are too long to read in one sweep.
const maxAskWidth = 96

// askBoxChrome is the border and padding around the panel: two columns of
// border and four of padding across, two rows of border down.
const (
	askBoxWidthChrome  = 6
	askBoxHeightChrome = 2
)

// askPanel draws the question below the transcript. Unlike a permission prompt,
// the context that led to a question helps the person give a useful answer.
//
// The question keeps its line breaks. It used to be wrapped as one paragraph,
// so a numbered list of choices or sub-questions arrived run together, and
// telling which answer went with which was the hard part (issue 70). It also
// fits the screen: when it cannot, the question is cut, never the choices or
// the answer line, since those are what the person has to reach.
func (m model) askPanel() string {
	return overlayBox.Render(strings.Join(m.askLines(), "\n"))
}

// askLines is the panel's content, fitted to the room there is.
func (m model) askLines() []string {
	q := m.asking
	width := m.width - askBoxWidthChrome
	if width > maxAskWidth {
		width = maxAskWidth
	}
	if width < 20 {
		width = 20
	}

	head := []string{lipgloss.NewStyle().Bold(true).Render("The agent is asking"), ""}
	body := wrapParagraphs(q.req.Question, width)

	var tail []string
	if len(q.req.Choices) > 0 {
		tail = append(tail, "")
		for i, choice := range q.req.Choices {
			label := badgeOK.Render(fmt.Sprint(i+1)) + " "
			for j, line := range wrapText(choice, width-2) {
				if j > 0 {
					label = "  "
				}
				tail = append(tail, label+line)
			}
		}
	}
	help := "type an answer · enter to send"
	if len(q.req.Choices) > 0 {
		help = "press a number, or type an answer · enter to send"
	}
	tail = append(tail, "", userStyle.Render("› ")+q.typed+badgeOK.Render("▌"), "", dim.Render(help))

	// Leave the status and help lines, and one row of transcript.
	room := m.height - 3 - askBoxHeightChrome
	if spare := room - len(head) - len(tail); spare < len(body) {
		if spare < 1 {
			spare = 1
		}
		body = append(body[:spare-1:spare-1], dim.Render("… (the rest of the question is cut to fit)"))
	}

	out := append(head, body...)
	return append(out, tail...)
}

// wrapParagraphs wraps each line of text on its own, so blank lines and list
// items survive.
func wrapParagraphs(text string, width int) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		out = append(out, wrapText(strings.TrimRight(line, " \r"), width)...)
	}
	return out
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
