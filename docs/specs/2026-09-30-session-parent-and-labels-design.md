# Sessions with a parent and labels, and `/run`

**Date:** 2026-09-30
**Status:** Approved by Kyle in conversation
**Amended 2026-09-30:** the brief is the session's `description`, a third option, and
never its goal. See "The brief" below.
**Part 1 of 4** of orchestrated runs (`2026-09-30-orchestrated-runs-design.md`). This is
the only part that changes the daemon or the protocol.

## Problem

A run is a home session plus one session for each step. From the TUI, Kyle wants to
start a run and to watch it. Today neither is possible:
- **No way to say "run this".** A session cannot carry a request to the runner. The
  only client-writable state is goal, tasks and three options.
- **No way to group a run's sessions.** A step session looks like any other. The list
  would show a dozen unrelated sessions per run, each in its own worktree.

## Decision

Two session options, added to the protocol the way options already work.

### `parent`

- A session ID, set **only at creation**: `nabu.session.create {options: {parent}}`.
- Recorded in the `session` event's `options`.
- `set_option` with `parent` is `nabu_invalid_params`, because a session's parent never
  changes.
- The daemon checks that the parent exists when the session is created, and nothing
  else. It attaches no meaning to it: it does not archive children with the parent and
  does not stop them with it. That would be policy, which belongs to the runner.

### `labels`

- A list of strings.
- It can be set at creation, and replaced with `set_option {key: "labels", value: [...]}`,
  which appends `options_change` as with every option.
- The daemon stores and projects labels and attaches no meaning to them.
- A label is at most 64 characters, from `[a-z0-9:_./-]`. A session has at most 16
  labels. That keeps a label machine-readable, and keeps a list row a list row.
- The runner uses `run:requested`, `run:<step>`, `run:done` and `run:failed`
  (part 2). Anything else can use its own prefix.

### The brief: `description`

- Text, at most 16,000 bytes.
- It can be set at creation, and replaced with `set_option {key: "description"}`, which
  appends `options_change`.
- The daemon records it and attaches no meaning to it. In particular, unlike a goal, it
  starts nothing.
- An orchestrated run's brief is its home's description.

### The protocol

In `protocol/spec.md`, the JSON schema and a conformance vector, together:
- **§3.1 `session`:** `options` may carry `parent` and `labels`; a missing one means none.
- **§3.1 `options_change`:** `key` gains `labels`.
- **§5, the projection:** `options.parent` and `options.labels` follow the existing rule
  (the `session` options, then each `options_change`).
- **§7.2, `SessionSummary`:** gains `parent?` and `labels?`, so a list can group and show
  them without fetching each log.
- **§7.3 and §7.13:** accept them as above.
- **A projection vector:** a session created with a parent and labels, then relabeled
  twice, projects to the last labels and the same parent.

Readers that do not know the new fields ignore them. The Android client's decoder
already tolerates unknown fields, and will be checked for it.

### The TUI

- **The sessions list groups by parent.** A session whose `parent` is in the list is
  drawn indented under it rather than on its own. A home session shows its `run:<step>`
  label as its status, for example `run: fix (3/10)`. Attempt counts come from a label
  the runner sets alongside the step label, for example `run:attempt:3/10`.
- **`/run`** is a new command:
  - `/run <text>` sets the current session's `description` to the text and labels it
    `run:requested`. The runner takes it from there.
  - Plain `/run` labels it `run:requested` without a description. The runner's `brief` step turns
    the conversation into one.
  - On a session labeled `run:failed`, `/run` sets `run:requested` again, and the runner
    resumes the run (part 2).
  - `/run` is listed in `/help`.
- **Opening a step session works like opening any session.** Its parent's short ID is
  shown in the status line, so the path back up is visible.

## How it fails

| What | Then |
|---|---|
| `create` with a parent that does not exist | `nabu_invalid_params`. The session is not created. |
| `set_option parent` | `nabu_invalid_params` |
| A label that is too long, has a character outside the set, or is one too many | `nabu_invalid_params`. Nothing is appended. |
| `/run` with no runner running | The label sits there. The home shows `run:requested` until a runner starts and picks it up. Nothing is lost. |
| The parent is archived and the child is not | The child is listed at the top level, since its parent is not in the list. |

## Not doing

- Any daemon behaviour keyed on labels or parents.
- A run view in the TUI beyond grouping and the status label. If watching runs needs more,
  it gets its own spec.
- The Android and Rust clients. They receive the fields and can group later.

## Rejected

- **A run object in the daemon.** Kyle chose a runner process. Two plain options give the
  runner what it needs without the daemon knowing what a run is.
- **Tags as a new event type.** Options already have creation, change events, projection
  and a method, so labels need no new event type.
- **The brief as a label.** A brief can be pages long, and a label is a list row.
- **The brief as the session's goal.** This spec first chose it. But `set_goal` on an idle
  session starts the loop (§7.11). In the first live run of part 2, the home session began
  working on the brief in the clone and committed there, alongside the run's own
  worktree. The brief is now `description`: an option the daemon records, never acts on,
  and that is up to 16,000 bytes long.
- **The TUI talking to the runner directly.** That would make a second process boundary
  for clients (invariant 7). Everything a client does goes through the daemon.

## What proves it works

- The projection vector above, run by `go test ./protocol/`.
- `daemon/api` tests:
  - create with a parent and labels;
  - a missing parent;
  - `set_option labels` appends `options_change`, and the summary carries the labels;
  - `set_option parent` is refused;
  - invalid labels are refused.
- TUI tests:
  - the list indents children under a parent that is in the list, and does not when the
    parent is absent;
  - the status label renders;
  - `/run <text>` sends `set_option description` and `set_option labels`, and the session
    stays idle. `/run` alone sends only
    the label.
