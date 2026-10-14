# Claude gates park, and can be skipped: plan

Spec: `docs/specs/2026-10-13-claude-park-and-skip-design.md`.

One branch, `claude-park-skip`, off main, merged as one PR with a commit per step. Each
step leaves `gofmt -l .` empty and `go build ./... && go vet ./... && go test ./...`
green. Steps 2 and 5 also change what the Android app reads, so they run
`clients/android/gradlew.sh testDebugUnitTest` too.

### 1. Ask reads the CLI's JSON result

- **Changes:** `clients/runs/exec.go`: `Args` adds `--output-format json`. `Ask` reads
  the last line of stdout that is a JSON object, and returns its `result` when it is not
  `is_error`. `ClaudeUnavailable{Reason}` is returned for 429, 401 and 5xx statuses and
  for a timeout; everything else is a plain error, as now. `clients/runs/exec_test.go`:
  the fake CLI prints result objects; tests for each case the spec lists, and the new
  args.
- **Proves it:** `go test ./clients/runs -run 'Claude'` passes. The runner still counts
  every error as a try, so no other test changes.
- **Depends on:** nothing.

### 2. A gate parks on ClaudeUnavailable

- **Changes:**
  - `transition.go`, `goaltransition.go`: `Outcome.Parked` and `GoalOutcome.Parked`.
    A wait sets `Parked` from the outcome, and any other outcome clears it.
  - `state.go`, `goal.go`: `Run.Parked` and `Goal.Parked`.
  - `runner.go`, `goalrunner.go`: each gate turns an `Ask` error into its outcome through
    one helper. A wait that changes `Parked` is saved and shown, and logged when it
    starts and ends.
  - `status.go`: `parked` on both shapes. `status_test.go` puts it on the example run,
    and `testdata/status` is regenerated.
  - Android `protocol/Status.kt`: `parked` on both. The run page's Now card and the goal
    page show "Waiting for Claude: <reason>". The status tests read it.
- **Proves it:** new rows in `transition_test` and `goaltransition_test`. A runner test:
  a plan-review whose `Ask` returns `ClaudeUnavailable` is at plan-review after five polls,
  parked, with attempt 0; once `Ask` answers, it moves on with `Parked` empty. A goal
  test: a parked check does not block. The Android unit tests pass.
- **Depends on:** step 1.

### 3. The runner takes run:skip and goal:skip

- **Changes:**
  - `state.go`, `goal.go`: `Run.Skip`, `Run.Skipped`, `Goal.Skip`, `Verdict.Skipped`.
  - `transition.go`, `goaltransition.go`: an outcome that is not a failure clears `Skip`.
  - `runner.go`: `pickUpSkips` before `pickUp`, and each gate's skipped outcome.
    `record` gives a skipped gate the result "skipped by the owner". `prBody` names the
    gates Claude did not review.
  - `goalrunner.go`: `goal:skip` and the check's skipped outcome. `goalprompts.go`:
    `GoalPRBody` for a skipped verdict.
- **Proves it:** runner tests at each of the five run gates that check the next step,
  that no call reached Claude, and the gate's files. A skip off a gate leaves the run where it was.
  The PR body names the skipped gate. Goal tests: a skipped check reaches the goal's PR
  with a skipped verdict, and a skip at breakdown does nothing.
- **Depends on:** step 2, since skipping a parked gate clears `Parked`.

### 4. /skip in the TUI

- **Changes:** `clients/go-tui/commands.go`, `update.go`, `tui.go`: `/skip`. It sets
  `run:skip` or `goal:skip` when the home's label names a gate that can be skipped, and
  otherwise says why not.
- **Proves it:** table test of the labels `/skip` gives, and the note when it cannot.
- **Depends on:** step 3.

### 5. The skip button in the app

- **Changes:** `ui/Runs.kt`: `skipLabels` and `skippable(labels)`. `RhoViewModel.skip`
  sets the labels. `RunPage` and `GoalPage` show **Skip Claude's check** while the
  home's label is a skippable gate, and `MainActivity` passes them the home's labels and
  the action.
- **Proves it:** `RunsTest` covers `skipLabels` and `skippable`, and
  `testDebugUnitTest` and `assembleDebug` pass.
- **Depends on:** step 3.

## Not in this plan

- A push notification when a run parks.
- Any change to how a run starts when the CLI is missing.
