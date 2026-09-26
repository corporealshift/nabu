# One reminder at the stop, when Kyle is watching

**Date:** 2026-09-26
**Status:** Agreed with Kyle in conversation
**Changes:** architecture spec §10.2–10.4 for sessions without a goal. Sessions with a
goal keep them as written.

## Problem

Across the logs, the `verify` stop gate objected 90 times in 12 sessions, and five
sessions ended blocked with an objection still standing. No session had a goal set, so
every one of those objections reached a model working with Kyle watching.

- **Objections pushed the model into more work than was asked for.** In `01M3DQZR…`
  Kyle said "don't add extra things. just hello world is fine for now". Three
  objections later (tasks not marked done, uncommitted files twice), the model had
  lost that instruction. It attributed its own earlier plan to Kyle, and started on
  a data layer he had just declined.
- **Objections read as the person speaking.** They reach the model as one user-role
  message (§6.4), and its thinking says "the user wants me to…" to each one.
- **Objections that repeat block the session.** The model explained build files 14
  times in `01M39VB8…`. In `01M3DPQ6…`, a gate failing on code the session never
  touched blocked a request for a README.
- **The task list is a poor signal.** Tasks are left open after the work is done, and
  an open task set off the first objection in `01M3DQZR…`.

When Kyle is watching, stopping too early costs one "continue". Carrying on too far
costs unrequested work, loops and blocked sessions. Unattended, it is the other way
round, and there a goal is set.

## What Kyle wants at the end of a turn

When a turn has done work, the work is committed and there is a pull request. Unfinished
work is not pushed, and the model says what is left. Whether work is finished is the
model's call: nabu cannot tell, and the task list is not reliable enough to decide it.

## Decision

**Without an active goal**, `verify` gives each turn at most one reminder, and then lets
the model stop.

It fires only when the turn changed the workspace (`module.ChangedSinceUser`), and only
if at least one of these holds:

- files the session changed are not committed (split as in the tree veto, with build
  output summarised);
- the session committed onto `main` or `master`;
- the session committed, and the branch has no pull request (`gh pr view`; if `gh`
  cannot say, nothing is claimed);
- the session's commits added build output;
- tasks are still open or blocked without a note;
- a task's check last failed;
- the project gate is configured and fails.

The reminder says it is nabu's, not the person's. It quotes the person's last message
and says to stay within it. Then it gives the facts, and asks: if the work is finished,
mark finished tasks done, commit on a branch and open a pull request; if it is not, do
not push, and say what is left. It ends by saying the model may stop after answering.

Whether this turn has had its reminder is read from the log: a `verify` veto after the
person's last message. So a restart does not repeat it.

**With an active goal**, §10.2–10.4 stand unchanged: repeated vetoes, and the judge.

**Unchanged in both modes:**
- A task's `check` runs when the task is marked done, and a failure refuses it.
- A question that was answered is a finished turn.
- Interrupting runs no checks at all.

## Rejected

- **No checks at all when Kyle is watching.** The tree veto did get work committed that
  would otherwise have been left on disk. One reminder keeps that and costs one turn.
- **Deciding "unfinished" from open tasks.** Tasks are left open when the work is done.
  They go in the reminder as a list to bring up to date, not as a verdict.
- **Rewording the §6.4 veto template first.** That is a protocol change, and it does
  not stop the objections from repeating. The reminder does its own framing inside its
  reason, so no change to the protocol is needed.
- **nabu committing or opening the PR itself.** That would be an actor neither the model
  nor Kyle asked for.
