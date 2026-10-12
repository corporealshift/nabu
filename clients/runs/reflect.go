package runs

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/corporealshift/rho/protocol"
)

// The digest's bounds. The whole of it is what one local session can read
// with room to think; each part is cut so one long part cannot push out the
// rest.
const (
	maxDigest      = 60000
	maxDigestBrief = 4000
	maxDigestTasks = 4000
	maxAskQuestion = 400
	maxAskAnswer   = 4000
)

// Digest is how a run went, for its reflection
// (docs/specs/2026-10-11-run-reflection-design.md): how it ended, the brief,
// every claude.ask in its sessions with Claude's answer, the tasks, and each
// step in order. logs are the step sessions' logs, by session.
//
// The asks come before the steps: each is an outside look that got the model
// unstuck, so they are the last thing the bound should cut.
func Digest(r Run, tasks string, logs map[string][]protocol.Event) string {
	titles := map[int]string{}
	for i, t := range ParseTasks(tasks) {
		titles[i] = t.Title
	}
	var b strings.Builder
	b.WriteString("# How the run went\n\n")
	fmt.Fprintf(&b, "Outcome: %s\n\n", runOutcome(r))
	fmt.Fprintf(&b, "## The brief\n\n%s\n\n", cutHead(strings.TrimSpace(r.Brief), maxDigestBrief))
	if asks := askSection(r, logs, titles); asks != "" {
		b.WriteString("## When Claude was asked\n\n" + asks)
	}
	if t := strings.TrimSpace(tasks); t != "" {
		fmt.Fprintf(&b, "## The tasks\n\n%s\n\n", cutHead(t, maxDigestTasks))
	}
	b.WriteString("## Each step, in order\n\n")
	for i, rec := range r.History {
		fmt.Fprintf(&b, "%d. %s\n", i+1, stepLine(rec, logs, titles))
		if note := strings.TrimSpace(rec.Note); note != "" {
			b.WriteString("   > " + strings.ReplaceAll(note, "\n", "\n   > ") + "\n")
		}
	}
	return cutHead(b.String(), maxDigest)
}

func runOutcome(r Run) string {
	switch {
	case r.FailedAt != "":
		return fmt.Sprintf("failed at %s: %s", r.FailedAt, r.Why)
	case r.PRURL != "":
		return "done: " + r.PRURL
	}
	return "done"
}

// stepName is a step as the digest names it: a work step with its task.
func stepName(rec Record, titles map[int]string) string {
	if rec.Step != StepWork {
		return string(rec.Step)
	}
	if t, ok := titles[rec.Task]; ok {
		return fmt.Sprintf("work, task %d (%s)", rec.Task+1, t)
	}
	return fmt.Sprintf("work, task %d", rec.Task+1)
}

func stepLine(rec Record, logs map[string][]protocol.Event, titles map[int]string) string {
	parts := []string{stepName(rec, titles)}
	if rec.Session != "" {
		parts = append(parts, fmt.Sprintf("session %s, %d turns", rec.Session, turns(logs[rec.Session])))
	}
	why := strings.TrimSuffix(rec.Why, " (session "+rec.Session+")")
	switch {
	case !rec.OK && rec.Session != "":
		parts = append(parts, "thrown away: "+why)
	case !rec.OK:
		parts = append(parts, "did not work: "+why)
	case rec.Result != "":
		parts = append(parts, rec.Result)
	default:
		parts = append(parts, "done")
	}
	return strings.Join(parts, ": ")
}

// turns counts the model's replies in a log.
func turns(events []protocol.Event) int {
	n := 0
	for _, e := range events {
		if e.Type != protocol.EventMessage {
			continue
		}
		var m protocol.MessageData
		if json.Unmarshal(e.Data, &m) == nil && m.Role == "assistant" {
			n++
		}
	}
	return n
}

// askSection is every claude.ask in the run's sessions, in order, each with
// who asked, after how many turns, and Claude's answer.
func askSection(r Run, logs map[string][]protocol.Event, titles map[int]string) string {
	type ask struct{ head, question, answer string }
	var asks []ask
	seen := map[string]bool{}
	for _, rec := range r.History {
		if rec.Session == "" || seen[rec.Session] {
			continue
		}
		seen[rec.Session] = true
		byCall := map[string]int{}
		replies := 0
		for _, e := range logs[rec.Session] {
			switch e.Type {
			case protocol.EventMessage:
				var m protocol.MessageData
				if json.Unmarshal(e.Data, &m) == nil && m.Role == "assistant" {
					replies++
				}
			case protocol.EventToolCall:
				var c protocol.ToolCallData
				if json.Unmarshal(e.Data, &c) != nil || c.Tool != "claude.ask" {
					continue
				}
				var args struct {
					Prompt string `json:"prompt"`
				}
				_ = json.Unmarshal(c.Arguments, &args)
				// rho's own ask is the same long framing every time, and the
				// heading says why it asked; the model's question is its own.
				who, question := "the model asked", cutHead(strings.Join(strings.Fields(args.Prompt), " "), maxAskQuestion)
				if strings.HasPrefix(c.Source, "module:") {
					who, question = "rho asked for the model", ""
				}
				byCall[c.CallID] = len(asks)
				asks = append(asks, ask{
					head:     fmt.Sprintf("%s, session %s: %s after %d turns", stepName(rec, titles), rec.Session, who, replies),
					question: question,
				})
			case protocol.EventToolResult:
				var res protocol.ToolResultData
				if json.Unmarshal(e.Data, &res) != nil {
					continue
				}
				if i, ok := byCall[res.CallID]; ok {
					asks[i].answer = cutHead(strings.TrimSpace(res.Content), maxAskAnswer)
				}
			}
		}
	}
	var b strings.Builder
	for _, a := range asks {
		answer := a.answer
		if answer == "" {
			answer = "(no answer)"
		}
		fmt.Fprintf(&b, "### %s\n\n", a.head)
		if a.question != "" {
			fmt.Fprintf(&b, "Question: %s\n\n", a.question)
		}
		fmt.Fprintf(&b, "Claude answered:\n\n%s\n\n", answer)
	}
	return b.String()
}

// cutHead keeps the first n bytes of s, on a character boundary.
func cutHead(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "\n(the rest was cut)"
}

// ReflectPrompt asks the run's last session to save what the run taught.
// What went right comes first, and lessons are written as what to do: a
// small model repeats what fills its context, so a memory that names a
// mistake still puts the mistake in front of it.
func ReflectPrompt(r Run, digest string) string {
	return fmt.Sprintf(`This run has ended. Before it closes, look back at how it went, and save what a future session working in this repository would want to know. The digest below is the whole run: how it ended, the brief, every time Claude was asked for help and what it said, the tasks, and each step in order with how it went.

Save memories with memory.save, scope "workspace". Look first for what worked:
- an approach, command or pattern that got a task done or made the check pass, above all the first time;
- a fix Claude suggested that then worked;
- something about this repository, its build or its tests that a session had to find out the hard way.

Write every memory as what to do, not what to avoid. A lesson from a failure becomes the thing that worked instead: "call waitForIdle() after typing into a field", not "the test fails without waitForIdle()". The sessions that read these are small models, and they tend to repeat whatever is in front of them, mistakes included.

Save at most 5 memories, and only ones a future session would act on. When the memory index already has one on the same subject, save under its name to update it rather than adding another, and use memory.forget for one this run showed to be wrong. Do not save what the repository already records, such as its layout, or what concerns this run alone, such as its task list or its pull request. Saving nothing is a fine answer when the run taught nothing new.

## Rules for this session

- Use only memory.recall, memory.save and memory.forget. Do not change, create or commit any file: the runner puts the worktree of %s back as it was when you finish.
- Do not run commands, search the web, or ask Claude: everything you need is in the digest.
- When you have saved what is worth saving, say in one line what you saved, and end your turn.

## The digest

%s
`, r.Branch, digest)
}
