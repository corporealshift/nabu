# A task's failed check can be cleared

**Date:** 2026-10-09
**Status:** Approved by Kyle in conversation

## Problem

A fix session on the liftoff equipment run ended blocked after about a hundred turns. It
had done what the owner asked. The verify module's stop gate refused every stop with:

```
these checks are failing:
  - task:: exit status 1
```

Two faults combined.

1. **The check had no task id.** `task.update` lets the model leave ids out: `Merge`
   gives each task the id of an earlier task with the same title, or a new `tN`. But the
   verify module's `GateTool` runs a task's check before that happens, under the id the
   model sent. Here that was none, so every check was recorded as `task:` with no
   `task_id`. Two more things went wrong as a result:
   - A task already done counted as newly done each time the list was resent, so its
     check ran again.
   - A matching task was taken for a new one, and asked for a `done_when` it already had.
2. **Nothing could clear the failure.** A failed check vetoes until a later check of the
   same name passes. The model had cancelled the task whose check failed, as it was told
   to, so that check never ran again. The model then searched the repository for
   `task::` for ninety turns, because the veto did not say what it was.

## Decision

- **Ids first.** The id assignment moves out of `tools.Merge` into
  `module.TaskIDs(prev, incoming)`, which both use. Merge calls it before anything else,
  and the gate calls it before it runs a check. A check is then always recorded as
  `task:<id>` with its `task_id`, as `protocol/spec.md` shows it. The done-already and
  `done_when` rules use the real id.
- **A task's check counts only while its task does.** A failed check belonging to a task
  stops vetoing when that task is cancelled or no longer on the list. A task's check is
  one with a `task_id`, or, in sessions logged before this change, a name starting
  `task:`. A check with no task id at all, the bare `task:`, can never be cleared, and
  no longer vetoes. Other checks, such as the workspace gate's `verify.command`, are
  unchanged.
- **The veto says which task, and how to clear it.** Each failing task check is shown
  with its task's title. The veto ends: "A task's check clears when the task is marked
  done and its check passes, or when the task is cancelled." The reminder for sessions
  without a goal uses the same list.

A failed or blocked task's check still vetoes. The model said the work failed, or is
stuck, and the check agrees. Cancelling is how it says the work is no longer wanted.

## Rejected

- **Requiring ids in `task.update`.** Spec §9.2 lets them be left out, and the local
  model leaves them out often.
- **Naming a check by its task's title when it has no id.** Titles change between
  snapshots, so the failure would still be stranded.
- **Dropping a task's failed check once the task is marked failed.** A failed task is
  not finished. Letting its check go quiet would let a goal session stop on work that
  does not pass.
- **Clearing failed checks at each user message.** That is broader than this bug, and a
  failure the session caused should still hold over its next turn.

## What proves it works

`daemon/modules/verify`:
- Tasks sent without ids are checked under the ids they get: an existing title takes its
  id, and a new task takes the next `tN`.
- A task already done, resent without its id, does not run its check again.
- A failed task's check vetoes, naming the task's title and saying how to clear it.
- A failed `verify.command` still vetoes.
- A task's failed check does not veto once the task is cancelled, once it is off the
  list, or when it was logged with no id.

`daemon/tools`: `Merge`'s existing tests pass unchanged on the extracted helper.

Then the full gate.
