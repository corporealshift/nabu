# A run reflects before it ends

**Date:** 2026-10-11
**Status:** Approved by Kyle in conversation
**Amends:** `docs/specs/2026-09-30-orchestrated-runs-design.md` (a new last step) and
`docs/specs/2026-09-11-nabu-architecture-design.md` §11.4 (no curator on run steps).

## Problem

liftoff has had dozens of runs, and its memory holds nothing that was learned from them.
Two things went wrong:

- **The curator answered nothing.** A pass allowed 1200 tokens, thinking included, and
  the local Qwen thought through all of them. That is fixed separately
  (`memory.curator_max_tokens`, PR #163).
- **Even working, the curator is the wrong tool for a run.** It sees one session at a
  time, at most 24KB of it. A run is a group of sessions: plan, verify, tasks, a work
  session per task, fixes, and tries that were thrown away. The lessons live across them,
  and no single session sees them. For example: which task needed Claude to step in and
  what Claude said, what the check kept failing on, and what the final review blocked.
  A run's step sessions are also the noisiest input there is. One is a single try of
  one task, and it may be thrown away. The "in progress" memory a step session writes
  is about a try the runner may discard. That is how liftoff ended up with an "in
  progress" memory from a discarded session, offered to every later session.

## Decision

When a run ends, merged or failed, the runner starts one more session before it marks
the run `done` or `failed`: a **reflection**. It reads a digest of the whole run and
saves what a future session in the repository would want to know, with what went right
first. The run's outcome never depends on it.

Run step sessions no longer get the per-session curator. A run makes one reflection
pass instead of about ten narrow ones.

### What went right comes first

A small model repeats what fills its context, and looping is the extreme case. A memory
saying "don't do X" still puts X in front of the model. The prompt therefore:

- asks first for what worked: approaches that passed the check the first time,
  commands and patterns that worked, and fixes Claude suggested that then worked;
- asks that a lesson from a failure be written as what to do ("call `waitForIdle()`
  after typing into a field"), never as what to avoid;
- allows at most five saves, and prefers updating a memory on the same subject over
  adding a near-duplicate. The memory index is in front of the session, as it is in
  every session;
- says that saving nothing is a valid answer.

### The `reflect` step

- `reflect` is a session step. A run enters it from `merge` or `ci` instead of going
  to `done`, and from any failure instead of going to `failed`. A failure is recorded
  first (`failed_at`, `why`), so `reflect` keeps the outcome and hands it on: to
  `failed` with the original reason, or to `done`.
- `reflect` never fails a run and is never retried. If the session blocks, hits its
  cap, or is paused by a daemon restart, the runner logs it and the run goes to its
  outcome.
- A run with no step session has nothing to reflect on (a failed `setup`, for one),
  and goes straight to its outcome.
- The session runs in the run's worktree. Worktrees share the repository's workspace
  key, so its memories are filed with the repository's. Afterwards the runner resets
  the worktree to where it was before the step, so the session cannot leave changes
  behind.
- The turn cap is `reflect_turns` in the runner config, default 30.
- A goal waits for its run to be `done` or `failed`, so it waits for the reflection.
  That keeps one job at a time on the single local model slot, as now.

### The digest

The runner keeps a **history** on the run: one entry each time a step finishes. An
entry holds the step, the task for work, the session, and whether the step did its job
and why not. Some entries also carry a note, cut to a bound:

- the end of the check's output when the check failed;
- the final review's blockers;
- Claude's answer to a revision request;
- the CI failure.

The digest is built from the history and the step sessions' logs, and goes in the
prompt. It holds:

- the brief and the task list;
- how the run ended, with the PR;
- each step in order: its session, its try, its number of turns, and how it ended,
  with its note;
- every `claude.ask` in the run's sessions: which step and task asked, after how many
  turns, and Claude's answer. These are the most valuable lines, because each one is an
  outside look that got the model unstuck.

Each part is cut to a bound, and the whole digest to 60,000 characters.

A run resumed with `/run` keeps its history, so its next reflection sees both goes. A
memory saved under an existing name replaces that memory, so a lesson seen twice is
saved once.

### No curator on run steps

The runner puts a new label, `memory:no-curator`, on every session it makes, the
reflection's included. The memory module skips the curator pass and the "in progress"
memory for a session carrying it. Model-initiated `memory.save` still works, and the
session's writes are still committed at its end.

A dedicated label was chosen over the existing `unattended` one. Headless sessions and
the GitHub jobs are unattended too, and they keep the curator. The label is a
client-owned string, defined once in the runner and once in the module, like
`guard:no-push`.

## Rejected

- **Claude reflects.** It has better judgment and is faster, but each run would cost
  another Claude call. The runner's Claude is read-only, so its memories would come
  back as text that something then writes outside the tool API. Kyle chose the local
  model.
- **Only runs that hit trouble.** That would cost less, but it never records what went
  right, which is the part a small model needs most.
- **Keep the curator on step sessions.** Each pass sees one try, and there are about
  ten passes a run.
- **A daemon hook at run end.** The daemon does not know what a run is. Runs are client
  policy, so the reflection belongs in the runner.
- **Reflect alongside the next run.** The local model serves one request at a time, so
  the reflection would slow the next run down and add complexity.

## How it is proved

- Transition table rows:
  - `merge`, and `ci` passing or merged, go to `reflect`, then `done`.
  - A failure goes to `reflect` with `failed_at` and `why` set, then to `failed` with
    them unchanged.
  - A `reflect` that did not work changes nothing about the outcome, and is not
    retried.
  - A run with no step sessions skips `reflect`.
- History: each finished step adds an entry with its session and note, and notes are
  bounded.
- The digest holds the brief, the tasks, each step with its turns, every `claude.ask`
  answer, check failures, blockers and discarded tries. It stays within its bound.
- The prompt asks for what went right first, for lessons written as what to do, and
  for at most five saves, and allows only the memory tools.
- The runner's sessions carry `memory:no-curator`.
- The memory module makes no curator pass and writes no "in progress" memory for a
  labeled session, and still commits its own saves.
- Live: the next liftoff run ends with a `memory: N saved` commit from its reflection
  session.
