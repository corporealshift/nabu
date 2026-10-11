// Package prompt keeps the person's latest prompt in front of the model, word
// for word, through summarising.
//
// A summary covers the history up to its newest event, prompt included, so
// after one the request the model is working on survives only as the
// summariser's paraphrase. The paraphrase keeps the request but can lose its
// limits: a step told to write a plan and nothing else can come back from a
// summary whose open items read "implement the plan". So after each summary
// the prompt goes back as a prefix block, as written, and lasts until the next
// summary puts it back again.
package prompt

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// promptMax caps the prompt kept. A step's prompt is a few kilobytes; a prompt
// past this is mostly pasted material, and its opening is where the request
// is.
const promptMax = 8 << 10

const header = "## The prompt you are working on\n\n" +
	"The person's latest prompt, word for word, kept through summarising. " +
	"The summary says how far the work has got; this says what was asked, and its limits still hold.\n\n"

// Module is the prompt module.
type Module struct {
	enabled bool
}

func (m *Module) Name() string { return "prompt" }

func (m *Module) Init(_ module.Host, cfg module.Config) error {
	m.enabled = cfg.Enabled()
	return nil
}

// AfterCompaction implements module.CompactionHook: it puts the latest prompt
// back once a summary has retired it.
func (m *Module) AfterCompaction(_ context.Context, s module.Session) ([]module.ContextBlock, error) {
	if !m.enabled || s == nil {
		return nil, nil
	}
	log, err := s.Events(nil)
	if err != nil {
		// No block leaves the session where it would be without this module.
		return nil, nil
	}
	text := latest(log)
	if text == "" {
		return nil, nil
	}
	return []module.ContextBlock{{Slot: "prefix", Content: header + cut(text, promptMax)}}, nil
}

// BeforeCompaction implements module.CompactionHook. It hands the summariser
// nothing: the prompt goes back word for word afterwards.
func (m *Module) BeforeCompaction(context.Context, module.Session, module.Range) []string {
	return nil
}

// latest is the newest user message's content, trimmed, or "" when there is
// none.
func latest(log []protocol.Event) string {
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Type != protocol.EventMessage {
			continue
		}
		var d protocol.MessageData
		if json.Unmarshal(log[i].Data, &d) == nil && d.Role == "user" {
			return strings.TrimSpace(d.Content)
		}
	}
	return ""
}

// cut shortens s to at most max bytes on a rune boundary, saying how much was
// left out.
func cut(s string, max int) string {
	if len(s) <= max {
		return s
	}
	end := max
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + fmt.Sprintf("\n\n(%d more bytes of the prompt are left out.)", len(s)-end)
}
