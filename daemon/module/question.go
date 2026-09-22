package module

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/corporealshift/nabu/protocol"
)

// AsksOnly reports whether a person's message asks a question and asks for
// nothing else: "what's the status here?", not "can you fix the build?".
//
// It is a heuristic and it leans towards no. A request mistaken for a question
// would have its edits held for approval; a question mistaken for a request is
// only what happened before this existed. So anything phrased as a request —
// "can you", "please", "I'd like you to", a sentence opening on a verb like
// "fix" — makes the whole message a request, even beside a question.
func AsksOnly(text string) bool {
	if !strings.Contains(text, "?") {
		return false
	}
	asked := false
	for _, s := range sentences(text) {
		switch kindOf(s) {
		case sentenceRequest:
			return false
		case sentenceQuestion:
			asked = true
		}
	}
	return asked
}

type sentenceKind int

const (
	sentenceStatement sentenceKind = iota
	sentenceQuestion
	sentenceRequest
)

// sentences splits on sentence ends and line breaks, keeping the terminator so
// a question can be told from a statement.
func sentences(text string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if s := strings.TrimSpace(b.String()); s != "" {
			out = append(out, s)
		}
		b.Reset()
	}
	for _, r := range text {
		if r == '\n' {
			flush()
			continue
		}
		b.WriteRune(r)
		if r == '?' || r == '!' || r == '.' {
			flush()
		}
	}
	flush()
	return out
}

// fillers open a sentence without changing what it is: "ok so why ...".
var fillers = map[string]bool{
	"ok": true, "okay": true, "so": true, "and": true, "but": true, "also": true,
	"hmm": true, "hm": true, "well": true, "hey": true, "right": true, "then": true,
	"now": true, "actually": true, "oh": true, "yeah": true, "yes": true, "no": true,
}

// requestOpenings are the ways people ask for work politely, or indirectly.
var requestOpenings = []string{
	"can you", "could you", "would you", "will you", "can u", "could u",
	"can we", "could we", "please", "pls", "let's", "lets ", "go ahead",
	"i want you", "i'd like you", "i would like you", "i need you", "i'd like to have you",
	"you should", "make sure", "feel free", "we need to", "we should",
}

// imperatives open a sentence that tells the agent to do something.
var imperatives = map[string]bool{
	"add": true, "apply": true, "ask": true, "build": true, "change": true, "check": true,
	"clean": true, "commit": true, "continue": true, "create": true, "delete": true,
	"deploy": true, "do": true, "document": true, "finish": true, "fix": true, "get": true,
	"go": true, "implement": true, "install": true, "investigate": true, "keep": true,
	"look": true, "make": true, "merge": true, "move": true, "open": true, "push": true,
	"put": true, "rebase": true, "refactor": true, "remove": true, "rename": true,
	"replace": true, "resume": true, "revert": true, "review": true, "rewrite": true,
	"run": true, "set": true, "ship": true, "split": true, "start": true, "test": true,
	"try": true, "update": true, "use": true, "write": true,
}

// interrogatives open a question.
var interrogatives = map[string]bool{
	"what": true, "what's": true, "whats": true, "why": true, "how": true, "how's": true,
	"when": true, "where": true, "where's": true, "who": true, "who's": true, "which": true,
	"whose": true, "is": true, "isn't": true, "are": true, "aren't": true, "was": true,
	"wasn't": true, "were": true, "does": true, "doesn't": true, "did": true, "didn't": true,
	"have": true, "has": true, "hasn't": true, "haven't": true, "should": true, "shall": true,
	"would": true, "wouldn't": true, "could": true, "can": true, "will": true, "won't": true,
	"am": true, "any": true, "anything": true,
}

func kindOf(sentence string) sentenceKind {
	s := strings.ToLower(strings.TrimSpace(sentence))
	s = strings.NewReplacer("’", "'", "‘", "'").Replace(s)
	words := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == ',' || r == '\t' || r == '"' || r == '`' || r == '(' || r == ')'
	})
	for len(words) > 0 && fillers[strings.Trim(words[0], ".!?:;-")] {
		words = words[1:]
	}
	if len(words) == 0 {
		return sentenceStatement
	}
	rest := strings.Join(words, " ")
	for _, p := range requestOpenings {
		if strings.HasPrefix(rest, p) {
			return sentenceRequest
		}
	}
	first := strings.Trim(words[0], ".!?:;-")
	if imperatives[first] {
		return sentenceRequest
	}
	if strings.HasSuffix(s, "?") {
		return sentenceQuestion
	}
	if interrogatives[first] {
		// "why did you stop here" with the mark left off is still a question.
		return sentenceQuestion
	}
	return sentenceStatement
}

// ChangesWorkspace reports whether a tool call writes to the workspace or its
// history: the calls that turn answering a question into doing work.
//
// bash is judged by its command, and only the plainly mutating ones count:
// running the tests to say whether they pass is answering, not working.
func ChangesWorkspace(call protocol.ToolCallData) bool {
	switch call.Tool {
	case "edit", "write":
		return true
	case "git":
		var a struct {
			Op string `json:"op"`
		}
		_ = json.Unmarshal(call.Arguments, &a)
		return a.Op == "commit"
	case "bash":
		var a struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(call.Arguments, &a)
		return mutatingCommand.MatchString(harmlessRedirects.ReplaceAllString(a.Command, " "))
	}
	return false
}

// harmlessRedirects write nowhere that matters: merged streams and the null device.
var harmlessRedirects = regexp.MustCompile(`\d*>&\d|\d*>\s*(/dev/null|nul)\b`)

// mutatingCommand matches shell commands that change files or history.
var mutatingCommand = regexp.MustCompile(`(^|[;&|]\s*|\s)(` +
	`git\s+(commit|push|add|rm|mv|checkout|switch|reset|rebase|merge|cherry-pick|stash|tag|apply|am|restore|revert)\b` +
	`|rm\s|mv\s|cp\s|mkdir\s|rmdir\s|touch\s|chmod\s|sed\s+(-[a-zA-Z]*i|--in-place)` +
	`|tee\s|truncate\s|patch\s)` +
	`|[^0-9&|<>]>{1,2}\s*[^&\s|]`)

// QuestionTurn reports whether the person's latest message asks a question and
// nothing else. Never inside a run with a goal: the goal is a standing
// instruction to work, whatever was said last.
func QuestionTurn(log []protocol.Event, st protocol.State) bool {
	if st.GoalActive() {
		return false
	}
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Type != protocol.EventMessage {
			continue
		}
		if d := protocol.MustData[protocol.MessageData](log[i]); d.Role == "user" {
			return AsksOnly(d.Content)
		}
	}
	return false
}

// ChangedSinceUser reports whether a call that changes the workspace has run,
// and succeeded, since the person last said anything.
func ChangedSinceUser(log []protocol.Event) bool {
	succeeded := map[string]bool{}
	for i := len(log) - 1; i >= 0; i-- {
		e := log[i]
		switch e.Type {
		case protocol.EventMessage:
			if protocol.MustData[protocol.MessageData](e).Role == "user" {
				return false
			}
		case protocol.EventToolResult:
			if d := protocol.MustData[protocol.ToolResultData](e); d.Status == "ok" {
				succeeded[d.CallID] = true
			}
		case protocol.EventToolCall:
			d := protocol.MustData[protocol.ToolCallData](e)
			if succeeded[d.CallID] && ChangesWorkspace(*d) {
				return true
			}
		}
	}
	return false
}
