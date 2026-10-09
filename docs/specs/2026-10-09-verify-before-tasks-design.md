# The check before the tasks, and only the tests the brief needs

**Date:** 2026-10-09
**Status:** Approved by Kyle in conversation
**Amends:** `2026-09-30-orchestrated-runs-design.md` (the order of the planning steps)
and `2026-10-04-run-decisions-design.md` ("The check pins each decision", and the PR
body's line about it).

## Problem

In the last two liftoff runs, fixing the check took longer than building the feature:

| Run | Tasks | Fix after the check failed |
|---|---|---|
| equipment-management-in-mission-control | about 2h | over 6h12m, 244 turns, 5 summaries, 3 asks to Claude |
| mission-control-settings | about 7h | 3h41m, 181 turns |
| navigation-shell-and-theme-fixes | about 1h40m | none |

In the equipment run, the first time the check ran all 19 view-model and existing tests
passed. All six failures were Compose screen tests that "did not run": nobody had
written them. The brief says "Compose UI tests are not required." Neither the plan nor
the tasks mention those tests. The verify step added them, one per plan decision. The
fix session spent six hours writing them from scratch, mostly fighting the test setup:
a form below the bottom of the Robolectric window, buttons laid out with zero width,
and Room's background threads, which `waitForIdle` does not wait for. It edited
`MissionControlScreenTest.kt` 42 times.

Across the five runs whose scripts name tests, the scripts name 25 to 46 test methods
each, and almost none of those names appear in `tasks.md`. There are three causes:

1. **The tasks are written before the check.** `tasks` runs before `verify`, so no task
   can be given the tests the check goes on to name. `work` is told to read `verify.sh`
   and write "where it names a test this task should write". A test that no task's
   session recognises as its own is left to `fix`. By then a single session has to
   write it, with no plan for it and none of the implementation's context.
   navigation-shell-and-theme-fixes ran smoothly because its tasks happened to name the
   tests its check required.
2. **One named test per decision.** `verify` gives each entry in the plan's
   `## Decisions` a test of its own, and `verify-review` enforces it. Plans now have
   long decisions sections, and in an Android app most decisions are about the screen.
   So the rule produces screen tests: the slowest and most fragile kind, and the kind
   the local model handles worst. The rule also overrides the brief, as it did here.
3. **Nothing holds the check to the brief's testing scope.** "Not required" is not a
   requirement, so the verify step never acts on it.

## Decision

### The check comes before the tasks

The planning steps run in this order:

```
brief → plan → plan-review → verify → verify-review → tasks → work …
```

- `verify` is written from the brief and the plan. The plan already says how the
  result will be tested.
- `verify-review` runs the script and reviews it exactly as before. Before any work,
  the script must still fail.
- `tasks` reads the brief, the plan and the approved `verify.sh`. **Every test that
  `verify.sh` names and that does not exist yet goes to exactly one task, and that task
  names it under the exact name the script uses.** Tests that already exist need no
  task.
- `work` writes the tests its own task names, under exactly those names. It still reads
  `verify.sh`, since the run is done when the script passes.

`verify-review` then always goes to `tasks`, and `tasks` goes to `work`, or to `check`
when it leaves nothing to do.

### Decisions are shown in the PR, not forced into tests

The rule "one named test per decision" goes. A decision's place is the PR, where Kyle
reads it and changes it with a comment, and that stays as it is.

- A decision gets a named test only where a test the check already runs shows it, or
  where a quick test of the logic can (the state holder or view model, not the screen).
  The comment above that test names the decision.
- `verify-review` no longer requires a test for every decision.
- The PR body's line changes from "Each is pinned by a test in `verify.sh`" to "Where
  `verify.sh` tests one, the test's comment names it. To change one, say so in a comment
  on this PR."
- `final-review` keeps treating a disagreement with a decision as a note, not a
  blocker. Its reason was that `verify.sh` pins the decision. The reason now is that
  the decision is Kyle's to make, from the PR.

### The check stays within what the brief asks

`verify` and `verify-review` are told to:

- name roughly one test per thing the brief asks for, not one per detail of how it is
  built;
- never require a kind of test the brief or the plan rules out or says is not needed.
  "UI tests are not required" means the check names none;
- prefer the cheapest test that shows the behaviour: a test of the logic over a test
  driven through the UI, unless the brief asks for the UI test.

`revise`, which judges a fix session's claim that the check is wrong, is told the other
side of this: a check that requires more than the brief does is wrong too. It is told
that in the same sentence that says not to weaken the check.

## Where it lives

- `clients/runs/transition.go`: the new order.
- `clients/runs/prompts.go`: `VerifyPrompt`, `VerifyReviewPrompt`, `TasksPrompt`,
  `WorkPrompt`, `RevisePrompt` and `FinalReviewPrompt`.
- `clients/runs/parse.go`: the PR body's line.

Nothing changes in the daemon or the protocol.

## How it fails

| What happens | Effect |
|---|---|
| The tasks session leaves a named test out of every task | `work` still reads `verify.sh`, so a session may pick it up. If none does, `fix` writes it, as today. |
| The check still names a screen test the brief did not rule out | That is allowed. The brief may want one, and the fix session can ask for a revision. |
| A runner is upgraded while a run is between `plan-review` and `tasks` | A run that had reached `tasks` under the old order goes on to `work` without a `verify.sh`. `check` then fails, and the fix session is asked to fix a check that does not exist. Upgrade between runs. None is in planning as this is written. |

## Rejected

- **The runner checks that every test named in `verify.sh` appears in `tasks.md`.** The
  script is free-form bash, and the recent ones name tests through helper functions each
  script defines for itself. A required list the runner could parse would be a second copy of
  the check that drifts away from it.
- **Keep the order, and have the verify step add tasks.** That breaks the rule that a
  planning step writes only its own file, which exists because a verify session once
  wrote the feature too.
- **Keep the per-decision tests, and only allow them below the UI.** That is better,
  but a decision the brief left open still adds a test nobody planned. The PR is where
  decisions are reviewed.
- **Cap the number of named tests.** A number would be arbitrary, and the problem was
  tests nobody wrote, not too many.

## What proves it works

- `transition_test.go`: `plan-review → verify → verify-review → tasks → work`, and
  `tasks` with nothing left goes to `check`.
- `prompts_test.go`:
  - `tasks` reads `verify.sh` and gives each named test to one task;
  - `work` writes the tests its entry names;
  - `verify` and `verify-review` keep to the brief's scope and no longer require a test
    per decision;
  - `revise` says a check that asks for more than the brief is wrong.
- `parse_test.go`: the PR body's new line.
- The runner tests that walk a run through its steps, in the new order.
- The full gate.
- Live: in the next liftoff run, every test that `verify.sh` names appears in
  `tasks.md`, the first `check` fails only on behaviour (if at all), and a fix session
  is short or not needed.
