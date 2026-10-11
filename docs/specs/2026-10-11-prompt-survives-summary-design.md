# The prompt survives a summary

**Date:** 2026-10-11
**Status:** Approved by Kyle in conversation
**Amends:** the module list (a new `prompt` module) and the summarize prompt.

## Problem

A summary covers the history up to its newest event, the prompt included. After one,
the request the model is working on reaches it only as the summariser's paraphrase,
written by the same model.

In run 01M4P8EH (liftoff, round 7) the plan step's prompt said to write `plan.md` and
nothing else. The step's second summary kept that in its first section, but listed
"Need to implement the plan… Need to commit once compile and tests pass" as
unresolved. The model followed the list: it built the feature, committed it, and was
only turned back by Claude's check-in 100 turns into the step. About 60 turns went to
work the step was told not to do, and then to undoing it.

## Decision

- **A `prompt` module.** After each summarize it returns the session's latest user
  message, word for word, as a prefix block under the heading "The prompt you are
  working on". It lasts until the next summary, which puts it back again, the way the
  claude module keeps Claude's answers
  (`2026-10-08-ask-survives-compaction-design.md`).
  - The latest user message, not the first: in an interactive session the latest is
    the request being worked on. A run's step has only one.
  - Capped at 8 KB, cut on a rune boundary with a line saying how much was left out.
    A step's prompt is about 2 KB.
  - It hands the summariser nothing to preserve: the prompt goes back as written.
  - Enabled by default; `enabled: false` in its config section turns it off.
- **The summarize prompt's item 4** now asks for what is unresolved *within what the
  user asked for*, and says that work the user did not ask for is not unresolved.

No protocol change: a prefix block is a `context` event, which every client already
handles.

## Rejected

- **Only the prompt change.** It asks the same model that drifted to keep the limit.
  The limit is already in the prompt, word for word, so put that back.
- **The first user message.** Right for a step, wrong for an interactive session whose
  first prompt is long since done.
- **Putting the prompt into the summary text in the daemon.** It is policy, so it
  belongs in a module, and a prefix block is how modules already re-inject.
- **Every user message.** An interactive session can have dozens, mostly done.

## What proves it works

- `daemon/modules/prompt`: the latest prompt is kept word for word, an earlier one is
  not, no prompt means no block, a long prompt is cut to valid UTF-8 and says so, and
  a disabled module keeps nothing.
- `daemon/agent`: with the real module, the request after a summary carries the prompt
  in the system message ahead of the summary, and not in the conversation.
- `go build ./... && go vet ./... && go test ./...`.
- Live: the next run step that summarizes shows the block in its request, and does not
  start work outside its step.
