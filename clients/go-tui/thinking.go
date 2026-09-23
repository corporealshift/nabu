package tui

import (
	"fmt"
	"strings"

	"github.com/corporealshift/nabu/clients/goclient"

	"github.com/corporealshift/nabu/protocol"
)

// thinkingLine is one transcript slot: collapsed it is a single line saying
// there was reasoning, expanded it is the reasoning itself. One slot either
// way, so expanding does not move the lines around it.
func thinkingLine(content string, expanded bool) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if !expanded {
		return dim.Render(fmt.Sprintf("~ thought (%d words) · t to show", words(content)))
	}

	var b strings.Builder
	b.WriteString(dim.Render("~ thought · t to hide"))
	for _, line := range strings.Split(content, "\n") {
		b.WriteString("\n" + dim.Render("  "+line))
	}
	return b.String()
}

func words(s string) int { return len(strings.Fields(s)) }

// rememberThought writes a thought as a block of its own, and records where so
// the toggle can rewrite that slot in place.
func (m *model) rememberThought(ev protocol.Event) {
	var d protocol.ThinkingData
	if unmarshal(ev, &d) != nil || strings.TrimSpace(d.Content) == "" {
		return
	}
	if m.thoughts == nil {
		m.thoughts = map[int]string{}
	}
	m.startBlock()
	m.thoughts[len(m.transcript)] = d.Content
	m.transcript = append(m.transcript, entry{text: thinkingLine(d.Content, m.showThinking)})
}

// toggleThinking shows or hides every thought at once. The transcript is a
// flat list with no cursor in it, so there is nothing to expand one by one.
func (m *model) toggleThinking() {
	m.showThinking = !m.showThinking
	for at, content := range m.thoughts {
		if at < len(m.transcript) {
			m.transcript[at] = entry{text: thinkingLine(content, m.showThinking)}
		}
	}
	// Entries already wrapped just changed in place.
	m.invalidateWrap()
	m.refresh()
}

// addThinking accumulates streaming reasoning for the live preview. It is only
// shown when thinking is expanded: someone who has hidden it has said so.
func (m *model) addThinking(d goclient.SessionDelta) {
	if d.TurnID != m.thinkingTurn {
		m.thinkingTurn = d.TurnID
		m.thinkingNow = ""
	}
	m.thinkingNow += d.Text
	if m.showThinking {
		m.refresh()
	}
}
