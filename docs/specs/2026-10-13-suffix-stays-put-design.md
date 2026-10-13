# Suffix blocks stay where they were sent

**Date:** 2026-10-13
**Status:** Approved by Kyle in conversation
**Supersedes:** "a `suffix` block applies to exactly one request" in
`docs/specs/2026-09-11-rho-architecture-design.md` §6 and `protocol/spec.md` §4
(`context`) and §6.1 item 5; the notes block "on every request" in
`docs/specs/2026-09-18-workspace-notes-design.md` §5.

## Problem

A suffix block is sent at the end of one request and left out of the next. So every
request that follows a suffix block rewrites the end of the one before it.

Strata, like llama.cpp on hybrid models, can resume a prompt only from a checkpoint it
saved: the last turn boundary, the end of the system prompt, and every 16K prompt tokens
(`G:\Strata-main\docs\DETAILS.md`, "Conversation cache"). A prompt that changes before
its end falls back to the nearest older checkpoint and reads everything after it again.

The notes block is a suffix on every request, and its text changes on every request
anyway, because it shows each note's age ("3m"). Session `01M4TE3Z` on 2026-10-13,
Qwen 3.8 Flash Next on Strata at 92K:

| | calls | median tokens read again per call |
|---|---|---|
| before the first note | 35 | 5 |
| after it | 167 | 8,548 |

About 1.5M tokens were read again for nothing. At the 140 tokens a second Strata reads
at, that is about 3 of the session's 7.2 model-hours. The cache stuck at fixed points
(68,697, then 7,453, then 25,885 tokens) while the prompt grew past them. Over five days
notes wrote 874 of the 1,006 suffix blocks; skills 77, answer 33, loop 22.

## Decision

- **A suffix block stays at its place in the log.** It is part of every request from
  the point it is appended until the next `summarize` compaction, rendered in log order
  among the messages and tool events, as a `user` message. Suffix blocks next to each
  other in the log are joined into one message, separated by a blank line. Every request
  is then the previous request plus what was logged since, which is the shape that read
  back 5 tokens a call.
  - `protocol/spec.md` §4 (`context`): a `suffix` block is part of the request from the
    point it is appended until the next `summarize` compaction, at its position in the
    log.
  - §6.1: item 5 is folded into item 4 (suffix `context` events after the last
    `summarize`, in log order). Item 6, outstanding vetoes, is unchanged: a veto is
    rare, once at the end of a turn, and not worth a change of its own.
  - `protocol/assemble.go` and `daemon/agent/request.go` follow; the
    `suffix_context` segment keeps its name and now appears in log position.
- **A module sends a suffix block only when it has something new to say.** Sending the
  same text again now costs context instead of replacing the last copy. The
  `RequestHook` comment in `daemon/module/module.go` and the architecture spec's module
  table say so.
- **Notes.**
  - The block is sent when the live notes (names, bodies, write times) differ from the
    last notes block in the log since the last `summarize`, and on the first request
    after one. An expiry is a change.
  - Each note shows when it was written (`written 2026-10-13 12:33`) instead of its age,
    so the same notes render the same text whenever they are rendered.
  - The comparison reads the last `module:notes` suffix block from the log, so it holds
    across a daemon restart.
- **Answer.** Its reminder is sent once, on the first request of a question turn,
  instead of on every request of it. It was repeated so it stayed at the end; it now
  stays in the conversation instead.
- **Skills, loop, watch** already send a block only when something happened, and need no
  change.

## Rejected

- **Fix notes alone** (notes in the prefix at session start and after each summarize, no
  protocol change). It covers 87% of today's suffix blocks and leaves the same trap for
  every other module, and for the next one written.
- **Fix it in Strata**, with a checkpoint in front of the trailing user turn. Strata is
  not ours, and llama.cpp behaves the same on hybrid models.
- **Keep one request per block, but put the block inside the last tool result.** The
  text still changes before the end of the prompt.
- **Keep the notes at the end because they are read last.** Being read last is what put
  a stale note after a newer instruction from the person in `01M4TE3Z`
  (`2026-10-13-summaries-chain-design.md`).

## How it fails

- Context grows by each block sent. Notes changed three times in `01M4TE3Z`, about 2K
  tokens in all, against 1.5M read again.
- An old version of a note stays visible until the next summarize. The newer one comes
  later in the conversation and carries its write time.
- A module that keeps sending the same block grows the request each time. The notes and
  answer tests pin "nothing new, nothing sent".

## What proves it works

- `protocol/vectors/assembly/02-prefix-and-suffix-contexts.json` updated, and a new
  vector in which each request's messages start with the previous request's: a suffix
  block, then a tool round, then another suffix block.
- `daemon/agent`: a request built after a suffix block and a tool round starts with the
  request built before it, message for message.
- `daemon/modules/notes`: unchanged notes send nothing; a write, a delete or an expiry
  sends one block; a summarize sends one on the next request; rendering is independent of
  the time it runs.
- `daemon/modules/answer`: one reminder per question turn.
- Live: a session on Strata that writes notes reads back a median under 2K tokens a call,
  from 8,548 in `01M4TE3Z`.
