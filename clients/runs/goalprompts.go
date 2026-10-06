package runs

import (
	"fmt"
	"strings"

	"github.com/corporealshift/nabu/clients/github"
	"github.com/corporealshift/nabu/protocol"
)

// planner is what Claude is told it is doing for a goal.
const planner = "You are planning and judging a goal for an automated coding system in this repository. You can read files; you cannot change them or run commands. The runner acts on your answer, so answer in exactly the form asked for.\n\n"

// briefRules is what makes a brief one a run can take on its own.
const briefRules = `Each brief becomes one automated run: a smaller model plans it, writes tasks and a check, does the work in a fresh branch, and opens a pull request. The run sees only its brief and the repository, never the goal or the other briefs. So each brief must:
- say what to build or change, how it will be used, and what done looks like, in plain terms;
- say what it must not break, and name anything earlier briefs added that it builds on;
- be small enough for one pull request, and leave the repository working when it is merged;
- not say how to write the code, beyond what the codebase requires.

When the goal leaves a behavior open, choose what the people who use this would expect, not the narrowest reading of the goal's words.`

// BreakdownPrompt asks Claude to break a goal into its first briefs.
func BreakdownPrompt(g Goal) string {
	return planner + fmt.Sprintf(`The goal, from the owner:

~~~
%[1]s
~~~

Read as much of the repository as you need to understand where the goal starts from. Then break it into briefs, to be done one after another, each building on the ones before.

%[2]s

Write between 2 and 6 briefs, in the order they must be done. If the goal needs more, write the first ones: the goal is checked when they are done, and more briefs follow.

Also write the done-when list: a few outcomes, each one a person could check, that together mean the goal is met. They are judged by reading the code at the end, not run as a script.

Reply with one fenced json block:

`+"```json"+`
{"done_when": ["an outcome a person could check"], "briefs": [{"title": "short title", "brief": "the brief, in full"}]}
`+"```", strings.TrimSpace(g.Text), briefRules)
}

// CheckPrompt asks Claude whether the goal is met, and if not, for the next
// round's briefs. changed is every file the goal's branch has changed.
func CheckPrompt(g Goal, changed []string) string {
	var b strings.Builder
	b.WriteString(planner)
	fmt.Fprintf(&b, "The goal, from the owner:\n\n~~~\n%s\n~~~\n\n", strings.TrimSpace(g.Text))
	if len(g.DoneWhen) > 0 {
		b.WriteString("It is met when:\n")
		for _, d := range g.DoneWhen {
			fmt.Fprintf(&b, "- %s\n", d)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "This worktree is the goal's branch, %s, with every finished run merged into it. ", g.Branch)
	if len(changed) > 0 {
		fmt.Fprintf(&b, "Since %s it has changed:\n\n- %s\n\n", g.Base, strings.Join(changed, "\n- "))
	} else {
		fmt.Fprintf(&b, "It has changed nothing since %s.\n\n", g.Base)
	}
	b.WriteString("## What has been done\n\n")
	b.WriteString(history(g))
	if g.CIFailure != "" {
		fmt.Fprintf(&b, "## The goal's pull request fails CI\n\n%s\n\n", strings.TrimSpace(tail(g.CIFailure)))
	}
	fmt.Fprintf(&b, `## What to do

Read the code, and judge whether the goal is met: every done-when outcome holds, and the goal as the owner wrote it is done. A run that finished did what its brief said; check it did what the goal needed.

If it is met, say so. If it is not, write the briefs for the next round: only what is still missing or wrong. Do not repeat work that is done.%s

%s

Reply with one fenced json block, either:

`+"```json"+`
{"met": true, "reason": "why the goal is met"}
`+"```"+`

or:

`+"```json"+`
{"met": false, "reason": "what is missing", "briefs": [{"title": "short title", "brief": "the brief, in full"}]}
`+"```", failedNote(g), briefRules)
	return b.String()
}

// failedNote is said when the round ended on a failed run.
func failedNote(g Goal) string {
	if g.Failures == 0 {
		return ""
	}
	return " The last run failed. Do not give the same brief again: find what went wrong, and write a brief that goes around it, smaller or by another way. If one more run fails in a row, the goal stops and waits for the owner."
}

// history is every round so far: each brief and how its run ended, and each
// verdict.
func history(g Goal) string {
	var b strings.Builder
	for round := 1; round <= g.Round; round++ {
		briefs := g.InRound(round)
		if len(briefs) == 0 {
			continue
		}
		fmt.Fprintf(&b, "### Round %d\n\n", round)
		for _, br := range briefs {
			fmt.Fprintf(&b, "- **%s**: %s\n", br.Title, outcome(br))
			for _, n := range br.Notes {
				fmt.Fprintf(&b, "  - review note: %s\n", strings.TrimSpace(n))
			}
		}
		for _, v := range g.Verdicts {
			if v.Round == round {
				fmt.Fprintf(&b, "\nThe check said it was not met: %s\n", v.Reason)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// outcome is how one brief's run went, in a few words.
func outcome(br GoalBrief) string {
	switch br.State {
	case BriefDone:
		if br.PRURL != "" {
			return "done, merged from " + br.PRURL
		}
		return "done"
	case BriefFailed:
		return fmt.Sprintf("the run failed at its %s step: %s", br.FailedAt, br.Why)
	case BriefDropped:
		return "not started, because the run before it failed"
	case BriefRunning:
		return "running"
	}
	return "not started"
}

// Roadmap is roadmap.md: the goal, what done means, and every round.
func Roadmap(g Goal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Goal\n\n%s\n\n", strings.TrimSpace(g.Text))
	if len(g.DoneWhen) > 0 {
		b.WriteString("## Done when\n\n")
		for _, d := range g.DoneWhen {
			fmt.Fprintf(&b, "- %s\n", d)
		}
		b.WriteString("\n")
	}
	for round := 1; round <= g.Round; round++ {
		briefs := g.InRound(round)
		if len(briefs) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## Round %d\n\n", round)
		for i, br := range briefs {
			fmt.Fprintf(&b, "### %d. %s\n\n%s\n\n", i+1, br.Title, strings.TrimSpace(br.Brief))
		}
		for _, v := range g.Verdicts {
			if v.Round == round {
				met := "not met"
				if v.Met {
					met = "met"
				}
				fmt.Fprintf(&b, "**Check:** %s. %s\n\n", met, v.Reason)
			}
		}
	}
	return b.String()
}

// GoalTasks is the goal home's task list: the roadmap as Kyle sees it on
// his phone. Every brief, every check, and why the goal is blocked.
func GoalTasks(g Goal) []protocol.Task {
	var out []protocol.Task
	for i, br := range g.Briefs {
		t := protocol.Task{ID: fmt.Sprintf("b%d", i+1), Title: fmt.Sprintf("Round %d: %s", br.Round, br.Title), BlockedBy: []string{}}
		switch br.State {
		case BriefDone:
			t.Status, t.Note = protocol.TaskDone, br.PRURL
		case BriefRunning:
			t.Status = protocol.TaskInProgress
		case BriefFailed:
			t.Status, t.Note = protocol.TaskFailed, fmt.Sprintf("failed at %s: %s", br.FailedAt, br.Why)
		case BriefDropped:
			t.Status = protocol.TaskCancelled
		default:
			t.Status = protocol.TaskPending
		}
		out = append(out, t)
	}
	for _, v := range g.Verdicts {
		t := protocol.Task{ID: fmt.Sprintf("c%d", v.Round), Title: fmt.Sprintf("Round %d check", v.Round), Status: protocol.TaskDone,
			Note: "not met: " + v.Reason, BlockedBy: []string{}}
		if v.Met {
			t.Note = "met: " + v.Reason
		}
		out = append(out, t)
	}
	if g.Step == GoalBlocked {
		out = append(out, protocol.Task{ID: "blocked", Title: "Blocked at " + string(g.BlockedAt), Status: protocol.TaskBlocked,
			Note: g.Why, BlockedBy: []string{}})
	}
	if g.PRURL != "" {
		t := protocol.Task{ID: "pr", Title: "Pull request", Status: protocol.TaskInProgress, Note: g.PRURL, BlockedBy: []string{}}
		if g.Step == GoalDone {
			t.Status = protocol.TaskDone
		}
		out = append(out, t)
	}
	return out
}

// GoalPRBody is the goal's pull request description.
func GoalPRBody(g Goal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s opened this from a goal, worked as %d runs.\n\n## Goal\n\n%s\n\n", github.Signature, countRuns(g), strings.TrimSpace(g.Text))
	if len(g.DoneWhen) > 0 {
		b.WriteString("## Done when\n\n")
		for _, d := range g.DoneWhen {
			fmt.Fprintf(&b, "- %s\n", d)
		}
		b.WriteString("\n")
	}
	if n := len(g.Verdicts); n > 0 && g.Verdicts[n-1].Met {
		fmt.Fprintf(&b, "## Claude's verdict\n\n%s\n\n", g.Verdicts[n-1].Reason)
	}
	b.WriteString("## Runs\n\n")
	for _, br := range g.Briefs {
		fmt.Fprintf(&b, "- Round %d, %s: %s\n", br.Round, br.Title, outcome(br))
	}
	fmt.Fprintf(&b, "\nThe roadmap is `%s`.\n\n%s\n", g.File(RoadmapFile), github.Marker)
	return b.String()
}

// countRuns is how many of the goal's briefs were run.
func countRuns(g Goal) int {
	n := 0
	for _, br := range g.Briefs {
		if br.Run != "" {
			n++
		}
	}
	return n
}
