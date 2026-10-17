# A summary can be the session's next turn

**Date:** 2026-10-16
**Status:** Approved by Kyle in conversation
**Amends:** the summarize call (`daemon/agent/compaction.go`); provider config
(`docs/configuration.md`). Builds on `2026-10-16-summary-length-design.md`.

## Problem

A summary is written from a fresh prompt: the summary instructions as the system
message, and the history rendered as one transcript in a user message. On a server that
caches the conversation, that prompt shares nothing with the request just before it,
so the server reads all of it again.

In liftoff's plan session `01M53EKP` on 2026-10-16, the turn before each summary
reused 64,000 of its prompt tokens from Strata's cache and read a few hundred new ones.
Each summary request then reused none:

| summary | prompt tokens | reused | reading took |
|---|---|---|---|
| 1 | 26,712 | 0 | 177 s |
| 2 | 41,469 | 0 | 263 s |
| 3 | 39,606 | 0 | 173 s |

Ten minutes of one session went to reading again what the server had just read.

## Decision

- **A provider setting, `summary_in_session`.** When true, a summary is asked for as
  one more turn of the session's own conversation: the request is the one the next turn
  would send (the same system message, messages and tools, built by `buildRequest`),
  with the summary instruction as a last user message. A server that caches the prompt
  reuses all of it and reads only what came after the last turn.
- **The instruction is the summary prompt reworded for a turn.** The same rules (what to
  keep, the length, notes and the person's words, the facts to preserve), with the
  framing changed: stop the work, the conversation above is what to summarize, the
  earlier summary is the system message's "Earlier conversation (summarized)" section,
  call no tools, output only the summary.
- **It goes with the session's effort and the summary's thinking setting.** `Effort` is
  the session's, `NoThinking` follows `summary_thinking`, and the cap is the summary cap
  where thinking is off, never more than the context has room for.
- **Anything wrong falls back to the transcript summary**, with a notice: an error,
  including a server that refuses the size; an empty answer; or a tool call instead of
  an answer. The fallback is what every summary is today, so the setting never makes a
  summary fail that would have succeeded.
- **Off by default.** It only pays where the server caches the prompt *and* renders the
  summary's prompt start the same as a turn's. Where it does not, the request is the
  whole context instead of a shortened transcript, and reading it costs more than today.

### Strata needs `effort_position: end`

Qwen's template puts the effort's instruction at the top of the system message:
`xhigh` (the default) and `low` add a sentence there, `medium` adds none, and thinking
off adds none. A summary without thinking in a session at `low` or the default
therefore differs from its turns in the first system message, and Strata's cache stops
there. With `"effort_position": "end"` in `strata-<model>.json` (Strata 0.1.39, #458),
every request's prompt starts as the default effort's and a `low`, `medium` or no-thinking
request has its level in a short turn before the answer, so the summary matches its
turns whatever the effort. Without it, turn this on only for sessions at `medium`.

## Rejected

- **Always on.** On a server without a prompt cache it costs more than today, and rho
  cannot tell from the outside.
- **`tool_choice: none`.** Strata, like most servers, drops the tools for it, and the
  tools are in the system message, so the cache would stop there too. The instruction
  says not to call tools, and a call falls back.
- **Thinking at the session's effort instead of `summary_thinking`.** It would match the
  prompt start without the Strata setting, but a session at the default effort would
  think at `xhigh` over its summary, which is what `summary_thinking: false` exists to
  stop.
- **Logging the summary turn as a turn.** It is a compaction, and the log already
  records it as one. A summary turn in the log would be summarized by the next one.

## Proof

- With `summary_in_session`, the summary request starts with exactly the messages and
  tools of the last turn's request, and ends with the instruction.
- It carries the session's effort, and the summary cap where thinking is off.
- An error, an empty answer, or a tool call falls back to the transcript request, with
  a notice, and the summary still lands.
- Off, the request is the transcript request.
- Live: with Strata at `effort_position: end`, a summary request in Strata's log reuses
  nearly all of its prompt and reads it in under 30 seconds.
