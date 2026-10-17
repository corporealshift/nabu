# Summaries have a length

**Date:** 2026-10-16
**Status:** Approved by Kyle in conversation
**Amends:** the summarize call (`daemon/agent/compaction.go`).

## Problem

Liftoff's plan session `01M53EKP` on 2026-10-16 took 124 minutes. Its session log and
Strata's log for each request, matched call for call (116 of 116), show where they went:

| | minutes |
|---|---|
| reading new prompt tokens over 116 turns | 43 |
| writing the 116 replies | 43 |
| **writing 3 summaries** | **36** |
| tools | under 1 |

Each summary was 48,087 to 53,128 characters, about 12,000 to 13,000 tokens. With
`summary_thinking: false` none of that was thinking: Strata wrote 14,298, 15,444 and
15,921 tokens at about 30 tokens a second, 8 to 10 minutes each. The summary then goes
into every later request's system message. A 13,000-token summary is a seventh of the
92,000-token window before the agent has read anything, so the window fills sooner and
the next summary comes sooner. Since summaries chain
(`2026-10-13-summaries-chain-design.md`), each carries the last forward, and they grew:
12,021, 12,959, 13,282 tokens.

Nothing asks for less. The summary prompt lists what to keep and asks for specifics; it
names no length, and the request has no cap.

## Decision

- **The prompt names a length:** at most about 2,000 words, because the agent re-reads
  the summary with every request. Facts it is told to preserve verbatim are kept whole
  even when they take it past that.
- **A summary written without thinking is capped at 6,144 tokens.** 2,000 words of
  this text (paths, identifiers, commands) is 3,000 to 4,000 Qwen tokens, so the cap
  leaves room to overrun the target without being cut, and bounds the worst case at
  about 3½ minutes at 30 tokens a second instead of 10.
- **A summary written with thinking keeps the provider's cap.** Thinking counts against
  `max_tokens`, so a cap sized for the summary alone would cut it off mid-thought.
- **A summary cut off by the cap is kept**, with a warning notice saying so. What it has
  is the history; falling back to clearing tool results would keep the whole range in
  the request, the opposite of what was asked for.

## Rejected

- **A setting.** The length is about what the next request carries, which is the same
  for every provider. A constant until a provider needs another.
- **Asking again when the cap cuts a summary.** Another 3 to 4 minutes for the part
  after the cut. The warning makes a cut visible; if it is common, the target is wrong.
- **A smaller target.** 2,000 words keeps the five sections the prompt asks for with
  file names and errors in them. The run's own files (brief, plan, tasks) hold the rest,
  and the Current state block carries the person's words and the tasks verbatim.

## Proof

- The summary request asks for at most about 2,000 words.
- With `summary_thinking: false` its `max_tokens` is 6,144; with it unset, the
  provider's.
- A summary that finishes with `length` lands, and a warning notice names the cap.
- Live: the next plan session's summaries are under 4,000 tokens in Strata's log
  (`N generated`), and each takes under 3 minutes to write.
