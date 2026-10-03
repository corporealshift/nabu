# Orchestrated runs on Android

**Date:** 2026-10-01
**Status:** Asked for by Kyle ("make the orchestrator flow work on Android"). It mirrors the
TUI behaviour approved in `2026-09-30-session-parent-and-labels-design.md`.

## Problem

Runs could be started and watched only from the TUI. Part 1 left the Android client
receiving `parent`, `labels` and `description` without using them. On the phone, a run's
step sessions were a dozen unrelated cards, and there was no way to start a run.

## Decision

The phone does what the TUI does, using the same protocol fields and the same labels.
Nothing changes on the daemon or the runner.

### The list

- **Grouping:** each step session (a session whose `parent` is listed) is drawn straight
  after its home, indented. A step whose home is not listed stands on its own.
- **Run status:** a home shows its run status from its labels, for example
  `run: fix (3/10)`, beside its state.
- **Titles:** a card's title is its last prompt. A run's home is never prompted, so its
  title is the first line of its brief, its description. Titles are one line of words:
  a step's prompt opens "Do task 2 of 4 of this run:" and a blank line, which left the
  card saying nothing else.

A card's parent and labels come from the mirrored log: the `session` event and every
`options_change`, projected. So nothing changes in the database, and the list stays
right offline.

### A session

- **Status:** under the top bar, a home shows its run status, and a step shows "Step of:
  <home>", which opens the home.
- **The brief:** a home's description is shown above its transcript. A home is never
  prompted, so without this it would show nothing.
- **Run:** an action in the top bar, on any session that is not itself a step. It asks
  for an optional brief.
  - With a brief, the brief is set as the session's description, then its labels get
    `run:requested`.
  - With none, only the labels change, and the runner's `brief` step writes the brief
    from the conversation.
  - On a failed run, the same action resumes it.
  - It never sets a goal, because a goal starts the session working (protocol §7.11).
- **`/run [brief]`** typed in the composer does the same, as in the TUI. It is a command,
  never sent as a prompt.
- **Online only:** starting a run needs the daemon. Offline, it says so rather than
  queueing, because a run's labels are not a prompt the outbox can retry later.

## Rejected

- **A separate run screen.** The home session already is the run's page: its status, its
  brief, and its steps under it in the list. If watching runs needs more, it gets its own
  spec, as part 1 said for the TUI.
- **Run columns in the sessions table.** A parent and labels projected from the mirrored
  events need no migration, and stay right however the events arrive.

## What proves it works

- **Unit tests:**
  - `runLabels`, `runStatus`, `groupByParent`, `parseRun`, and card titles;
  - `startRun` against the fake daemon: the description and then the labels, and never
    `set_goal` or `send_prompt`;
  - options projected from mirrored `session` and `options_change` events;
  - the shared projection vectors, now including `parent`, `labels` and `description`.
- **On the emulator, against a scratch daemon:**
  - a finished run's steps are grouped under a home named by its brief, and labeled
    `run: done`;
  - "Step of:" opens the home;
  - Run with a brief sets the description and `run:requested`, and the session stays idle;
  - the runner then picks the run up, and its progress shows on the phone.
