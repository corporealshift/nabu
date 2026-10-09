package runs

import (
	"fmt"
	"strings"
)

// expectation is how every step that settles an open behavior settles it
// (docs/specs/2026-10-04-run-decisions-design.md). In a live run the plan
// review chose the literal reading of a brief over what callers would expect,
// and the run failed on exactly that.
const expectation = "When the brief leaves a behavior open, choose what the people who use this would expect, not the narrowest reading of the brief's words."

// DecisionsHeading heads the plan's section of open behaviors and what was
// chosen for each. The runner copies the section into the pull request.
const DecisionsHeading = "## Decisions"

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
- Nobody is watching, so there is no one to ask. If you are stuck on something, look it up first: web.search finds documentation and answers, and web.fetch reads a page. If you have tried that and still cannot settle it, ask Claude with claude.ask, saying what you tried and what you are choosing between.
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
- anything risky or uncertain;
- the decisions, in a section headed exactly "%s" at the end.

The decisions section has one entry for each behavior the brief leaves open that someone using the result could notice. Each entry gives the question, what the plan chooses, the alternative, and why the choice is what users would expect. Choices about how the code is written do not go there. If the brief settles every behavior the plan touches, say so in one line under the heading. The owner reads this section in the pull request, and changes a decision there if they disagree.

%s

Plan exactly what the brief asks for, and nothing more. Commit it with the message "run: plan".
%s%s`, r.File(BriefFile), r.File(PlanFile), DecisionsHeading, expectation, rules(r), planningOnly(r, PlanFile))
}

// TasksPrompt asks for tasks.md. The check is written first, so the tasks can
// carry every test it names: a test no task was given was left to the fix
// session, which in one run spent six hours writing six of them from nothing
// (docs/specs/2026-10-09-verify-before-tasks-design.md).
func TasksPrompt(r Run) string {
	return fmt.Sprintf(`Read the brief in %[1]s and the plan in %[2]s, then break the plan into tasks.

Write %[3]s as a checklist of 3 to 8 tasks that together carry out the plan. Each task is a coherent chunk of work that ends in a commit: bigger than a single edit, much smaller than the whole plan. A separate session does each task, reading only the brief, the plan and its own entry, so each must stand on its own.

Read the check, %[4]s, too. It is what defines done for the run, and the tests it names are fixed. Every test it names that does not exist yet must be written by exactly one task, and that task's entry names it, under the exact name %[4]s uses, so the session doing the task writes it. A test no task names is left to nobody. Tests that already exist need no task.

Write each task as a checkbox line, with indented lines under it saying what it covers and how you will know it is done:

- [ ] Add the cache type
  A read-through cache in reader/cache.go with get and put. Tests: CacheTest.getReturnsWhatPutStored, CacheTest.getMissesAnUnknownKey.

Commit it with the message "run: tasks".
%[5]s%[6]s`, r.File(BriefFile), r.File(PlanFile), r.File(TasksFile), r.File(VerifyFile), rules(r), planningOnly(r, TasksFile))
}

// VerifyPrompt asks for verify.sh. It is the one session allowed to write it.
// It comes before the tasks, which are then written to carry its tests.
func VerifyPrompt(r Run) string {
	return fmt.Sprintf(`Read the brief in %[1]s and the plan in %[2]s, then write the check that proves the brief is done.

Write %[3]s: a bash script that exits 0 only when the brief is done, and non-zero otherwise. It runs from the repository root with bash, on Windows under Git Bash. Write it the way CI is written:
- build, and run what the repository's CI runs: read its CI configuration, such as .github/workflows, if it has one;
- run, by name, the tests that prove what the brief asks for: roughly one for each thing it asks for, not one for each detail of how it is built. Fail if any of them fails or did not run at all. Most test runners pass when a name matches no test, so check their output for each named test passing. Above each one, say in a comment what that test must show: the tasks are written from this script, and the session that writes the test reads that comment;
- never require a kind of test the brief or the plan rules out or says is not needed: if the brief says UI tests are not required, name none;
- name the cheapest test that shows the behavior: a test of the logic over a test driven through the UI, unless the brief asks for the UI test;
- where a named test also shows a decision from the plan's "%[4]s" section, name the decision in the comment above it. Do not add a test only to pin a decision: the owner reviews the decisions in the pull request;
- check anything else the brief requires by running it, as a user or a test would.

%[5]s

Check behavior, never the text of the code. Do not grep source files for function names, strings or patterns, count tests, or check that files exist: those checks dictate how the work is written, and fail correct work that is written differently. A test of the new behavior is what proves it.

Use "set -euo pipefail". It must fail now, before the work is done, unless the brief is already met: a check that passes on untouched code proves nothing. The named tests, which do not exist yet, are what make it fail.

This script is the definition of done for the whole run. Once it is committed, only the runner may change it. Commit it with the message "run: verify".

## Rules for this session

- This session does one step of an automated run. Write the script and nothing else.
- Write only %[3]s. Change no other file: do not write the feature or its tests, which belong to later steps, and the runner throws away a session that changes anything else.
- Do not push, and do not open a pull request.
- Nobody is watching, so there is no one to ask. If you are stuck on something, look it up first: web.search finds documentation and answers, and web.fetch reads a page. If you have tried that and still cannot settle it, ask Claude with claude.ask, saying what you tried and what you are choosing between.
- Commit your work before you finish.
`, r.File(BriefFile), r.File(PlanFile), r.File(VerifyFile), DecisionsHeading, expectation)
}

// WorkPrompt asks for one task, and gives the goal it is judged against.
func WorkPrompt(r Run, i, n int, t Task) (prompt, goal string) {
	detail := strings.TrimSpace(t.Detail)
	if detail != "" {
		detail = "\n\n" + detail
	}
	prompt = fmt.Sprintf(`Do task %d of %d of this run:

**%s**%s

For context, the brief is %s, the plan is %s and the whole task list is %s. Do this task and only this task: later tasks belong to later sessions. The run is done when %s passes, so read it. This task's entry names the tests it writes: write each under exactly that name, and make it show what the comment above it in the check says. Build and test what you change. Commit with a message that says what the task did. Do not edit %s; the runner ticks the box when you finish.
%s`, i+1, n, t.Title, detail, r.File(BriefFile), r.File(PlanFile), r.File(TasksFile), r.File(VerifyFile), r.File(TasksFile), rules(r))
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
	return reviewer + fmt.Sprintf(`The brief is %[1]s. The plan, written by a smaller model, is %[2]s.

Check that the plan does everything the brief asks, and nothing it does not, and that it will work in this codebase: read the code it names.

%[5]s

The plan ends with a "%[3]s" section: the behaviors the brief leaves open, and what the plan chose for each. The owner reads it in the pull request, so a disagreement must stay visible there, never be quietly resolved:
- keep every entry;
- to change a decision, leave its entry, add a line "Changed by review: the plan chose X; the review chose Y, because Z.", and move the entry to the top of the section;
- to add a decision the plan missed, mark the entry "Added by review";
- if the plan has no such section, add one.

If the plan needs changes, reply with the complete revised plan between these two lines:

%[4]s
%[6]s

If it is good as it is, reply with exactly: NO CHANGES`, r.File(BriefFile), r.File(PlanFile), DecisionsHeading, Begin(PlanFile), expectation, End(PlanFile))
}

// VerifyReviewPrompt asks Claude to review verify.sh, which it may rewrite.
func VerifyReviewPrompt(r Run, passedBefore bool, output string) string {
	before := "fails"
	if passedBefore {
		before = "PASSES, which proves nothing unless the brief is already met. If the brief is not met, make the script fail by running tests of the new behavior by name, not by checking the source"
	}
	return reviewer + fmt.Sprintf(`The brief is %[1]s and the plan %[2]s. The check, written by a smaller model, is %[3]s.

That script defines done for the whole run. The model doing the work may not change it, and the run is finished when it exits 0. The tasks are written after this review, from the script, and each test it names is given to one of them. Run now, before any work, it %[4]s. The end of its output:

~~~
%[5]s
~~~

Check that it proves the brief is done, the way CI would, and asks for no more than the brief does:
- it builds, and runs what the repository's CI runs;
- it runs, by name, tests of the new behavior, roughly one for each thing the brief asks for, and fails if any of them fails or did not run;
- it requires no kind of test the brief or the plan rules out or says is not needed, and names a test of the logic rather than one driven through the UI unless the brief asks for the UI test. Take out any named test that goes beyond the brief;
- a decision in the plan's "%[8]s" section needs no test of its own: the owner reviews the decisions in the pull request. Where a named test shows one, the comment above it names the decision;
- it checks behavior only. It does not grep source files for names, strings or patterns, count tests, or check that files exist: those dictate how the work is written and fail correct work written differently. Take out any such check, and do not add one;
- it cannot pass on the code as it is now;
- it runs from the repository root under bash, on Windows with Git Bash.

If it falls short, reply with a complete replacement between these two lines:

%[6]s
%[7]s

%[9]s

If it is right, reply with exactly: APPROVED`, r.File(BriefFile), r.File(PlanFile), r.File(VerifyFile), before,
		strings.TrimSpace(tail(output)), Begin(VerifyFile), End(VerifyFile), DecisionsHeading, expectation)
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

Keep it as strict as the brief requires, and no stricter: a test of a kind the brief rules out, or one for something it never asked, is the check's fault, not the work's. Do not weaken it to let broken work pass. Keep it a check of behavior, as CI is: run tests and commands, and do not add checks on the text of the source. %[7]s

If the check is right and the work is wrong, reply with REFUSED, followed by one paragraph telling the next fix session what is actually wrong.`,
		r.File(VerifyFile), r.File(RevisionFile), r.File(BriefFile), strings.TrimSpace(tail(output)), Begin(VerifyFile), End(VerifyFile), expectation)
}

// FinalReviewPrompt asks Claude for blockers in the finished work, once.
func FinalReviewPrompt(r Run, changed []string) string {
	return reviewer + fmt.Sprintf(`The run is finished and its check, %[1]s, passes. Review the work against the brief, %[2]s, before it becomes a pull request. The plan is %[3]s. The branch changed these files since %[4]s:

%[5]s

Report only blockers. A blocker is one of:
- the work does not do what the brief asks;
- it has a bug.

Style, naming, refactoring, suggestions, anything starting "consider", and anything the brief did not ask for are NOT blockers. Leave them out, or put them in notes, which go in the pull request description and change nothing. Every blocker becomes a task the run must do, so do not report one you would not insist on.

That task's session may not change %[1]s. Never ask for a change to %[1]s: say what the code must do, and where a test would show it, in the project's own tests. If %[1]s misses a bug, the bug is the blocker.

%[6]s The plan's "%[7]s" section records what the run chose for each behavior the brief left open. If you think one goes against what users would expect, say so in notes, not as a blocker: the decision is the owner's, made from the pull request.

Reply with one fenced json block:

`+"```json"+`
{"blockers": [{"title": "what must change", "detail": "where, and why it blocks"}], "notes": ["anything worth mentioning that is not a blocker"]}
`+"```"+`

For good work the right answer has no blockers.`, r.File(VerifyFile), r.File(BriefFile), r.File(PlanFile), r.Base, "- "+strings.Join(changed, "\n- "), expectation, DecisionsHeading)
}

// CIFixPrompt asks for the PR's failing checks to be fixed. failures is each
// failed check with the end of its log.
func CIFixPrompt(r Run, failures string) (prompt, goal string) {
	prompt = fmt.Sprintf(`The run's pull request is open, and its CI fails. These are the failed checks, with the end of each log:

%[1]s

Find the cause and fix it, then commit. The brief is %[2]s and the plan is %[3]s, if you need them. CI may check things %[4]s does not, such as formatting, another platform or a linter; fix what it reports. Then run %[4]s yourself with bash: a CI fix that breaks the brief is no fix.

If you are sure the failure shows %[4]s is wrong, do not touch it. Write %[5]s instead, saying exactly what is wrong with the check, commit it, and stop.
%[6]s`, strings.TrimSpace(tail(failures)), r.File(BriefFile), r.File(PlanFile), r.File(VerifyFile), r.File(RevisionFile), rules(r))
	goal = fmt.Sprintf("The CI failures listed in the first message are fixed and committed, %s still passes, and %s is untouched.",
		r.File(VerifyFile), r.File(VerifyFile))
	return prompt, goal
}
