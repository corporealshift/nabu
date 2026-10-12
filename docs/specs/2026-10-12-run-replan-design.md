# A run can say its plan won't work

**Date:** 2026-10-12
**Status:** Approved by Kyle in conversation
**Amends:** `2026-09-30-orchestrated-runs-design.md` (a new `replan-review` step and two
caps) and `2026-10-07-ask-claude-when-stuck-design.md` (the question gains a verdict
line).

## Problem

rho has no good way for a run's session to say "I can't do this as planned". When a
task cannot be done as written, the local model burns turns going round it: rewriting
the same file, re-running the same failing test. The fix session can already say the
check is wrong (`verify-revision.md`), but nothing lets a work session say the plan or
its task is wrong, and nothing lets the automatic Claude ask say it either.

## Decision

A work, fix or ci-fix session that finds the run's plan cannot be carried out as written
says so. Claude judges the claim. When Claude agrees, the run goes back to its own plan
step, and what was found is written where the planner reads it. The automatic ask can
reach the same verdict without the model asking. Goals are unchanged: they see a run that
ended done or failed, as now.

### The model claims it

- The work, fix and ci-fix prompts gain the paragraph fix sessions already have for
  `verify.sh`: if you are sure the plan, or this task, cannot be done as it is written,
  write `.rho/runs/<slug>/replan-request.md` saying what you tried, what makes it
  impossible, and what is true instead; commit it, and finish.
- Each of those steps' session goals gains "or `replan-request.md` explains why the plan
  cannot work". Without it the goal judge would refuse the finish, and the session would
  go round again.
- When the session ends, the runner finds the file and goes to a new step,
  **`replan-review`**. Claude, read-only as at every gate, gets the brief, the plan, the
  tasks, the task being done, and the claim. It answers one of:
  - **`REFUSED`**, then a paragraph of advice. The same step starts again in a fresh
    session, with the advice in its prompt, as a refused revision's advice reaches the
    next fix.
  - **`REPLAN`**, then the finding: what cannot be done as planned, and what is true
    instead. The runner appends it to `.rho/runs/<slug>/findings.md`, removes the
    request, and goes to `plan`.

### The automatic ask decides it

- The `claude` module's automatic question gains one instruction: if the task cannot be
  done as it is set out, begin the answer with the line `PLAN CANNOT WORK`, then say what
  makes it impossible and what is true instead. It knows nothing of runs. Outside a run
  the answer is advice to the model, as now.
- While a work, fix or ci-fix session runs, the runner reads its new events at each poll,
  from a cursor kept on the run (`EventsAfter` already pages by cursor). A `claude.ask`
  result from `source: module:claude` whose first line is `PLAN CANNOT WORK` makes the
  runner stop the session, keep what it committed and drop what it did not, and take the
  `REPLAN` path with Claude's answer as the finding. There is no second review: Claude
  already judged.
- A `claude.ask` the model made itself does not count: the model's question may have
  been a leading one, and its claim has its own path.
- `auto_ask_after` stays at 100.

### Re-planning

- The run goes back through `plan`, `plan-review`, `verify`, `verify-review`, `tasks`,
  then work. The check is rewritten too: what cannot be done is often a test it names.
- The plan, verify and tasks prompts say: read `findings.md` first; it is what earlier
  tries found cannot work. The plan and tasks prompts also say that work is already
  committed on the branch, to be seen with `git log <base>..`, and to plan and list only
  what remains.
- `findings.md` only grows. It goes into the pull request with the run's other files, so
  Kyle sees what was found.
- Each re-plan and each refusal is a history entry with the finding or the advice as its
  note, so the run's reflection sees them.

### Caps

- **`MaxReplans`, 2.** A third agreed claim fails the run at `plan` with the finding, so
  resuming it plans again. A goal handles that as any failed run: the round ends and its
  check runs.
- **`MaxRefusals`, 2.** A third refused claim fails the run at the step that claimed, so
  resuming it tries that step again: the model and Claude disagree about whether the
  plan can work, and that is worth Kyle's eye.
- `Resume` clears both, as it clears the fix and revision counts.
- A re-plan also starts the fix and revision counts again, since the check they were
  counted against is written again.

## How it fails

| What | Then |
|---|---|
| Claude's answer is neither `REPLAN` nor `REFUSED`, or the call fails | Retried as the other Claude steps are, 3 tries a poll apart, then the run fails at `replan-review` |
| A planning session (brief, plan, verify, tasks) writes the request | Discarded as today: it changed a file other than its own |
| A session claims and also commits work | The work stays on the branch; a re-plan plans around it, a refusal's retry builds on it |
| The runner restarts | The cursor is saved on the run. At worst events are read again, and the verdict is still in the log |
| The marker shows in an answer outside a run | Nothing acts on it; the model reads it as advice |

## Rejected

- **Re-planning the goal.** Kyle ruled it out: a run re-plans itself, and a goal only
  hears of it if the run fails.
- **Claude writes the new plan in its verdict.** Faster, but it skips the review steps
  and makes Claude the run's planner, which the runs design leaves to the local model.
- **Re-planning only the tasks and keeping the check.** A test the check names that
  cannot be run would stay impossible.
- **Telling the model to act on the automatic answer.** The stuck-session spec found it
  ignored loop notices for 344 turns. The runner acting on the verdict is reliable.
- **The `claude` module ending the session itself.** The module does not know what a run
  is, and stopping step sessions is the runner's job (invariant 4).

## What proves it works

- Transition rows: work, fix and ci-fix with a request go to `replan-review`; `REPLAN`
  goes to `plan` and counts a re-plan; `REFUSED` goes back to the same step with its
  advice; each cap fails the run; `Resume` clears both counts.
- Parser rows for `REPLAN` and `REFUSED`, and malformed answers.
- Runner tests with fakes: a `PLAN CANNOT WORK` result from `module:claude` in a running
  session stops it and re-plans; one from the model's own `claude.ask` is ignored;
  `findings.md` is appended and named in the plan, verify and tasks prompts.
- The `claude` module's question carries the verdict instruction.
- The full gate.
- Live: the next liftoff run that hits a wall shows a `replan-review` step, and its pull
  request carries `findings.md`.
