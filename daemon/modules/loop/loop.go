// Package loop notices when the model is repeating itself, and tells it so
// with facts from the log.
//
// The logs had the model write the same 1,259 bytes to error.rs 32 times in
// 13 minutes, saying "I've been stuck in a loop" before every write. Knowing
// it was looping did not help; what broke it was a call that returned
// something new. So this module's job is to make repetition return something
// new (docs/proposals/2026-09-23-breaking-repetition-loops.md).
//
// Repeating a call is not by itself a loop. Re-running the build after an
// edit is how a fix is checked, and polling CI is how a run is waited on: in
// the logs, 54 of 81 repeated checks saw their output change along the way.
// So a repeat is sorted into one of three cases, and only one is treated as a
// loop:
//
//   - identical change: the same write or edit, same arguments, same result,
//     and nothing else has touched the file. A notice, then a refusal, then
//     the session stops blocked so the person finds out.
//   - change not landing: the same check failing the same way after edits.
//     The model is told its edits are not reaching the failure. Never refused.
//   - watching: the same check, same output, nothing changed in between. A
//     hint about the wait tool at a high threshold. Never refused.
//
// And one more, about turns rather than calls:
//
//   - stalled: several turns in a row, of more than one call, in which
//     nothing came back that had not come back before. A notice that points
//     at stopping or asking, then the session stops blocked. One call
//     repeated is watching, and left to that case.
//
// Everything is derived from the log since the person's last message, so a
// word from them starts every count again.
package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// refusedPrefix starts every refusal this module makes, so it can recognise
// its own refusals in the log.
const refusedPrefix = "loop: "

// Module is the loop module.
type Module struct {
	enabled bool
	// identicalAfter is how many identical changes run before the next is
	// refused; the notice comes with the last one that runs.
	identicalAfter int
	// haltAfter is how many refusals of one change the model gets before
	// trying it again stops the session.
	haltAfter int
	// notLandingAfter is how many identical failures of one check, with
	// changes made in between, earn a notice.
	notLandingAfter int
	// watchAfter is how many identical outputs of one check, with nothing
	// changed in between, earn a hint; it repeats at every multiple.
	watchAfter int
	// stalledAfter is how many turns in a row returning nothing new earn a
	// notice.
	stalledAfter int
	// stalledHaltAfter is how many more such turns after the notice stop the
	// session.
	stalledHaltAfter int
}

func (m *Module) Name() string { return "loop" }

// Init reads enabled, identical_after (3), halt_after (2),
// not_landing_after (3), watch_after (5), stalled_after (5) and
// stalled_halt_after (2).
func (m *Module) Init(_ module.Host, cfg module.Config) error {
	m.enabled = cfg.Enabled()
	m.identicalAfter = max(cfg.Int("identical_after", 3), 2)
	m.haltAfter = max(cfg.Int("halt_after", 2), 1)
	m.notLandingAfter = max(cfg.Int("not_landing_after", 3), 2)
	m.watchAfter = max(cfg.Int("watch_after", 5), 2)
	m.stalledAfter = max(cfg.Int("stalled_after", 5), 2)
	m.stalledHaltAfter = max(cfg.Int("stalled_halt_after", 2), 1)
	return nil
}

// ---------------------------------------------------------------- history

// attempt is one model tool call and what came back.
type attempt struct {
	tool    string
	path    string // for write and edit
	key     string // tool and canonical arguments
	label   string // "write error.rs", "bash cargo build"
	command string // for bash
	result  string // status and content: two attempts with equal results said the same thing
	content string
	ok      bool
	at      time.Time
	refused bool // refused by this module
	changes bool // changed the workspace, and succeeded
	fresh   bool // made since the model's last message: what the next request is the first to see
}

// history returns the model's tool calls since the person's last message.
func history(log []protocol.Event) []attempt {
	start := sincePerson(log)
	calls := map[string]protocol.ToolCallData{}
	var out []attempt
	freshFrom := 0
	for _, e := range log[start:] {
		switch e.Type {
		case protocol.EventMessage:
			freshFrom = len(out)
		case protocol.EventToolCall:
			if c := protocol.MustData[protocol.ToolCallData](e); c.Source == "model" {
				calls[c.CallID] = *c
			}
		case protocol.EventToolResult:
			r := protocol.MustData[protocol.ToolResultData](e)
			c, ok := calls[r.CallID]
			if !ok {
				continue
			}
			a := attempt{
				tool:    c.Tool,
				key:     c.Tool + "\x00" + canonical(c.Arguments),
				label:   label(c),
				result:  r.Status + "\x00" + r.Content,
				content: r.Content,
				ok:      r.Status == "ok",
				at:      e.Timestamp,
				refused: r.Status == "error" && strings.HasPrefix(r.Content, "denied: "+refusedPrefix),
			}
			a.changes = a.ok && module.ChangesWorkspace(c)
			a.command = argString(c.Arguments, "command")
			if mutating(c.Tool) {
				a.path = argPath(c.Arguments)
			}
			out = append(out, a)
		}
	}
	for i := freshFrom; i < len(out); i++ {
		out[i].fresh = true
	}
	return out
}

// sincePerson is the index of the first event after the person's last message.
func sincePerson(log []protocol.Event) int {
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Type == protocol.EventMessage && protocol.MustData[protocol.MessageData](log[i]).Role == "user" {
			return i + 1
		}
	}
	return 0
}

// mutating names the tools whose identical repetition is a loop: writing the
// same thing again cannot be waiting for anything.
func mutating(tool string) bool { return tool == "write" || tool == "edit" }

// observing reports whether a call looks at something rather than changing it,
// the calls that are checked and watched. wait is waiting already, and a
// resent plan is task.update's own business.
func observing(a attempt) bool {
	return !mutating(a.tool) && !a.changes && a.tool != "wait" && a.tool != "task.update" && !a.refused
}

// explicitWait matches a command that is waiting on purpose.
var explicitWait = regexp.MustCompile(`(^|[\s;&|(])(sleep|watch)\s|--watch\b`)

// touches reports whether a successful change could have altered path.
func (a attempt) touches(path string) bool {
	if !a.changes {
		return false
	}
	if mutating(a.tool) {
		return samePath(a.path, path)
	}
	return true // a mutating command: it could have touched anything
}

// ---------------------------------------------------------------- the three cases

// identical is the state of one change's repetition.
type identical struct {
	runs     int // times it ran with the same result, nothing else touching the file
	refusals int // times this module refused it since that run began
	first    attempt
	last     attempt
}

// identicalRun follows key through atts.
func identicalRun(atts []attempt, key, path string) identical {
	var st identical
	disturbed := false
	for _, a := range atts {
		if a.key != key {
			if st.runs > 0 && a.touches(path) {
				disturbed = true
			}
			continue
		}
		switch {
		case a.refused:
			st.refusals++
		case st.runs > 0 && !disturbed && a.result == st.last.result:
			st.runs++
			st.last = a
		default:
			st = identical{runs: 1, first: a, last: a}
			disturbed = false
		}
	}
	return st
}

// streak is the state of one check's repetition.
type streak struct {
	runs      int      // consecutive identical results
	changes   []string // labels of changes made between them
	lastGap   bool     // a change was made just before the latest run
	anyChange bool
	first     attempt
	last      attempt
}

// checkStreak follows key through atts.
func checkStreak(atts []attempt, key string) streak {
	var st streak
	var gap []string
	for _, a := range atts {
		if a.key != key {
			if a.changes {
				gap = append(gap, a.label)
			}
			continue
		}
		if a.refused {
			continue
		}
		if st.runs > 0 && a.result == st.last.result {
			st.runs++
			st.changes = append(st.changes, gap...)
			st.lastGap = len(gap) > 0
			st.anyChange = st.anyChange || st.lastGap
			st.last = a
		} else {
			st = streak{runs: 1, first: a, last: a}
		}
		gap = nil
	}
	return st
}

// ---------------------------------------------------------------- stalls

// turn is one model reply that called tools, and what came back.
type turn struct {
	opening string   // the first sentence of its thinking, or of its message
	keys    []string // its calls, as attempt keys
	labels  []string
	results []string // status and content, one per call
	at      time.Time
	changed bool // a write or edit in it changed a file
	stale   bool // nothing changed, and every result had come back in an earlier turn
	latest  bool // the model's last reply: what the next request is the first to see
}

// turns returns the model's replies that called tools since the person's
// last message. A reply with no calls is left out: it neither adds to a stall
// nor ends one.
func turns(log []protocol.Event) []turn {
	calls := map[string]protocol.ToolCallData{}
	var all []turn
	thinking := ""
	for _, e := range log[sincePerson(log):] {
		switch e.Type {
		case protocol.EventThinking:
			if d := protocol.MustData[protocol.ThinkingData](e); d.Source == "model" {
				thinking = d.Content
			}
		case protocol.EventMessage:
			text := thinking
			if strings.TrimSpace(text) == "" {
				text = protocol.MustData[protocol.MessageData](e).Content
			}
			all = append(all, turn{opening: firstSentence(text), at: e.Timestamp})
			thinking = ""
		case protocol.EventToolCall:
			if c := protocol.MustData[protocol.ToolCallData](e); c.Source == "model" {
				calls[c.CallID] = *c
			}
		case protocol.EventToolResult:
			r := protocol.MustData[protocol.ToolResultData](e)
			c, ok := calls[r.CallID]
			if !ok || len(all) == 0 {
				continue
			}
			t := &all[len(all)-1]
			t.keys = append(t.keys, c.Tool+"\x00"+canonical(c.Arguments))
			t.labels = append(t.labels, label(c))
			t.results = append(t.results, r.Status+"\x00"+r.Content)
			// An edit that landed changed the files, however alike its
			// result reads: that is a change not landing, not a stall.
			t.changed = t.changed || mutating(c.Tool) && r.Status == "ok" && !strings.HasPrefix(r.Content, "unchanged")
		}
	}
	seen := map[string]bool{}
	var out []turn
	for i, t := range all {
		if len(t.results) == 0 {
			continue
		}
		t.stale = !t.changed
		for _, r := range t.results {
			t.stale = t.stale && seen[r]
		}
		for _, r := range t.results {
			seen[r] = true
		}
		t.latest = i == len(all)-1
		out = append(out, t)
	}
	return out
}

// stall is the state of the model's stale turns.
type stall struct {
	run     []turn // the stale turns in a row up to the latest
	counted bool   // run reached stalledAfter with more than one call in it
	runs    int    // runs counted since the person's last message, this one included
}

func (m *Module) stalled(ts []turn) stall {
	var st stall
	for _, t := range ts {
		if !t.stale {
			st.run, st.counted = nil, false
			continue
		}
		st.run = append(st.run, t)
		if len(st.run) == m.stalledAfter && distinctCalls(st.run) > 1 {
			st.counted = true
			st.runs++
		}
	}
	return st
}

// halts reports why a stall stops the session, or "" if it does not: it went
// on after its notice, or it is the second since the person spoke.
func (m *Module) halts(st stall) string {
	switch {
	case st.runs >= 2:
		return "stalled twice since the person's last message"
	case st.counted && len(st.run) >= m.stalledAfter+m.stalledHaltAfter:
		return fmt.Sprintf("%d turns in a row returned nothing new", len(st.run))
	}
	return ""
}

func distinctCalls(ts []turn) int {
	keys := map[string]bool{}
	for _, t := range ts {
		for _, k := range t.keys {
			keys[k] = true
		}
	}
	return len(keys)
}

// stalledNotice is said once, on the request after the turn that made the run
// long enough. It quotes what the model keeps doing and keeps saying.
func (m *Module) stalledNotice(ts []turn) (string, string) {
	st := m.stalled(ts)
	if !st.counted || len(st.run) != m.stalledAfter || !st.run[len(st.run)-1].latest {
		return "", ""
	}
	first, last := st.run[0], st.run[len(st.run)-1]
	text := fmt.Sprintf("[nabu loop notice] Your last %d turns have returned nothing new: every result was one you had already seen since the person's last message, over %s.",
		len(st.run), since(first.at, last.at))

	// The call made most often, the latest on a tie.
	count := map[string]int{}
	top, topLabel, topResult := "", "", ""
	for _, t := range st.run {
		for i, k := range t.keys {
			count[k]++
			if count[k] >= count[top] {
				top, topLabel, topResult = k, t.labels[i], t.results[i]
			}
		}
	}
	_, content, _ := strings.Cut(topResult, "\x00")
	text += fmt.Sprintf(" `%s` was called %s in them and returned %s each time.", topLabel, plural(count[top], "time", "times"), quote(content))

	openings := map[string]int{}
	said, n := "", 0
	for _, t := range st.run {
		if t.opening == "" {
			continue
		}
		k := normalise(t.opening)
		openings[k]++
		if openings[k] >= n {
			said, n = t.opening, openings[k]
		}
	}
	if n > 1 {
		text += fmt.Sprintf(" You began %d of those turns with %q.", n, said)
	}
	text += " If the request does not fit what you have found, say so and stop, or `ask`."
	if st.runs >= 2 {
		text += " This is the second time since the person's last message; the next call will stop the session so they can look."
	} else {
		text += fmt.Sprintf(" %s like this will stop the session so the person can look.", plural(m.stalledHaltAfter, "more turn", "more turns"))
	}
	return text, fmt.Sprintf("told the model its last %d turns returned nothing new", len(st.run))
}

// firstSentence is the opening of a reply, clipped.
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	for i := 0; i+1 < len(s); i++ {
		if (s[i] == '.' || s[i] == '!' || s[i] == '?') && s[i+1] == ' ' {
			s = s[:i+1]
			break
		}
	}
	return clip(s, 160)
}

// normalise compares openings that differ only in punctuation, spacing or case.
func normalise(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !('a' <= r && r <= 'z' || '0' <= r && r <= '9')
	}), " ")
}

// ---------------------------------------------------------------- hooks

// GateTool implements module.ToolGate. Only an identical change is ever
// refused; a check never is, because a check is how the model finds out
// whether it is done.
func (m *Module) GateTool(_ context.Context, s module.Session, call protocol.ToolCallData) module.Verdict {
	allow := module.Verdict{Decision: module.Allow}
	if !m.enabled || s == nil || call.Source != "model" {
		return allow
	}
	log, err := s.Events(nil)
	if err != nil {
		return allow
	}
	// A stall stops whatever the model does next: the call is not the
	// problem, the turns that return nothing new are.
	if why := m.halts(m.stalled(turns(log))); why != "" {
		return module.Verdict{
			Decision: module.Halt,
			Reason:   refusedPrefix + why + ". The session is stopping so the person can look.",
			Summary:  "loop: " + why,
		}
	}
	if !mutating(call.Tool) {
		return allow
	}
	atts := history(log)
	st := identicalRun(atts, call.Tool+"\x00"+canonical(call.Arguments), argPath(call.Arguments))
	if st.runs < m.identicalAfter {
		return allow
	}
	what := fmt.Sprintf("`%s` has already run %d times since the person's last message with the same arguments and the same result",
		st.last.label, st.runs)
	if st.refusals >= m.haltAfter {
		return module.Verdict{
			Decision: module.Halt,
			Reason: refusedPrefix + what + fmt.Sprintf(", and was refused %d times. The session is stopping so the person can look.",
				st.refusals),
			Summary: fmt.Sprintf("loop: `%s` repeated %d times with the same result, and tried again after %d refusals",
				st.last.label, st.runs, st.refusals),
		}
	}
	reason := refusedPrefix + what + " (" + quote(st.last.content) + "). Running it again cannot change anything, so it was not run."
	if f := lastFailure(atts, ""); f != "" {
		reason += " " + f
	}
	reason += " Do something different: read the file or the error again, or say what you are stuck on and stop."
	if left := m.haltAfter - st.refusals - 1; left == 0 {
		reason += " Trying it again will stop the session."
	} else {
		reason += fmt.Sprintf(" It will be refused %s more, and then trying it again will stop the session.", plural(left, "time", "times"))
	}
	return module.Verdict{Decision: module.Deny, Reason: reason}
}

// BeforeRequest implements module.RequestHook: after a turn whose calls
// repeated, say so at the end of the next request, where it is read last.
// Each block names the call, the count and what changed or did not, so no two
// read alike: a fixed nudge would be one more thing to copy.
func (m *Module) BeforeRequest(_ context.Context, s module.Session) ([]module.ContextBlock, error) {
	if !m.enabled || s == nil {
		return nil, nil
	}
	log, err := s.Events(nil)
	if err != nil {
		return nil, nil
	}
	atts := history(log)
	type said struct{ text, note string }
	var identical, checks []said
	seen := map[string]bool{}
	for i := len(atts) - 1; i >= 0 && atts[i].fresh; i-- {
		a := atts[i]
		if seen[a.key] || a.refused {
			continue
		}
		seen[a.key] = true
		upTo := atts[:i+1]
		switch {
		case mutating(a.tool):
			if text, note := m.identicalNotice(upTo, a); text != "" {
				identical = append(identical, said{text, note})
			}
		case observing(a):
			if text, note := m.checkNotice(upTo, a); text != "" {
				checks = append(checks, said{text, note})
			}
		}
	}
	// A stall is said instead of the checks' notices: a hint about wait is
	// wrong for it. A repeated change is the more specific fact, and said
	// instead of the stall.
	if len(identical) == 0 {
		if text, note := m.stalledNotice(turns(log)); text != "" {
			checks = []said{{text, note}}
		}
	}
	var blocks []module.ContextBlock
	for _, n := range append(identical, checks...) {
		blocks = append(blocks, module.ContextBlock{Slot: "suffix", Content: n.text})
		// Context is for the model; the person's clients show notices.
		s.Append(protocol.EventNotice, protocol.NoticeData{Source: "module:loop", Level: "info", Message: n.note})
	}
	return blocks, nil
}

func (m *Module) identicalNotice(atts []attempt, a attempt) (string, string) {
	st := identicalRun(atts, a.key, a.path)
	if st.runs < m.identicalAfter {
		return "", ""
	}
	text := fmt.Sprintf("[nabu loop notice] `%s` has now run %d times since the person's last message, with the same arguments and the same result each time (%s).",
		a.label, st.runs, quote(a.content))
	if a.path != "" {
		text += fmt.Sprintf(" Nothing else has changed %s in the %s since the first.", a.path, since(st.first.at, a.at))
	}
	if f := lastFailure(atts, a.key); f != "" {
		text += " " + f
	}
	text += " Repeating it cannot change anything, and the next identical call will be refused."
	return text, fmt.Sprintf("told the model `%s` has run %d times with the same result", a.label, st.runs)
}

func (m *Module) checkNotice(atts []attempt, a attempt) (string, string) {
	st := checkStreak(atts, a.key)
	switch {
	case failed(a) && st.lastGap && st.runs >= m.notLandingAfter:
		text := fmt.Sprintf("[nabu loop notice] `%s` has failed with the same output %d times in a row, and the %s made in between (%s) did not change it.",
			a.label, st.runs, plural(len(st.changes), "change", "changes"), listed(st.changes, 6))
		if e := firstError(a.content); e != "" {
			text += fmt.Sprintf(" Its first error is still %q.", e)
		}
		if locs := locations(a.content); len(locs) > 0 {
			text += " It points at " + strings.Join(locs, ", ") + "."
		}
		text += " The changes may not be reaching the cause; look at what the error names before changing more."
		return text, fmt.Sprintf("told the model its last %s did not change the failure of `%s`",
			plural(len(st.changes), "change", "changes"), a.label)
	case !st.anyChange && st.runs >= m.watchAfter && st.runs%m.watchAfter == 0 && !explicitWait.MatchString(a.command):
		text := fmt.Sprintf("[nabu loop notice] `%s` has returned the same output %d times over %s, with nothing changed in between. "+
			"If you are waiting for something to finish, the `wait` tool runs a command until its output changes and returns once. "+
			"If you are not waiting, asking again will not give a different answer.",
			a.label, st.runs, since(st.first.at, a.at))
		return text, fmt.Sprintf("suggested `wait` after `%s` returned the same output %d times", a.label, st.runs)
	}
	return "", ""
}

// ---------------------------------------------------------------- facts

// lastFailure describes the latest failed check other than key, the fact a
// model repeating a change has usually stopped looking at.
//
// Only a check whose latest run failed: one that failed and then passed is
// not what the model is stuck on.
func lastFailure(atts []attempt, key string) string {
	seen := map[string]bool{}
	for i := len(atts) - 1; i >= 0; i-- {
		a := atts[i]
		if a.key == key || !observing(a) || seen[a.key] {
			continue
		}
		seen[a.key] = true
		if !failed(a) {
			continue
		}
		f := fmt.Sprintf("The last failing check was `%s`", a.label)
		if e := firstError(a.content); e != "" {
			f += fmt.Sprintf(", which said %q", e)
		}
		if locs := locations(a.content); len(locs) > 0 {
			f += " (at " + strings.Join(locs, ", ") + ")"
		}
		return f + "."
	}
	return ""
}

// failed reports whether a check failed. Its status alone is not enough: a
// build piped through head exits 0 whatever the build did, so a command's
// output that reads as an error counts too, unless the command only searches
// or shows text, where "error" may be exactly what was looked for.
func failed(a attempt) bool {
	if !a.ok {
		return true
	}
	return a.tool == "bash" && !viewing.MatchString(a.command) && firstError(a.content) != ""
}

// viewing matches a command that searches or shows text rather than checking
// anything, after any leading cd.
var viewing = regexp.MustCompile(`^\s*(cd\s+[^;&|]+(&&|;)\s*)*(grep|rg|cat|head|tail|less|ls|find|echo|git\s+(log|show|diff|grep|status))\b`)

// errorLine matches a line reporting a failure: an error, a panic, FAIL or
// FAILED as test runners print them, or a non-zero count of failures. A
// summary saying "0 failed" is a pass.
var errorLine = regexp.MustCompile(`(?i:\berror\b|\bpanic)|\bFAIL(ED)?\b|\b[1-9]\d* (failed|failures?|errors?)\b`)

// firstError is the first line of output that reads as an error.
func firstError(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if l := strings.TrimSpace(line); l != "" && errorLine.MatchString(l) {
			return clip(l, 160)
		}
	}
	return ""
}

var location = regexp.MustCompile(`[\w./\\-]+\.[A-Za-z]{1,5}:\d+`)

// locations are the first few file:line references in output.
func locations(content string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range location.FindAllString(content, -1) {
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
			if len(out) == 3 {
				break
			}
		}
	}
	return out
}

// ---------------------------------------------------------------- helpers

// canonical re-encodes arguments so key order and spacing do not make two
// identical calls look different.
func canonical(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func argPath(raw json.RawMessage) string { return argString(raw, "path") }

func argString(raw json.RawMessage, key string) string {
	var args map[string]any
	_ = json.Unmarshal(raw, &args)
	v, _ := args[key].(string)
	return v
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.ToSlash(filepath.Clean(a)), filepath.ToSlash(filepath.Clean(b)))
}

// label names a call in a few words: the tool and what it was pointed at.
func label(c protocol.ToolCallData) string {
	var args map[string]any
	_ = json.Unmarshal(c.Arguments, &args)
	if v, ok := args["path"].(string); ok && v != "" {
		// The end of a path is the part that names it.
		if r := []rune(filepath.ToSlash(v)); len(r) > 60 {
			v = "…" + string(r[len(r)-60:])
		}
		return c.Tool + " " + v
	}
	for _, key := range []string{"command", "pattern", "op"} {
		if v, ok := args[key].(string); ok && v != "" {
			if i := strings.IndexAny(v, "\r\n"); i >= 0 {
				v = v[:i] + " …"
			}
			return c.Tool + " " + clip(leadingCd.ReplaceAllString(v, ""), 80)
		}
	}
	return c.Tool
}

// leadingCd is the "cd <workspace> &&" a model puts before most commands; it
// says nothing about which command this is.
var leadingCd = regexp.MustCompile(`^\s*cd\s+[^;&|]+(&&|;)\s*`)

func quote(s string) string { return fmt.Sprintf("%q", clip(strings.TrimSpace(s), 160)) }

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func listed(labels []string, n int) string {
	if len(labels) > n {
		return strings.Join(labels[len(labels)-n:], ", ") + fmt.Sprintf(", and %d earlier", len(labels)-n)
	}
	return strings.Join(labels, ", ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// since is the time between two events, from the log rather than the clock,
// so the same log always gives the same words.
func since(from, to time.Time) string {
	d := to.Sub(from)
	switch {
	case d < time.Minute:
		return plural(int(d.Seconds()), "second", "seconds")
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute", "minutes")
	}
	return fmt.Sprintf("%.1f hours", d.Hours())
}
