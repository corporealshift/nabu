package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// Asking on the model's behalf when a session has gone on too long
// (docs/specs/2026-10-07-ask-claude-when-stuck-design.md).
//
// A liftoff task ran 344 turns, the last 5½ hours of them round one failing
// test, and never once called claude.ask. One fact it never found would have
// ended it. A turn cap would have cut off sessions that were still working, as
// caps did before; asking cuts nothing off.

// selfSource is how this module's own tool calls are logged.
const selfSource = "module:claude"

// Caps on the question's parts. Claude reads the repository itself; the
// question only has to say where to look and what keeps going wrong.
const (
	questionMax    = 24 << 10
	taskMax        = 6 << 10
	recentMessages = 10
	messageMax     = 600
	recentFailures = 4
	failureLines   = 40
	failureMax     = 2 << 10
	busiestFiles   = 5
)

// stretchOf splits a log at the last user message: what was asked, and
// everything since. With no user message the whole log is the stretch.
func stretchOf(log []protocol.Event) (task string, since []protocol.Event) {
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Type != protocol.EventMessage {
			continue
		}
		var d protocol.MessageData
		if json.Unmarshal(log[i].Data, &d) == nil && d.Role == "user" {
			return d.Content, log[i+1:]
		}
	}
	return "", log
}

// turns counts the assistant messages in a stretch.
func turns(since []protocol.Event) int {
	n := 0
	for _, e := range since {
		var d protocol.MessageData
		if e.Type == protocol.EventMessage && json.Unmarshal(e.Data, &d) == nil && d.Role == "assistant" {
			n++
		}
	}
	return n
}

// asked counts this module's own claude.ask calls in a stretch. The model's
// calls are its own business and do not count toward the automatic ones.
func asked(since []protocol.Event) int {
	n := 0
	for _, e := range since {
		var d protocol.ToolCallData
		if e.Type == protocol.EventToolCall && json.Unmarshal(e.Data, &d) == nil &&
			d.Tool == "claude.ask" && d.Source == selfSource {
			n++
		}
	}
	return n
}

// question is what Claude is asked: who is asking, the task, what the model
// has been saying, what keeps failing, and where it keeps working.
func question(log []protocol.Event) string {
	task, since := stretchOf(log)
	var b strings.Builder
	fmt.Fprintf(&b, "nabu is asking on behalf of a smaller local model that has spent %d turns "+
		"on the task below without anyone stepping in. It may be going round in circles.\n\n", turns(since))

	b.WriteString("## The task\n\n" + cut(strings.TrimSpace(task), taskMax) + "\n\n")

	if says := lastMessages(since); len(says) > 0 {
		b.WriteString("## What it has been saying, oldest first\n\n")
		for _, s := range says {
			b.WriteString("- " + s + "\n")
		}
		b.WriteString("\n")
	}

	if fails := lastFailures(since); len(fails) > 0 {
		b.WriteString("## What keeps failing, oldest first\n\n")
		for _, f := range fails {
			b.WriteString(f + "\n\n")
		}
	}

	if files := busiest(since); len(files) > 0 {
		b.WriteString("## The files it keeps changing\n\n")
		for _, f := range files {
			b.WriteString("- " + f + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("## What to do\n\nRead the repository. Say what is going wrong and what to do next, " +
		"as specific steps: files, functions, commands. If the approach is wrong, say so.\n")
	return cut(b.String(), questionMax)
}

// lastMessages is the model's last few things said, each on one line.
func lastMessages(since []protocol.Event) []string {
	var out []string
	for _, e := range since {
		var d protocol.MessageData
		if e.Type != protocol.EventMessage || json.Unmarshal(e.Data, &d) != nil || d.Role != "assistant" {
			continue
		}
		if s := strings.Join(strings.Fields(d.Content), " "); s != "" {
			out = append(out, cut(s, messageMax))
		}
	}
	return out[max(0, len(out)-recentMessages):]
}

// lastFailures is the tail of each of the last few results that failed: an
// error, or a build or test run that says it failed though the command ran.
func lastFailures(since []protocol.Event) []string {
	calls := map[string]protocol.ToolCallData{}
	var out []string
	for _, e := range since {
		switch e.Type {
		case protocol.EventToolCall:
			var d protocol.ToolCallData
			if json.Unmarshal(e.Data, &d) == nil {
				calls[d.CallID] = d
			}
		case protocol.EventToolResult:
			var d protocol.ToolResultData
			if json.Unmarshal(e.Data, &d) != nil || !failed(d) {
				continue
			}
			what := d.Tool
			if c, ok := calls[d.CallID]; ok {
				what += " " + cut(strings.Join(strings.Fields(string(c.Arguments)), " "), 200)
			}
			out = append(out, "`"+what+"`\n```\n"+cutFront(tail(d.Content, failureLines), failureMax)+"\n```")
		}
	}
	return out[max(0, len(out)-recentFailures):]
}

// failed says a result is worth showing Claude.
func failed(d protocol.ToolResultData) bool {
	if d.Status == "error" {
		return true
	}
	if d.Tool != "bash" {
		return false
	}
	if strings.Contains(d.Content, "FAILED") {
		return true
	}
	for _, line := range strings.Split(d.Content, "\n") {
		if strings.HasPrefix(line, "e:") {
			return true
		}
	}
	return false
}

// busiest names the files written or edited most, with how often.
func busiest(since []protocol.Event) []string {
	counts := map[string]int{}
	for _, e := range since {
		var d protocol.ToolCallData
		if e.Type != protocol.EventToolCall || json.Unmarshal(e.Data, &d) != nil || (d.Tool != "write" && d.Tool != "edit") {
			continue
		}
		var a struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(d.Arguments, &a) == nil && a.Path != "" {
			counts[a.Path]++
		}
	}
	paths := make([]string, 0, len(counts))
	for p := range counts {
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, j int) bool {
		if counts[paths[i]] != counts[paths[j]] {
			return counts[paths[i]] > counts[paths[j]]
		}
		return paths[i] < paths[j]
	})
	var out []string
	for _, p := range paths[:min(len(paths), busiestFiles)] {
		out = append(out, fmt.Sprintf("%s (%d times)", p, counts[p]))
	}
	return out
}

// tail keeps the last n lines.
func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(s, "\r\n", "\n"), "\n"), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}

// ellipsis marks a cut. Its three bytes count toward the limit.
const ellipsis = "…"

// cut keeps at most n bytes from the start, never splitting a character.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	i := max(0, n-len(ellipsis))
	for i > 0 && !runeStart(s[i]) {
		i--
	}
	return s[:i] + ellipsis
}

// cutFront keeps at most n bytes from the end, never splitting a character:
// the end of a build's output is where it says what failed.
func cutFront(s string, n int) string {
	if len(s) <= n {
		return s
	}
	i := len(s) - max(0, n-len(ellipsis))
	for i < len(s) && !runeStart(s[i]) {
		i++
	}
	return ellipsis + s[i:]
}

func runeStart(b byte) bool { return b&0xC0 != 0x80 }

// The defaults: a hundred turns without a word from anyone, then two hundred,
// then quiet.
const (
	defaultAutoAfter = 100
	defaultAutoMax   = 2
)

// autoAsk is one automatic ask, from start to recording.
type autoAsk struct {
	prompt string
	// spoken is how many user messages the log held when the ask began. More
	// by the time the answer lands means someone spoke, and the answer is
	// for a stretch that has ended.
	spoken int
	done   bool
	answer string
	err    error
}

// automatic says whether this module may ask on a session's behalf at all.
// A session in ask mode has a person approving its calls: they are there to
// be asked, and a call needing their approval would wait on them.
func (m *Module) automatic(s module.Session) bool {
	if !m.enabled || m.exe == "" || m.autoAfter == 0 || m.autoMax == 0 || s == nil {
		return false
	}
	mode := s.State().Options.PermissionMode
	return mode != "" && mode != protocol.PermissionAsk
}

// TurnEnd starts an automatic ask when the stretch reaches its next mark.
// The CLI takes minutes and a hook gets seconds, so it runs in the
// background, and the model keeps working meanwhile.
func (m *Module) TurnEnd(_ context.Context, s module.Session) {
	if !m.automatic(s) {
		return
	}
	log, err := s.Events(nil)
	if err != nil {
		return
	}
	_, since := stretchOf(log)
	done := asked(since)
	if done >= m.autoMax || turns(since) < m.autoAfter*(done+1) {
		return
	}

	m.mu.Lock()
	if m.waiting[s.ID()] != nil {
		m.mu.Unlock()
		return
	}
	a := &autoAsk{prompt: question(log), spoken: spoken(log)}
	m.waiting[s.ID()] = a
	m.mu.Unlock()

	dir := s.Workspace().Path
	go func() {
		// Not the hook's context: that ends with the hook.
		answer, err := m.ask(context.Background(), dir, a.prompt, m.timeout)
		m.mu.Lock()
		a.answer, a.err, a.done = answer, err, true
		m.mu.Unlock()
	}()
}

// BeforeRequest records an answered automatic ask, at the one point where
// every other call has its result: it calls claude.ask with the same prompt,
// and runAsk hands back the answer at once. The host logs the call and its
// result together, so the model sees an ordinary claude.ask in its
// conversation, and so does everyone reading the session later.
func (m *Module) BeforeRequest(ctx context.Context, s module.Session) ([]module.ContextBlock, error) {
	if s == nil {
		return nil, nil
	}
	m.mu.Lock()
	a := m.waiting[s.ID()]
	ready := a != nil && a.done
	m.mu.Unlock()
	if !ready {
		return nil, nil
	}

	log, err := s.Events(nil)
	if err == nil && spoken(log) == a.spoken && m.host != nil {
		args, _ := json.Marshal(map[string]string{"prompt": a.prompt})
		if _, err := m.host.Tools().Call(ctx, s, "claude.ask", args); err != nil && m.host.Log() != nil {
			m.host.Log().Info("automatic claude ask recorded as failed", "session", s.ID(), "err", err)
		}
	}
	// Recorded, refused by a gate, or overtaken by a person: either way this
	// ask is finished. Asking again is the next mark's business.
	m.drop(s.ID())
	return nil, nil
}

// SessionEnd drops an ask the session will not be there to hear.
func (m *Module) SessionEnd(_ context.Context, s module.Session) {
	if s != nil {
		m.drop(s.ID())
	}
}

// answered hands over a session's finished automatic ask for this prompt,
// once.
func (m *Module) answered(session, prompt string) (string, error, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.waiting[session]
	if a == nil || !a.done || a.prompt != prompt {
		return "", nil, false
	}
	delete(m.waiting, session)
	return a.answer, a.err, true
}

func (m *Module) drop(session string) {
	m.mu.Lock()
	delete(m.waiting, session)
	m.mu.Unlock()
}

// spoken counts the user messages in a log.
func spoken(log []protocol.Event) int {
	n := 0
	for _, e := range log {
		var d protocol.MessageData
		if e.Type == protocol.EventMessage && json.Unmarshal(e.Data, &d) == nil && d.Role == "user" {
			n++
		}
	}
	return n
}
