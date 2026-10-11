# Run page

**Date:** 2026-10-11
**Status:** Approved by Kyle in conversation
**Amends:** `protocol/spec.md` §3.1 (a `status` event), §5 (the projection's `status`),
§6 (never sent), §7.21 (`tree`), and a new §7.27 (`nabu.session.set_status`); the
runner; the Android session screen.

## Problem

A run's or goal's home on the phone shows a conversation and a list of tasks. Kyle
cannot tell from it where a run is, how it got there, or what the goal has done so
far. That means asking Claude to read `~/.nabu/runner/runs/*.json`, which lives on
the desktop. The Stats screen for a run home is empty, because the work happened in
its child sessions.

Kyle wants three things, in this order: where it is right now, how it got here, and an
overview at the goal level. Cost is not one of them. The phone comes first.

## Decision

### What the pages show

A home with a status (see Protocol) opens on its status page.

- Every home with a status opens there, whoever made it, and its top bar has **Chat**,
  which shows today's screen, unchanged. Chat's top bar then has **Status** to come
  back. (Changed while building: this said only created homes open on status and
  prompted ones get a Run | Chat toggle. One rule is simpler, and "Run" already names
  the button that starts a run.)
- A home with no status, including every home from before this change, shows exactly
  what it shows today.

**Run page, top to bottom:**

1. **Header.** The brief's title. Below it, a line such as
   `work · task 2 of 3 · attempt 2 · 42m in this step`. Below that, the PR link, and
   "Part of: <goal>" when the run belongs to a goal. Tapping the goal opens its Goal
   page.
2. **Now.** A card for the step session that is running:
   - its state;
   - the turn and the turn cap;
   - when it last did anything;
   - how full its context is.

   Tapping it opens the session. A step done by Claude, or one waiting for a slot, says
   so instead.
3. **Timeline.** Every finished step, oldest first, then the current one. Each row
   shows ✓ or ✗, the step (and task), how long it took, and the result in one line,
   for example "check failed: 3 tests" or "final review: 2 blockers". Claude's gates
   and CI are rows too. Tapping a row with a session opens that session.
4. **Stats:**
   - wall time, model working time, turns, tokens in and out;
   - Claude asks, fixes, verify revisions and CI fixes;
   - turns per task as bars, with retries marked, so the task the run struggled with
     stands out.
5. **Brief.** Collapsed at the bottom.

**Goal page:**

- A header: the goal's text, then `round 6 of 10 · runs`, and the PR once there is one.
- The rounds, newest first. Each round shows its check verdict (collapsed) and its
  briefs. Each brief shows its state (merged, failed, dropped, running or pending) and
  its PR link. Tapping a brief that has a run opens that run's Run page.
- Stats for the whole goal:
  - runs merged and failed;
  - time per round, for rounds whose verdicts carry a time;
  - total turns, working time and tokens.

### Protocol

**A `status` event, §3.1:**

```jsonc
{"source": "client", "kind": "run", "data": { … }}
```

- `kind` is `run` or `goal`. Anything else is invalid.
- `data` is a JSON object, at most 64 KB once encoded. The daemon checks its size and
  that it is an object, and nothing more: what a run is belongs to the runner, as what
  a label means belongs to whoever set it.
- Each event is a full snapshot. The latest one is the status.

**§5 projection.** It gains `status: {kind, data}`, the last `status` event, and leaves
it out when there is none, as `goal` does. A client reads the status from the projection
it already keeps.

**§6.** `status` joins the list of events never sent to the model. It is for people;
the step sessions get what they need from their prompts.

**§7.27 `nabu.session.set_status {session_id, kind, data}` → `{event_id}`.**

- Appends a `status` event with `source: client`.
- `invalid_params` when `kind` is not `run` or `goal`, or `data` is not an object or is
  over 64 KB.
- Allowed in any session state, and it starts nothing, as `update_tasks` does.

**§7.21 `nabu.session.stats {session_id, tree?}`.**

- With `tree: true`, the result is the session and every session under it, by `parent`,
  at any depth: a goal home, its run homes and their step sessions.
- The totals are summed the way `nabu.stats` sums sessions.
- `members` lists each session's own `{session_id, parent, labels, turns,
  working_seconds, tokens: {input, output}, started_at, last_event_at, archived}`, so
  the phone can put numbers on timeline rows by session id. (Changed while building:
  this said `sessions`, which the summed totals already use for the count.)
- Archived sessions count, as in `nabu.stats`, because their work happened.
- Without `tree`, the result is unchanged.

The spec, the JSON schema, and the conformance vectors change together:

- a `status` event validates;
- one with a bad `kind` does not;
- a `status` event is never assembled into a request;
- the projection's `status` is the last one.

### The snapshots (the runner owns these)

The runner builds a snapshot from its own state. It sets it on the home only when the
snapshot differs from the last one it set, the way `Tasked` already works for a goal's
task list. Each shape carries `version: 1`. A shared fixture,
`clients/runs/testdata/status/*.json`, is decoded by a Go test and by the Android
parser's test, so the two cannot drift.

**Run:**

```jsonc
{"version": 1,
 "title": "In-Flight notes: data, state and actions (no UI)",
 "step": "work", "task": 2, "tasks": ["Notes in Room", "Notes in state", "Actions"],
 "attempt": 1, "since": "2026-10-11T17:58:46-04:00",
 "session": "01M4PFQC…", "waiting": false,
 "goal": "01M498J1…", "goal_title": "build the app based on the designs…",
 "pr": {"number": 41, "url": "https://github.com/…/pull/41"},
 "fixes": 1, "ci_fixes": 0, "revisions": 1,
 "failed_at": "", "why": "",
 "history": [{"step": "plan", "session": "01M4P8EN…", "ok": true,
              "result": "", "why": "", "at": "2026-10-11T17:26:52-04:00"}],
 "brief": "# In-Flight notes…",
 "started": "…", "updated": "…"}
```

- `title` is the brief's first heading, or its first line.
- `task`, here and in `history`, counts from 1, and is left out when there is no task.
  The runner's own index counts from 0.
- `tasks` holds the titles from `tasks.md`, once there is one.
- `since` is when the current step began. `Run` gains a `Since` field for it, set
  whenever the step or the task changes.
- `history` is `Run.History` without `note`. Each `result` and `why` is cut to 300
  characters.
- A step's duration is its `at` minus the previous record's `at`, or minus `started`
  for the first.
- Claude's gates and CI are already records in the history.

**Goal:**

```jsonc
{"version": 1,
 "text": "build the app based on the designs in the design directory",
 "step": "runs", "round": 6, "rounds": 10, "since": "…",
 "briefs": [{"title": "…", "round": 6, "state": "done", "run": "01M4…",
             "pr_url": "…", "failed_at": "", "why": ""}],
 "verdicts": [{"round": 5, "met": false, "reason": "…", "at": "…"}],
 "pr": {"number": 0, "url": ""}, "why": "",
 "started": "…", "updated": "…"}
```

- The briefs carry titles and not their text. The text is on each Run page.
- `rounds` is the goal's round allowance.
- `Verdict` gains an `At` field. Verdicts recorded before that have none, and their
  rounds show no time.
- A goal's `since` is a new `Goal.Since`, set when its step changes.

**Size.** A snapshot over 64 KB loses its oldest history rows first, then its oldest
verdict reasons, until it fits. The run's history is already bounded at 200 records.

### Android

- A status parser for both shapes, tested against the shared fixture.
- The Run page and Goal page as described, built from the projection's `status` and
  one `nabu.session.stats {tree: true}` call, repeated once a minute while the page is
  open.
- Opening on status, with Chat and Status to move between the two.
- An unknown `version`: the page says the status is from a newer runner, and offers
  Chat.

### Not now

- **The TUI.** It comes in a later plan and uses the same data.
- **Status for runs and goals that finished before this ships.** No backfill: their
  homes look as they do today.

## Rejected

- **Reusing `tasks` for status.** Tasks have no timestamps, and the timeline would end
  up as free text in task titles.
- **The daemon reading the runner's files.** The runner is a client. The daemon knowing
  where a client keeps its state breaks that boundary.
- **The phone working everything out from child sessions.** Claude's gates, CI, and the
  reasons a step was discarded have no session to read.
- **A typed run schema in the protocol.** That makes the daemon know what a run is. The
  runner owns the shape, and the shared fixture keeps the phone honest.
- **Patches instead of full snapshots.** The page reads only the latest snapshot, as
  with `tasks`. With notes left out and reasons cut, a run's snapshots total well under
  a megabyte.
- **Per-step stats from the runner.** The daemon already measures every session. The
  runner counting too would mean two numbers that disagree.

## How it fails

- **The runner cannot set a status.** It logs the error and carries on. Status is for
  display and never fails a run.
- **A snapshot is too big.** It is trimmed as above. If it still does not fit, the
  runner logs and sets nothing.
- **An older phone build** shows a `status` event as an unknown type. The phone is
  updated together with the daemon, which is fine for one user.
- **The `tree` call fails.** The page shows the status without stats, and says why.

## What proves it works

- `clients/runs`, table-driven:
  - snapshots built from recorded run and goal states;
  - cutting to 300 characters;
  - the 64 KB trim;
  - setting only on change;
  - `Since` moving with the step and the task.
- The shared fixture is decoded by Go and by Kotlin.
- `daemon/stats`: `tree` totals equal the sum over the tree, with descendants found at
  any depth and archived sessions counted.
- `daemon/api`: `set_status` with each `invalid_params` case.
- Conformance vectors for the event, the projection and assembly.
- `go build ./... && go vet ./... && go test ./...`, `gofmt -l .`, and the Android
  unit tests.
- On the phone, against the live daemon:
  - the liftoff goal's page shows its rounds;
  - a run's page shows its timeline, its live step and its stats;
  - tapping through to a session works.
