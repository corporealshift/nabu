# Orchestrated runs: the runner — plan

Spec: `docs/specs/2026-09-30-orchestrated-runs-design.md`. Branch `runs-runner`, rebased on
`main` after #125, and one PR. Every step leaves
`go build ./... && go vet ./... && go test ./...` green and is its own commit.

The package is `clients/runs`. It imports `clients/goclient`, `clients/github` (for
`ExecRunner`, `Signature` and `Marker`) and `protocol`, and nothing under `daemon/`. A
boundary test like the watcher's enforces that.

## Decisions the spec left to the plan

- **Transitions are pure.** `Transition(Run, Outcome) Run` takes the result of a step and
  gives the next state: step, counters, failure. The runner performs a step, reduces what
  happened to an `Outcome`, and hands it to `Transition`.
- **Session slots are handed out at admission, not while a run advances.** `Advance`
  observes sessions and does every mechanical and Claude step. When a run needs its next
  session, `Advance` marks it as waiting for a slot. The shared admission starts waiting
  sessions: comment jobs first, then runs (continuing ones before new ones), then
  reviews. So a run gives up its slot between steps, and a comment job can go in between.
- **Claude calls and `verify.sh` block the loop while they run.** With `max_jobs: 1` and
  one local model, nothing else could run at the same time anyway. Running them in the
  background is left until it is needed.
- **Config lives in `<root>/runner/config.json`, and it is optional.** It holds `poll`,
  `max_jobs`, `label` (`nabu`), `verify_timeout` (30m), the `claude` settings (`path`,
  `model`, `timeout` 15m) and the turn budgets (none by default; `plan_turns` and `work_turns` set them).
  `nabu runner` also runs the GitHub watcher when `<root>/github/config.json` exists. The
  runner's `max_jobs` and `poll` then govern both.
- **The slug** comes from the brief, or from the home's last prompt for a plain `/run`:
  lower-cased, dashes, at most 40 characters, then `-` and the last 4 characters of the
  home's ID, so two runs never share a branch.
- **The PR** has the brief's first line as its title, at most 70 characters. Its body
  has the brief, pointers to `plan.md` and `tasks.md`, the passing `verify.sh` run, and
  the final-review notes. It ends with the signature and the marker.

## 1. Run state and transitions

**Depends on:** nothing.

**Changes:** `clients/runs/state.go`, `clients/runs/transition.go`:
- `Step` constants for every step in the spec's table, plus `done` and `failed`.
- `Run` holds:
  - the home, workspace, slug, branch and worktree;
  - the step and `FailedAt`;
  - the attempt count for the current step, the task index, `Fixes`, `Revisions` and
    `FinalReviewed`;
  - the current session, the commit it started from, and `Prompted`/`Stopped`;
  - `WaitingSlot`, the PR number and URL, and the notes.
- `Outcome{OK, CheckPassed, Revision, TasksLeft, Blockers, Why}`.
- `Transition(Run, Outcome) Run`, implementing the spec:
  - a session step that fails is retried once;
  - Claude and `pr` steps get 3 tries;
  - `check` → `fix`, capped at 10;
  - `fix` → `revise` when a revision was asked for, capped at 2;
  - `final-review` sends blockers back to `work`, once;
  - the attempt count resets whenever the step or the task changes.
- `Save` and `Load` for `<root>/runner/runs/<home>.json`, atomic as the watcher's state
  is.

**Proves it:** `TestTransition`, with a row for every arrow in the spec's steps table,
each cap, the retry-once rule, the attempt reset, blockers going back to `work` exactly
once, and resuming a failed run (`Resume`: step = `FailedAt`, counters reset). Plus
`TestRunRoundTrip`.

## 2. Prompts and parsers

**Depends on:** step 1.

**Changes:** `clients/runs/prompts.go`, `clients/runs/parse.go`:
- The prompts for each model step: `brief`, `plan`, `tasks`, `verify`, `work`, `fix`.
  Each names the files it reads and the one file it must write and commit. Each forbids
  touching `verify.sh`, forbids `ask`, and forbids pushing.
- The goals for `work` and `fix`.
- The prompts for each Claude gate: `plan-review`, `verify-review` (including whether the
  script passed on the untouched tree), `revise`, `final-review`. The final review's
  prompt holds the narrow definition of a blocker.
- `ParseTasks(md) []Task{Title, Done}` for GitHub-style checkboxes, plus
  `TickTask(md, i)` and `AppendTasks(md, blockers)`.
- `ParseRewrite(answer, lang, keep string) (text string, changed bool, ok bool)`. It
  reads the plan and verify answers: a fenced block means a rewrite, and the sentinel
  (`NO CHANGES`, `APPROVED`, `REFUSED`) means none.
- `ParseFinal(answer) (blockers, notes, ok)`.
- `Slug(text, homeID)`.

**Proves it:** table tests for every parser:
- well-formed answers;
- a sentinel together with a block (the block wins);
- neither a sentinel nor a block (`ok` false);
- nested fences inside a script;
- tasks with indentation, with `[X]`, and with none;
- slugs from unicode and long text.

Also a prompt test for each step, checking it names its files and carries the
`verify.sh` rule.

## 3. Ports and their real implementations

**Depends on:** step 1.

**Changes:** `clients/runs/ports.go`, `clients/runs/exec.go`, `clients/runs/daemon.go`:
- `Daemon`: `ListRequested` (sessions labeled `run:requested`), `Home(id)` (the goal and
  labels, and the transcript for `brief`), `SetLabels`, `SetGoal`,
  `Create(workspace, parent, maxTurns)`, `SendPrompt`, `State`, `Events`, `Stop`.
- `Git`, over `ExecRunner`:
  - `DefaultBranch(clone)` (`symbolic-ref refs/remotes/origin/HEAD`, falling back to
    `main`);
  - `Fetch`, and `AddBranchWorktree(clone, path, branch, from)`;
  - `Head`, `Changed(dir, from) []string`, `Clean(dir, path) bool`, and
    `ResetHard(dir, sha)` (reset, then `clean -fd`);
  - `Commit(dir, msg, paths...)`, `Remove(dir, path)` and `Push(dir, branch)`.
- `GH`: `CreatePR(dir, base, head, title, body, label) (number, url)`.
- `Claude`: `Ask(dir, prompt)`, running `claude -p <prompt> --allowedTools Read`,
  `Grep` and `Glob`, with an optional `--model`, in the worktree, under the configured
  timeout. `Available()` checks that the CLI can be found.
- `Shell`: `Verify(dir, script, timeout) (passed bool, output string)`. It runs the
  script with `bash`, and keeps the last 20,000 characters of the combined output.

**Proves it:** `TestExecArgs` with a recording `Runner`, one row per command, like the
watcher's. `Verify` is tested against real scripts: exit 0, exit 1, output too long, and
a timeout.

## 4. The runner

**Depends on:** steps 1–3.

**Changes:** `clients/runs/runner.go`:
- `Runner{Cfg, Root, Daemon dial, Git, GH, Claude, Shell, Now, Log}`, with the methods
  below.
- `Advance(ctx)`. For each run that isn't finished, it performs the current step:
  - it observes a running session, and when the session is done, stops it and checks what
    it produced;
  - or it does the mechanical or Claude step;
  - then it applies `Transition`, and updates the home's labels (`run:<step>`, and
    `run:attempt:n/10` during `fix`).
- `Waiting() int` and `Start(ctx, n) (started int, err error)`. `Start` begins up to `n`
  waiting step sessions, continuing runs first. It then takes new runs: each home
  labeled `run:requested` gets a run, `setup` runs, and so does the first session step if
  a slot is left.
  - Every step session is created with `parent: home`, in `auto` mode, with its budget.
    `work` and `fix` sessions get their goal.
- `/run` on a home labeled `run:failed` becomes `Resume`.
- The rule against touching `verify.sh`: after every `work` and `fix` session, `Changed`
  including the script means reset to the start commit and retry.
- If `Claude.Available()` is false, the home is labeled `run:failed` with the reason, and
  no run starts.

**Proves it:** fake-backed tests, like the watcher's, for:
- a whole run from a text brief to `done`, checking the commit messages in order and the
  PR;
- a plain `/run`, which goes through the `brief` session;
- a session that touches `verify.sh` (reset and retried);
- `check` failing twice, then passing;
- a revision rewritten, and one refused;
- blockers bouncing back once;
- the fix cap giving `failed`, then `/run` resuming it;
- a restart in the middle of a session;
- Claude missing;
- the home's labels at each step.

## 5. `nabu runner`

**Depends on:** step 4.

**Changes:**
- `clients/github/watcher.go`: an optional `Runs` hook (`Waiting`, `Start`,
  `RunningSessions`), consulted at admission between comment jobs and reviews, and
  counted in the running total.
- `cmd/nabu/runner.go`:
  - `nabu runner [--once] [--root]`. Each tick, `Runner.Advance` and then `Watcher.Poll`
    (with the hook), or `Runner.Advance` and then `Start` on its own when there's no
    GitHub config.
  - `--once` loops until no run is active and nothing starts.
- `cmd/nabu/main.go`: usage for `runner`, and `github` described as the GitHub part
  alone.
- `ARCHITECTURE.md`: `clients/runs`.

**Proves it:**
- A watcher test: with a waiting run and a due review under `max_jobs: 1`, the comment
  job starts first, then the run, then the review.
- A CLI test: `nabu runner --once` on an empty root exits 0 with nothing to do.

## 6. Checking it for real

Against a scratch daemon (`--root`, its own port) and the scratch repository:
1. `/run add a Median function with tests` in a TUI session on the scratch clone. Or,
   since the TUI can't be driven from here, the same calls `/run` makes: `set_goal`, then
   `set_option labels`.
2. `nabu runner --once --root …`.
3. Check the branch's commits in order, `.nabu/runs/<slug>/`, the Claude-committed
   revisions if any, the passing `verify.sh`, and the PR.

## Not doing in this plan

- `ci` and `ci-fix`: part 3.
- Issues: part 4.
- Running Claude or `verify.sh` in the background.
