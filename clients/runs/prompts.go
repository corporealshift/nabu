package runs

import (
	"fmt"
	"strings"
)

// planningOnly is told to every step that writes one of the run's files.
func planningOnly(r Run, name string) string {
	return fmt.Sprintf("\n- Write only %s. Change no other file: the work itself belongs to later steps, and the runner throws away a session that changes anything else.", r.File(name))
}

// rules go at the end of every session's prompt. The runner enforces the
// first one; the others are what an unattended session needs to be told.
func rules(r Run) string {
	return fmt.Sprintf(`
## Rules for this session

- This session does one step of an automated run. Do that step and nothing else; later steps have their own sessions.
- Never create, edit, rename or delete %s. The runner checks, and throws away any session that touches it.
- Do not push, and do not open a pull request. The runner does both at the end.
- Nobody is watching, so do not use the ask tool: a question would only wait ten minutes for an answer that never comes.
- Commit your work before you finish.
`, r.File(VerifyFile))
}

// maxTranscript is how much of the home's conversation a brief is written
// from, taken from the end.
const maxTranscript = 20000

// BriefPrompt asks for brief.md, written from the home's conversation.
func BriefPrompt(r Run, transcript string) string {
	if len(transcript) > maxTranscript {
		transcript = "(earlier conversation cut)\n\n" + transcript[len(transcript)-maxTranscript:]
	}
	return fmt.Sprintf(`Write the brief for an automated run, from the conversation below.

The brief says what is to be built or changed, and what done looks like, in the owner's own terms: what they asked for, what they ruled out, and anything they said about how it should behave. It does not plan how to do it; a later step does that. Keep it short, and do not add requirements the owner did not state.

Write it to %s and commit it with the message "run: brief".

## The conversation

%s
%s%s`, r.File(BriefFile), transcript, rules(r), planningOnly(r, BriefFile))
}

// PlanPrompt asks for plan.md.
func PlanPrompt(r Run) string {
	return fmt.Sprintf(`Read the brief in %s, then plan the work.

Read as much of the repository as you need. Write the plan to %s. It says:
- the approach, and why it fits this codebase;
- the files and parts of the code involved;
- the order the work should go in;
- how the result will be tested;
- anything risky or uncertain.

Plan exactly what the brief asks for, and nothing more. Commit it with the message "run: plan".
%s%s`, r.File(BriefFile), r.File(PlanFile), rules(r), planningOnly(r, PlanFile))
}

// TasksPrompt asks for tasks.md.
func TasksPrompt(r Run) string {
	return fmt.Sprintf(`Read the brief in %s and the plan in %s, then break the plan into tasks.

Write %s as a checklist of 3 to 8 tasks that together carry out the plan. Each task is a coherent chunk of work that ends in a commit: bigger than a single edit, much smaller than the whole plan. A separate session does each task, reading only the brief, the plan and its own entry, so each must stand on its own.

Write each task as a checkbox line, with indented lines under it saying what it covers and how you will know it is done:

- [ ] Add the cache type
  A read-through cache in reader/cache.go with get and put, and unit tests for both.

Commit it with the message "run: tasks".
%s%s`, r.File(BriefFile), r.File(PlanFile), r.File(TasksFile), rules(r), planningOnly(r, TasksFile))
}

// VerifyPrompt asks for verify.sh. It is the one session allowed to write it.
func VerifyPrompt(r Run) string {
	return fmt.Sprintf(`Read the brief in %[1]s, the plan in %[2]s and the tasks in %[3]s, then write the check that proves the brief is done.

Write %[4]s: a bash script that exits 0 only when the brief is done, and non-zero otherwise. It runs from the repository root with bash, on Windows under Git Bash. It should:
- build what the brief changes;
- run the tests that prove the brief, including the ones the tasks will add, by name where you can;
- check anything else the brief requires.

Use "set -euo pipefail". It must fail now, before the work is done, unless the brief is already met: a check that passes on untouched code proves nothing.

This script is the definition of done for the whole run. Once it is committed, only the runner may change it. Commit it with the message "run: verify".

## Rules for this session

- This session does one step of an automated run. Write the script and nothing else.
- Write only %[4]s. Change no other file: do not write the feature or its tests, which belong to later steps, and the runner throws away a session that changes anything else.
- Do not push, and do not open a pull request.
- Nobody is watching, so do not use the ask tool.
- Commit your work before you finish.
`, r.File(BriefFile), r.File(PlanFile), r.File(TasksFile), r.File(VerifyFile))
}

// WorkPrompt asks for one task, and gives the goal it is judged against.
func WorkPrompt(r Run, i, n int, t Task) (prompt, goal string) {
	detail := strings.TrimSpace(t.Detail)
	if detail != "" {
		detail = "\n\n" + detail
	}
	prompt = fmt.Sprintf(`Do task %d of %d of this run:

**%s**%s

For context, the brief is %s, the plan is %s and the whole task list is %s. Do this task and only this task: later tasks belong to later sessions. Build and test what you change. Commit with a message that says what the task did. Do not edit %s; the runner ticks the box when you finish.
%s`, i+1, n, t.Title, detail, r.File(BriefFile), r.File(PlanFile), r.File(TasksFile), r.File(TasksFile), rules(r))
	goal = fmt.Sprintf("Task %d of %s (%q) is done and committed, and %s is untouched.", i+1, r.File(TasksFile), t.Title, r.File(VerifyFile))
	return prompt, goal
}

// maxOutput is how much of verify.sh's output a prompt carries, from the end,
// where a failure explains itself.
const maxOutput = 20000

func tail(s string) string {
	if len(s) > maxOutput {
		return "(earlier output cut)\n" + s[len(s)-maxOutput:]
	}
	return s
}

// FixPrompt asks for a failing check to be fixed. advice is what Claude said
// when it last refused to change the check.
func FixPrompt(r Run, output, advice string) (prompt, goal string) {
	var extra string
	if strings.TrimSpace(advice) != "" {
		extra = "\n\nA reviewer looked at the check and says it is right. Their note:\n\n" + strings.TrimSpace(advice) + "\n"
	}
	prompt = fmt.Sprintf(`The run's check, %[1]s, fails. This is the end of its output:

~~~
%[2]s
~~~
%[3]s
Find the cause and fix it, then commit. The brief is %[4]s and the plan is %[5]s, if you need them. Run %[1]s yourself with bash to see the fix work.

If you are sure the check itself is wrong, and not the work, do not touch %[1]s. Write %[6]s instead, saying exactly what is wrong with the check and what it should do, commit it, and stop. A reviewer then decides.
%[7]s`, r.File(VerifyFile), strings.TrimSpace(tail(output)), extra, r.File(BriefFile), r.File(PlanFile), r.File(RevisionFile), rules(r))
	goal = fmt.Sprintf("The failure of %s shown in the first message is fixed and committed, or %s explains why the check is wrong; %s is untouched.",
		r.File(VerifyFile), r.File(RevisionFile), r.File(VerifyFile))
	return prompt, goal
}

// reviewer opens every prompt Claude gets.
const reviewer = "You are reviewing one step of an automated coding run in this repository. You can read files; you cannot change them or run commands. The runner acts on your answer, so answer in exactly the form asked for.\n\n"

// PlanReviewPrompt asks Claude to review the plan, once.
func PlanReviewPrompt(r Run) string {
	return reviewer + fmt.Sprintf(`The brief is %s. The plan, written by a smaller model, is %s.

Check that the plan does everything the brief asks, and nothing it does not, and that it will work in this codebase: read the code it names.

If the plan needs changes, reply with the complete revised plan between these two lines:

%s
%s

If it is good as it is, reply with exactly: NO CHANGES`, r.File(BriefFile), r.File(PlanFile), Begin(PlanFile), End(PlanFile))
}

// VerifyReviewPrompt asks Claude to review verify.sh, which it may rewrite.
func VerifyReviewPrompt(r Run, passedBefore bool, output string) string {
	before := "fails"
	if passedBefore {
		before = "PASSES, which proves nothing unless the brief is already met"
	}
	return reviewer + fmt.Sprintf(`The brief is %[1]s, the plan %[2]s and the tasks %[3]s. The check, written by a smaller model, is %[4]s.

That script defines done for the whole run. The model doing the work may not change it, and the run is finished when it exits 0. Run now, before any work, it %[5]s. The end of its output:

~~~
%[6]s
~~~

Check that it proves the brief is done:
- it builds, and runs tests that would fail if the brief were not met;
- it cannot pass trivially or be satisfied by a stub;
- it runs from the repository root under bash, on Windows with Git Bash.

If it falls short, reply with a complete replacement between these two lines:

%[7]s
%[8]s

If it is right, reply with exactly: APPROVED`, r.File(BriefFile), r.File(PlanFile), r.File(TasksFile), r.File(VerifyFile), before,
		strings.TrimSpace(tail(output)), Begin(VerifyFile), End(VerifyFile))
}

// RevisePrompt asks Claude to judge a fix session's claim that the check is
// wrong.
func RevisePrompt(r Run, output string) string {
	return reviewer + fmt.Sprintf(`The check for this run is %[1]s, and it fails. The session fixing it says the check itself is wrong; its reason is in %[2]s. The brief is %[3]s. The end of the check's output:

~~~
%[4]s
~~~

Decide who is right. If the check is wrong, reply with the corrected, complete script between these two lines:

%[5]s
%[6]s

Keep it as strict as the brief requires. Do not weaken it to let broken work pass.

If the check is right and the work is wrong, reply with REFUSED, followed by one paragraph telling the next fix session what is actually wrong.`,
		r.File(VerifyFile), r.File(RevisionFile), r.File(BriefFile), strings.TrimSpace(tail(output)), Begin(VerifyFile), End(VerifyFile))
}

// FinalReviewPrompt asks Claude for blockers in the finished work, once.
func FinalReviewPrompt(r Run, changed []string) string {
	return reviewer + fmt.Sprintf(`The run is finished and its check, %[1]s, passes. Review the work against the brief, %[2]s, before it becomes a pull request. The plan is %[3]s. The branch changed these files since %[4]s:

%[5]s

Report only blockers. A blocker is one of:
- the work does not do what the brief asks;
- it has a bug;
- %[1]s does not actually prove the brief is done.

Style, naming, refactoring, suggestions, anything starting "consider", and anything the brief did not ask for are NOT blockers. Leave them out, or put them in notes, which go in the pull request description and change nothing. Every blocker becomes a task the run must do, so do not report one you would not insist on.

Reply with one fenced json block:

`+"```json"+`
{"blockers": [{"title": "what must change", "detail": "where, and why it blocks"}], "notes": ["anything worth mentioning that is not a blocker"]}
`+"```"+`

For good work the right answer has no blockers.`, r.File(VerifyFile), r.File(BriefFile), r.File(PlanFile), r.Base, "- "+strings.Join(changed, "\n- "))
}
