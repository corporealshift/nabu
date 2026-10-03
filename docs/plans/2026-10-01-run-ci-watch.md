# Runs watch their CI — plan

Spec: `docs/specs/2026-09-30-run-ci-watch-design.md`. Branch `runs-ci`, rebased on `main`
after #126, and one PR. Every step leaves
`go build ./... && go vet ./... && go test ./...` green and is its own commit.

## 1. The steps and their transitions

**Depends on:** nothing.

**Changes:**
- `clients/runs/state.go`:
  - steps `ci`, `ci-fix` and `push`;
  - `Run` gains `CIFixes` and `CISince`.
- `clients/runs/transition.go`:
  - `Outcome` gains `CI` (`pass`, `pending`, `fail`, `none`, `merged` or `closed`) and
    `Wait`. A waiting outcome changes nothing and costs no attempt.
  - The transitions:
    - `pr` → `ci`;
    - `ci`: `pass` or `merged` → `done`; `closed` → `failed`; `pending` → wait; `none`
      → wait, until 10 minutes since `CISince`, then `done`; `fail` → `ci-fix`, capped at
      5, after which the run fails at `ci-fix`;
    - `ci-fix` → `check`;
    - `check` passes, with a PR open → `push`;
    - `push` → `ci`.
  - `ci` gets 10 tries rather than 3. It polls, so one failed `gh` call is not much.

**Proves it:** new `TestTransition` rows for each arrow, the cap, the timeout and the
10 tries. Existing rows stay the same, since `check` with no PR still goes to the final
review or the PR.

## 2. Reading CI

**Depends on:** nothing.

**Changes:**
- `clients/runs/ci.go`:
  - `Check{Name, State, Link}`.
  - `Classify([]Check) string`: pending wins, then fail, then pass, and `none` when there
    are no checks.
  - `RunID(link)`: the Actions run ID out of a `detailsUrl`.
- `clients/runs/exec.go`, on `GHCLI`:
  - `PRChecks(dir, n) (state string, checks []Check, err)`, over `gh pr view --json
    state,statusCheckRollup`, decoding both check runs and status contexts;
  - `FailedLog(dir, runID) (string, err)`, over `gh run view <id> --log-failed`, keeping
    the last 20,000 characters.
- `clients/runs/prompts.go`: `CIFixPrompt(r, failures)` and its goal.

**Proves it:**
- A table test of `Classify` over every state and conclusion.
- `RunID` against Actions URLs and other URLs.
- `PRChecks` decoding a recorded mix of check runs and status contexts.
- `TestExecArgs` rows for the new commands.
- A prompt test.

## 3. The runner does the CI steps

**Depends on:** steps 1 and 2.

**Changes:** `clients/runs/runner.go`:
- **`ci`:** `PRChecks`, reduced to an outcome. When it fails, the failure logs of each
  failed check that has a run ID are collected into `Output`.
- **`ci-fix`:** a session with `CIFixPrompt` and its goal. The `verify.sh` guard and the
  nudge apply, as for `fix`.
- **`push`:** `Git.Push`. If git says the push was rejected because the branch moved,
  `Fetch` the remote, reset the worktree to `origin/<branch>`, and go to `ci`: the
  moved head has checks of its own.
- **The labels:** `run:attempt:n/5` during `ci-fix`.

**Proves it:** fake-backed tests for:
- a run that goes from `pr` to `ci`, then passes;
- pending, then passing;
- failing, then `ci-fix`, `check`, `push`, then passing, with the failure log reaching the
  fix prompt;
- the cap;
- merged, and closed;
- no checks, timing out to `done`;
- a rejected push resetting the worktree to the moved head.

## 4. Checking it for real

On the scratch repository:
1. Add a CI workflow to `main` that checks something the brief will not mention: that
   `README.md` exists and mentions every exported function.
2. Run a brief that adds a function.
3. Check that the run opens its PR, the check fails, `ci-fix` adds to the README, the
   push goes up, and the next CI run passes, so the run is `done`.

## Not doing

- Re-running flaky checks.
- Anything other than GitHub's checks.
