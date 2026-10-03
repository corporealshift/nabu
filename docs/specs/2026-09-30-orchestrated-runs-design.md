# Orchestrated runs

**Date:** 2026-09-30
**Status:** Approved by Kyle in conversation
**Part 2 of 4.** This spec is the design as a whole, plus the runner through opening the
PR. The other parts:
- `2026-09-30-session-parent-and-labels-design.md` (part 1: protocol and TUI);
- `2026-09-30-run-ci-watch-design.md` (part 3: CI);
- `2026-09-30-runs-from-issues-design.md` (part 4: issues).

Each part is its own PR, and they land in order.

## Problem

Kyle works the same way on every change:
1. branch;
2. plan;
3. break the plan into tasks;
4. decide how to verify the work;
5. work the tasks;
6. run the verification, and fix until it passes;
7. open a PR;
8. fix CI.

He wants nabu to do this from idea to green PR without bringing him back in.

Two things stop a single session from doing it:
- **Nothing makes the model follow the order.** A skill or a prompt describes the
  workflow, and the model decides whether to follow it. With the local model, that is
  the part that breaks.
- **The local model does badly at long tasks.** The logs show it repeating itself, losing
  instructions after a few vetoes, and drifting from what was asked
  (`2026-09-26-one-reminder-stop-design.md`, `2026-09-23-loop-module-design.md`).

## Decision

A **run** takes a brief to a green PR through a fixed sequence of named steps.
`nabu runner`, a long-running client like the GitHub watcher, drives it:
- it starts one short session for each step that needs the model;
- it does every mechanical step itself;
- it calls Claude at three gates.

The order is the runner's code, not the model's choice. `nabu runner` does the GitHub
watcher's jobs too, and `nabu github` stays as an alias for it.

### Where a run starts

- **From the TUI** (part 1): `/run` in any session makes that session the run's **home**.
  - `/run <text>` makes the text the brief.
  - Plain `/run` after a back-and-forth means the `brief` step turns the conversation
    into a brief.
- **From GitHub** (part 4): a labeled issue is a brief. The watcher creates a home
  session for it.

The home carries the run's labels (`run:requested`, then `run:<step>`, then `run:done` or
`run:failed`). Every step session is created with the home as its `parent`, so the TUI
shows the run as one thing. The brief is the home's **description**
(`2026-09-30-session-parent-and-labels-design.md`, amended). An earlier version of this
spec made the brief the home's goal, and assumed `set_goal` does not start the loop. It
does (protocol §7.11). In the first live run, the home began working on the brief in the
clone, and committed there, in parallel with the run. A description starts nothing.

### The steps

Everything a step produces is committed on the run's branch, `nabu/<slug>`, under
`.nabu/runs/<slug>/`, in a worktree beside Kyle's clone. The files are chunks of the work,
so they belong in the PR.

| Step | Who | Does | Checked by |
|---|---|---|---|
| `setup` | runner | Fetch; branch `nabu/<slug>` from the default branch in a new worktree | — |
| `brief` | runner, or model | Commit `brief.md`: the `/run` text as-is, or a session that turns the home conversation into a brief | file committed |
| `plan` | model | Write and commit `plan.md` from the brief | file committed |
| `plan-review` | Claude | Review the plan against the brief, once. Return the plan revised, or unchanged; the runner commits a revision | — |
| `tasks` | model | Write and commit `tasks.md`: a checkbox list of coarse tasks, each a chunk that ends in a commit. The prompt asks for 3 to 8; whatever comes back is used. | runner parses it |
| `verify` | model | Write and commit `verify.sh`: the command that proves the brief is done | file committed |
| `verify-review` | Claude | Review `verify.sh` against the brief and the plan. Return it rewritten, or approved; the runner commits a rewrite | — |
| `work` | model | One session per unchecked task. The goal is that task, done and committed. The runner ticks the box and commits. | new commits; `verify.sh` untouched |
| `check` | runner | Run `verify.sh` in the worktree | exit code |
| `fix` | model | After a failed `check`: a session given the output. The goal: `verify.sh` passes, committed, the script untouched. Then `check` again. | as `work` |
| `revise` | Claude | When a `fix` session says the check itself is wrong: review that reason, the failure and the script. Rewrite it, or refuse; then `check` | — |
| `final-review` | Claude | Review the finished work against the brief, once. Blockers become new tasks, then back to `work`; anything else goes in the PR body | — |
| `pr` | runner | Push the branch; `gh pr create` with the `nabu` label | — |
| `ci` | runner, model | Part 3 | — |

The run then ends `done`, or `failed` at the step that ran out of attempts.

**Choosing the next step** is a pure function: the run's state plus what was just
observed gives the next action. The runner does what it says and records the result.

### The three Claude gates

The runner calls the Claude Code CLI itself: `claude -p` in the worktree, allowed only
`Read`, `Grep` and `Glob`. It does not go through the `claude` module's tool, because
clients cannot import modules, and the call is the same one. Claude never writes files.
It returns text, and the runner writes the file and commits it, with messages like
`run: plan revised by claude review`.

- **`plan-review`**, once. It checks that the plan does what the brief asks and nothing
  else. It returns the full revised plan in a fenced `markdown` block, or
  `NO CHANGES`.
- **`verify-review`** at creation, and **`revise`** each time a fix session asks.
  - The script defines done for the whole run, and the model that wrote it is the one
    that wants it to pass, so this gate matters most.
  - Before calling Claude at `verify-review`, the runner runs the script on the
    untouched tree and says whether it passed. A script that passes before any work
    proves nothing, unless the brief is already met.
  - Claude returns the full script in a fenced `bash` block, or `APPROVED` (for
    `revise`: `REFUSED` with a reason).
  - **`verify.sh` is written like CI** (amended 2026-10-03). It runs what the
    repository's CI runs, plus the tests of the new behavior, by name, and fails if any
    of them fails or did not run. What makes it fail before the work is that those
    tests do not exist yet. It never checks the text of the code: no grepping source
    for names or patterns, counting tests, or checking that files exist. In the first
    breezeway run, the `verify` session wrote only the CI gates, which passed on
    untouched code, and `verify-review` made it fail by adding 38 greps of the source.
    Those dictate how the work is written, and fail correct work written differently.
    The `verify`, `verify-review` and `revise` prompts all say so. `work` sessions are
    told to read `verify.sh` and write the tests it names, under those names.
- **`final-review`**, once. The prompt defines a blocker narrowly: the work does not do
  what the brief asks, it has a bug, or `verify.sh` does not actually prove it. Style,
  suggestions and "consider" are explicitly not blockers.
  - It returns a JSON block: `{"blockers": [{"title", "detail"}], "notes": [...]}`.
  - Each blocker is appended to `tasks.md` as a task, and the run goes back to `work`.
    There is no second final review.
  - Notes go in the PR description.

If the `claude` CLI is not on the machine, the run refuses to start. The gates are the
point of the design, so they are never skipped.

### Rules that hold throughout

- **No model session changes `verify.sh`.** After every `work` and `fix` session, the
  runner checks `git diff <start>..HEAD -- .nabu/runs/<slug>/verify.sh`. A session that
  touched it is reset to its starting commit (`git reset --hard <start>` in the
  worktree), and the step is retried once with the refusal in the prompt. Only the runner
  changes the script, with Claude's text.
- **No step session can publish.** Every session the runner or the watcher starts is
  labeled `guard:no-push`, and the guard denies such a session any `git push` and any
  `gh` command that writes to GitHub, in every mode. A push is otherwise medium risk,
  which `auto` allows. In a live run, a `verify` session pushed its branch and opened its
  own PR, so the run's `pr` step found one already there and failed. The prompts asked it
  not to; the guard now makes sure.
- **A fix session asks for a revision by writing a file, not by editing the script.** A
  fix session that concludes the check is wrong writes
  `.nabu/runs/<slug>/verify-revision.md` with its reason, and commits it. The runner
  sees the file and goes to `revise`, then deletes the file in its own commit.
- **Every session runs in `auto` mode, is told not to use `ask`,** and has its own turn
  budget. Planning steps get 30 turns, `work` and `fix` get 60.
- **A session that stops short is told so, once, in the same session.** The local model
  sometimes ends its turn having written nothing. In the second live run, a `tasks`
  session read the brief and the plan, then stopped, twice, and the run failed. When a
  step session ends without its file (or, for `work`, without a commit), the runner
  sends it one message saying exactly what is missing. Only if it ends short again is
  that a failed attempt.
- **A planning step writes its own file and nothing else.** A `brief`, `plan`, `tasks` or
  `verify` session that changes any other file is reset and retried, and the prompts say
  so. In the second live run, the `verify` session also wrote the feature and its tests.
  So the check passed before any work, and task 1 found itself already done.
- **A task already done is done.** A `work` session that commits nothing, when its goal
  was judged met, has found its task already done by an earlier one. The box is ticked.
  Committing nothing with the goal unmet is stopping short, as above.
- **Plan steps check what was committed, not what was said.** The runner checks that the
  step's file exists and is committed. It does not ask a judge. `work` and `fix` sessions
  have goals, so the judge applies.

### Caps

| What | Cap | Then |
|---|---|---|
| `fix` attempts | 10 | `failed` at `fix` |
| `revise` rounds | 2 | `failed` at `revise` |
| A step session that ends `blocked`, `paused` or `error`, or touched `verify.sh` | retried once | `failed` at that step |
| `final-review` | once | — |

### State

The runner keeps its state per run in `<root>/runner/runs/<id>.json`. That includes:
- the home session, the slug, the branch and the worktree;
- the current step;
- the attempt counts;
- the current session's ID, and the commit it started from.

The state is saved before and after each action, so a restart resumes at the current
step.

**`/run` on a failed run** resumes it at the failed step with that step's counters reset.
A run is never deleted by the runner, and neither is its worktree.

### Choosing what runs

`nabu runner` finds new runs each poll by listing sessions labeled `run:requested`.
Runs, comment jobs and reviews share `max_jobs`, in this order: comment jobs (someone is
waiting), then runs, then reviews. A run holds one job slot while any of its sessions
runs, and none while it only runs commands.

## How it fails

| What | Then |
|---|---|
| A step session fails twice | The run is `failed` at that step. Nothing is pushed, and the worktree is kept. The home's label says where it stopped. |
| `check` keeps failing | Up to 10 `fix` sessions, then `failed` at `fix` |
| Claude cannot be reached, or times out | Retried at the next poll, 3 times, then `failed` at that gate |
| Claude's answer cannot be parsed | Treated as "no change" for `plan-review`, and as a failure for `verify-review`, `revise` and `final-review`, which then retry as above |
| The runner restarts | It resumes from the saved state |
| `git push` or `gh pr create` fails | Retried at the next poll. After 3 tries, `failed` at `pr` |
| A PR is already open for the run's branch | The run takes it over at `pr`: it sets the run's title and body, adds the label, and carries on. It is the run's branch, so it is the run's PR. With the guard, no session can open one; this covers a PR opened before the guard existed, or by an earlier `pr` attempt that failed before recording it. |
| The default branch moves during the run | Nothing happens. The PR is against the default branch, and a conflict is for CI and Kyle. Rebasing a run is not in scope. |

## Not doing

- Running more than one session of a run at once.
- Rebasing, or resolving conflicts with the base branch.
- Letting any model session merge the PR.
- A run engine inside the daemon (see Rejected).
- Skipping a Claude gate when the CLI is missing.

## Rejected

- **Skills alone.** A skill describes the workflow, but the model chooses whether to
  follow it. Skills survive here as what each step's session is told, not as what
  enforces the order.
- **A run engine in the daemon.** Runs as first-class daemon objects would mean new
  events and methods, and the step logic is policy (invariant 4). Kyle chose a runner
  process. The daemon gains only `parent` and `labels` (part 1).
- **One long session with a goal**, which is what the old issues spec
  (`2026-09-28-github-issues-design.md`) proposed. That is the long-running shape the
  local model does badly at, and part 4 replaces it.
- **A session per mechanical step.** Branching, running `verify.sh`, pushing and opening
  the PR have no judgment in them. Giving them to the model only adds ways to wander.
- **Claude as reviewer only, sending the script back to the local model.** Kyle chose
  having Claude rewrite `verify.sh` itself. It is faster, and the local model is the
  party with a reason to weaken it.
- **Final-review findings posted on the PR instead of fixed.** Kyle wants done to mean
  done: blockers are fixed before the PR opens.
- **Sending back a task list that is too fine.** The prompt asks for coarse tasks; a list
  that comes back fine is used as it is.

## What proves it works

- A table test of the step function. There is at least one row per transition in the
  steps table, plus the caps, a session that touched `verify.sh`, a revision request, a
  refused revision, blockers bouncing back to `work` exactly once, and a resume after a
  failure.
- Fakes of the daemon, Claude, git and gh for the action layer. They check which files
  are committed, in what order, and with what messages.
- Parsers for the three Claude answers, and for `tasks.md`, each table-tested with
  malformed input.
- A live run on the scratch repository, from `/run <one line>` in the TUI to an open PR.
