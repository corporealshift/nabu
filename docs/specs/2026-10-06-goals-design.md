# Goals

**Date:** 2026-10-06
**Status:** Approved by Kyle in conversation ("build it")
**Amends:** `2026-09-11-nabu-architecture-design.md` §10.3 (who sets a session goal), and
`2026-09-30-orchestrated-runs-design.md` (a run's base branch, and a `merge` step).

Three parts, each its own PR, landing in order:
1. Remove the goal surfaces.
2. Goals in the runner.
3. Starting and watching goals on Android and in the TUI.

## Problem

A run takes one brief to one green PR. Kyle wants to hand nabu something broader, such as
"add offline sync to the Android client", and come back to it done. Today he would have to
split the goal into briefs himself, start each run once the last one had merged, and
judge at the end whether the whole thing was done.

Meanwhile the existing "goal" (`/goal` in the TUI, `nabu run --done-when`) sits unused.
Kyle drives nabu mostly from his phone, through runs. A goal on one long session is the
shape the local model does worst at (`2026-09-30-orchestrated-runs-design.md`, Rejected).

## Decision

### Part 1: the session goal stays, its surfaces go

The `goal` event, `set_goal`/`clear_goal` and the judge at the stop gate stay. Runs need
them: a `work` session's goal, judged in a fresh context, is how the runner tells a task
an earlier one already did from a session that stopped short. The GitHub comments job sets
one too. From here on the docs call it the **session goal**. Only the runner and the
watcher set it.

Removed:
- `/goal <text>` and `/goal` in the TUI;
- `nabu run --done-when`, and its line in the usage text.

Clients still *show* a session goal, because work sessions have one. In Part 3, `/goal`
comes back meaning a goal as described below.

### Part 2: goals

A **goal** is a broad piece of work that `nabu runner` breaks into briefs and takes to one
PR through as many runs as it needs. Claude plans and judges it. The local model does
every run.

**Where it starts.** A goal's home is a session labeled `goal:requested`, whose
description is the goal text. Like a run's home, it is never prompted: a prompt would start
it working in Kyle's clone. Part 3 adds the ways to make one.

**The goal branch.** Each goal has a branch, `nabu/goal-<slug>`, in a worktree beside
Kyle's clone, as a run has. A goal's runs branch from it and open their PRs against it.
When a run's PR is green, the runner merges it into the goal branch, and the next run
starts from the result. At the end, one PR goes from the goal branch to the default branch.

**The steps:**

| Step | Who | Does |
|---|---|---|
| `setup` | runner | Fetch. Branch `nabu/goal-<slug>` from the default branch in a new worktree. Commit `.nabu/goals/<slug>/goal.md`, and push the branch so runs can start from it. |
| `breakdown` | Claude | Read the goal and the repository, and return the done-when list and the first round's briefs. The runner commits `roadmap.md` and pushes. |
| `runs` | runner | One run for each brief of the round, in order, one at a time. A run whose PR is green is merged into the goal branch. |
| `check` | Claude | Judge the goal branch against `goal.md` and the done-when list. Met: go to `pr`. Unmet: Claude's new briefs are the next round. |
| `pr` | runner | Open one PR from the goal branch to the default branch, labeled `nabu`. If it is already open, update its body. |
| `ci` | runner | Watch the final PR's checks as a run does. Green or merged: `done`. Failing: back to `check`, with the failure. |
| `done` | | |
| `blocked` | | Waiting for Kyle. See below. |

**Breakdown.** Claude gets `claude -p` in the goal worktree, allowed only Read, Grep and
Glob, exactly as at a run's gates. It returns a fenced `json` block:

```json
{"done_when": ["a reader can …", "…"],
 "briefs": [{"title": "Sync queue in the data layer", "brief": "…"}]}
```

The prompt asks for 2 to 6 briefs in the order they must be done. Each brief must stand on
its own as a run's brief: what to build, how it is used, and what it must not break, since
the run sees only its brief. Each must be small enough for one PR. The done-when list is a
handful of outcomes a person could check. It is not a script (see Rejected).

**Runs inside a goal.** Each brief becomes an ordinary run, with three differences:
- Its home is created by the runner, with the goal's home as its `parent` and the brief as
  its description. So the phone shows goal → runs → steps.
- Its base is the goal branch, not the default branch. It branches from
  `origin/nabu/goal-<slug>` and opens its PR against that branch.
- Its PR carries no label. The watcher neither reviews it nor answers comments on it: it
  is merged as soon as it is green, and Kyle reviews the goal's final PR instead.

After `ci` passes, the run goes to a new step, **`merge`**: the runner runs
`gh pr merge <n> --squash`, then the run is `done`. Merging is the runner's, never a
model session's, and only ever into a goal branch. A PR found already merged at `ci` is
`done` as before.

The rest of the run is unchanged: plan, the three Claude gates, tasks, `verify.sh`, the
fix loop and CI fixes.

**Check.** At the end of a round, or early when a run fails, the runner brings the goal
worktree up to `origin/nabu/goal-<slug>` and asks Claude. The prompt gives:
- the goal and the done-when list;
- every round so far, with each brief and how its run ended: the PR, the final-review
  notes, or the failed step and its reason;
- the final PR's failed checks, when that is why it is here;
- the line `git diff origin/<default>...HEAD` shows the goal's whole change.

It returns:

```json
{"met": false, "reason": "…", "briefs": [{"title": "…", "brief": "…"}]}
```

`met: true` needs no briefs. `met: false` with no briefs cannot be acted on, so it counts
as an answer that did not parse. The runner commits the verdict to `roadmap.md`.

**Rounds.** A goal gets 5 rounds by default (`goal_rounds` in the runner config). A check
that is unmet after the last round blocks the goal with Claude's reason.

**When a run fails.** A run only fails after its own retries are spent, so a failure means
the brief, as written, did not work. The round ends early and goes to `check`, which sees
the failure and can write a brief that goes around it. If the next run fails as well, the
goal is `blocked`: two failures in a row mean re-planning is not getting anywhere.

**Blocked, and resuming.** A blocked goal waits for Kyle. The reason is on its home, as
below. Asking for the goal again (`goal:requested`, Part 3) resumes it:
- If its description changed, the new text is the goal from now on. It is committed to
  `goal.md` and goes into every later Claude call. This is how Kyle adds guidance: he edits
  the goal.
- A goal blocked at `setup`, `breakdown`, `pr` or `ci` retries that step. A goal blocked by
  failed runs or by the round cap goes to `check`, with its failures and rounds counted
  afresh.

**Progress on the phone, with no new protocol.** The runner keeps the goal home's task list
(`nabu.session.update_tasks`) as the roadmap:
- one task per brief, `pending`, `in_progress`, `done` or `failed`, with the run's PR, or
  its failure, in the note;
- one task per check, `Round N check`, with Claude's verdict in the note;
- a blocked goal has a last task, `blocked`, whose note says why.

The Android client and the TUI already draw a session's tasks. The home's labels give its
step, as a run's do: `goal:<step>` and `goal:round:<n>`.

**Slots.** A goal holds no job slot itself. Its current run holds one while a session runs,
as any run does. Goals, runs and the watcher's jobs share `max_jobs`.

**The final PR.** The body has:
- the goal;
- the done-when list;
- Claude's last verdict;
- each round's runs, with links to their merged PRs;
- the signature and marker.

It carries the `nabu` label, so the watcher reviews it and answers Kyle's comments on it
as on any PR. Pushes from comments jobs go to the goal branch. A later check, if there is
one, starts from there.

### Part 3: starting and watching goals

- **Android:** a **Goal** action beside **Run**, on any session that is not a step. It
  asks for the goal text, sets it as the description, then adds `goal:requested`. On a
  blocked goal, the same action offers the current text for editing. It never sets a
  session goal and never prompts. It is online only, as Run is.
- **TUI:** `/goal <text>` does the same, and `/goal` on a blocked goal resumes it.
- **Both:** the session list nests three levels, goal → runs → steps. A goal's home shows
  its status from its labels, for example `goal: runs, round 2`.

## How it fails

| What | Then |
|---|---|
| Claude cannot be reached, or its answer does not parse, at `breakdown` or `check` | Retried at the next poll, 3 times, then `blocked` at that step |
| The goal branch has no CI, because the workflows only run on PRs to the default branch | A run's PR with no checks is taken as green after 10 minutes, as today. `verify.sh` still had to pass, and the final PR gets the real CI. |
| `gh pr merge` fails, say because the repository does not allow squash merges | Retried 3 times, then the run fails at `merge`, and the goal handles that as any failed run |
| The goal branch moves while the goal writes `roadmap.md`, because a run merged in between | The push is refused; the goal fetches and tries again at the next poll |
| The default branch moves during a long goal | Nothing happens until the final PR. A conflict there is Kyle's, as for a run. |
| Kyle closes the final PR | The goal is `blocked` at `ci` |
| The runner restarts | Goals and runs resume from their saved state, in `<root>/runner/goals/` and `<root>/runner/runs/` |

## Not doing

- Goals from GitHub issues.
- Running a goal's runs in parallel.
- Kyle approving the breakdown before runs start: the roadmap is on the phone as soon as
  it exists, and the goal can be stopped at any time.
- Re-planning between runs inside a round.
- A goal-level `verify.sh`.
- Deleting the merged runs' branches.

## Rejected

- **Removing the session goal entirely.** Runs use its judge to tell "task already done"
  from "stopped short", and comments jobs use it.
- **A PR to the default branch per run, waiting for Kyle to merge each.** It keeps him in
  the loop, but the goal moves only as fast as he merges, which is not what "come back to
  it done" means.
- **Stacked PRs,** each run branching from the last. Every comment on an early PR would
  mean rebasing every later branch.
- **The local model planning or judging the goal.** Breaking work down is where it is
  weakest, and judging its own work is what the run design keeps it from doing.
- **Kyle approving each breakdown.** He chose hands-off.
- **A goal-level check script.** For a broad goal it would be either CI, which every run's
  PR and the final PR already run, or text-grepping, which the runs spec bans.
- **A separate `nabu goals` process.** It would duplicate the runner's Claude, git and gh
  code, and have to watch run labels from outside. Goals live in the runner beside runs.
- **Goals in the daemon.** Policy belongs in clients and modules (invariant 4), as the runs
  spec said of a run engine.
- **Labels on the goal's runs' PRs.** A review of a PR that is merged minutes later is work
  nobody reads.

## What proves it works

- A table test of the goal step function: a round met, a round unmet, a run failing into
  `check`, a second failure in a row to `blocked`, the round cap, a final PR failing CI back
  to `check`, and resuming from each kind of block.
- The run transition gains rows for `merge`: a goal run's `ci` pass goes to `merge`, a run
  without a goal goes to `done`, and a PR found merged is `done` either way.
- Fakes of Claude, gh and git checking that:
  - a goal's runs branch from the goal branch;
  - their PRs target it and carry no label;
  - the merge goes into the goal branch and never into the default branch;
  - the final PR targets the default branch with the label.
- Parsers for the breakdown and check answers, table-tested with malformed input.
- Live: a goal on the scratch repository that needs at least two runs, started from the
  phone, ending in one final PR.
