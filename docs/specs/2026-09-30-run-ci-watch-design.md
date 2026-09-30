# Runs watch their CI

**Date:** 2026-09-30
**Status:** Approved by Kyle in conversation
**Part 3 of 4** of orchestrated runs (`2026-09-30-orchestrated-runs-design.md`). It adds
the `ci` step after `pr`, and its fix sessions.

## Problem

A run that opens a PR and stops has not finished: CI can fail on something `verify.sh`
never runs, like another OS, a linter, or a build matrix. Kyle wants the run to watch CI
and fix it the same way it fixes a failing `check`.

## Decision

After `pr`, the run is at `ci`, and the runner polls the PR's checks each poll:

```
gh pr checks <n> --repo <repo> --json name,state,bucket,link
```

- **All checks pass** (`bucket` is `pass` or `skipping` throughout): the run is `done`.
  The home is labeled `run:done`.
- **Any check pending:** wait for the next poll.
- **A PR with no checks at all:** after 10 minutes with none reported, the run is `done`.
  A repository without CI has nothing to wait for.
- **Any check failed:** go to `ci-fix`.

### `ci-fix`

1. **Collect the failure.** For each failed check that is a GitHub Actions run:
   `gh run view <run id> --log-failed`, with the tail kept to 20,000 characters per check.
   The run ID comes from the check's `link`. A check that isn't Actions gives its name
   and link only.
2. **A fix session in the run's worktree.** The prompt gives the failure logs. The goal
   is: "The CI failures listed in the first message are fixed and committed, and
   `verify.sh` still passes." The same rules as `fix` apply: `verify.sh` must not be
   touched, and asking for a revision goes through `verify-revision.md`.
3. **`check`.** The runner runs `verify.sh`, because a CI fix that breaks the brief is no
   fix. If it fails, the run goes through the normal `fix` loop first; its attempts count
   against the `fix` cap.
4. **Push** (never forced) and return to `ci`.

**Cap:** 5 `ci-fix` attempts per run, then `failed` at `ci-fix`, with the PR left open as
it is.

### What the run does not touch

- The PR's review comments. The PR carries the `nabu` label, so the comments job
  (`2026-09-28-github-comments-design.md`) handles them in its own worktree, as for any
  labeled PR.
- **A push from the comments job while the run is at `ci`** moves the head, and the run
  simply sees new checks. If a `ci-fix` push is rejected because the branch moved, the
  run fetches, resets its worktree to the new head, and runs `ci-fix` again. That costs
  one attempt.

## How it fails

| What | Then |
|---|---|
| `gh pr checks` fails | Retried at the next poll. After 10 polls in a row, `failed` at `ci`. |
| A failed check with no readable log | The fix session gets the check's name and link, and is told the log was not available. |
| The same check fails the same way after a fix | Counted like any other attempt. The cap ends it. |
| The PR is closed or merged while the run watches | The run is `done` if merged, and `failed` at `ci` if closed. |

## Not doing

- Re-running flaky checks. A check that fails once and passes on retry costs one
  `ci-fix` attempt, which the session may spend concluding there is nothing to fix.
- CI systems other than what `gh pr checks` reports.
- Merging the PR.

## Rejected

- **Ending the run at `pr`, and fixing CI through the comments job.** CI failures are not
  comments, and the run already has the worktree, the brief and `verify.sh` that a fix
  needs.
- **Letting a `ci-fix` session change `verify.sh` to match CI.** The rule against
  touching the script holds everywhere. If CI shows the check is missing something,
  `verify-revision.md` sends it to Claude.

## What proves it works

- The step function's table gains rows for:
  - pass;
  - pending;
  - no checks, timed out;
  - failed;
  - the `ci-fix` cap;
  - a moved head;
  - merged;
  - closed.
- Parsing `gh pr checks` and a run ID out of a check link, table-tested.
- Live: a scratch PR whose CI workflow fails on something `verify.sh` does not cover,
  fixed by the run.
