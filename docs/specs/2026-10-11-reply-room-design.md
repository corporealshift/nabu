# Reply room, and summaries without thinking

**Date:** 2026-10-11
**Status:** Approved by Kyle in conversation
**Amends:** `docs/specs/2026-10-08-context-size-design.md` (what compaction measures);
the summarize call.

## Problem

Strata refuses any request whose prompt plus `max_tokens` exceeds its context
(`requests are never truncated`). rho sized the reply cap from the last request's input
alone. The next request also carries the reply to it and every tool result since, so
the cap could be thousands of tokens too big.

With a 48K normal window this never showed: sessions summarized at about 40.8K, leaving
24K for a 16K cap. With the normal window raised to the full 65,536, two plan sessions
were refused at 49,933 and 49,869 tokens, and the run failed. The window was put back.

Summaries were the other cost. Qwen 3.8 on Strata wrote 4K to 38K tokens per summary,
almost all thinking: 3 to 10 minutes each, and one ran into the ten-minute timeout and
fell back to clearing tool results.

## Decision

- **Estimate the next request.** `nextRequestTokens` is the last request's input, plus
  its reply's output, plus the characters of every tool result, message, context and
  veto logged since, at 2 characters a token, plus 512 tokens.
  - Qwen's tokenizer measured 1.9 to 2.7 characters a token on tool output with its
    framing in the two refused requests. 2 plus the margin covers both.
  - After a summary the next request is far smaller and the log cannot say how much, so
    the estimate is 0, as the old code's stale value effectively was.
- **The reply cap uses it.** The room is the context window less the estimate.
- **Compaction uses it.** The stage is chosen from the estimate, and runs after the
  turn's tools, so a turn whose results carry the next request over the line is
  compacted before that request is built. The cap therefore always has at least the room
  above the summarize line.
- **A refusal that names the room is retried once with it.** Strata's 400 says `at most
  N here`; rho asks again with N and logs a notice. A refusal that names no room fails
  as before.
- **`summary_thinking: false`** per provider sends `chat_template_kwargs:
  {"enable_thinking": false}` on the summarize call only. Unset, nothing changes: a
  hosted API that knows no template arguments never sees the field.

## Rejected

- **Strata's `fit_max_tokens`.** It shortens the cap server-side, but only Strata has it,
  it is set in Strata's own config file, and rho would still compact too late.
- **Counting tokens exactly.** rho has no tokenizer for the model, and the estimate errs
  safe by design.
- **Retrying any 400 with a smaller cap.** A refusal that names no room says nothing about
  what would fit.
- **Turning thinking off for every turn.** The work turns are where thinking earns its
  time; the summary is a rewrite of what is already there.

## What proves it works

- `daemon/agent`:
  - `nextRequestTokens` counts the reply and what came since, and is 0 after a summary;
  - both refused requests, rebuilt from their logs, now get a cap that fits;
  - a refusal naming its room is retried once with it, and the session goes on;
  - summaries carry `NoThinking` only with `summary_thinking: false`, and only the
    summary call.
- `daemon/provider`: `NoThinking` puts `enable_thinking: false` in
  `chat_template_kwargs`, and a request without it has no such field.
- Live: with `normal_window` raised to 60000 and `summary_thinking: false`, a run's
  sessions compact at about 51K, no request is refused, and a summary takes under a
  minute.
