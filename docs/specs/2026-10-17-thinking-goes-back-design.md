# Thinking goes back to the model that wrote it

**Date:** 2026-10-17
**Status:** Approved by Kyle in conversation
**Amends:** `protocol/spec.md` §3.1 (`thinking`) and §6 (request assembly), the assembly
vectors; request building and size estimates (`daemon/agent`); provider config
(`docs/configuration.md`). Reverses the rule in commit `d1b7f0c` (#29) that thinking is
never sent back, for providers that opt in.

## Problem

`d1b7f0c` kept the model's thinking for the reader and ruled it out of every request:
"replaying a model's own reasoning to it is not something every provider accepts, and
nothing here depends on it."

Something now does. Qwen3.8's template, as Strata serves it, renders every earlier
assistant turn with its thinking (`preserve_thinking`, on unless a client turns it off):
`<think>\n{reasoning_content}\n</think>`, filled from the `reasoning_content` the client
sends. Rho sends none, so the model sees every turn it has taken with an empty thinking
block, which is not what it was trained on.

The cost shows in liftoff's plan session `01M54PSM` on 2026-10-17 (127 minutes, effort
`low`). Seven of its 70 calls wrote over 8,000 tokens each, and the ten largest were
59% of its output. Each one worked out the whole plan in thinking, 40,000 to 60,000
characters, ending "Let me run these greps". After the greps, the next call worked it
out again from the start, at least four times. After each tool call, nothing in the
request said the plan had been worked out.

`2026-10-17-plan-in-the-file-design.md` asks planning steps to draft in their file,
which helps on any provider. This is the fix where the provider takes it, and beyond
planning: a work session's reasoning about a failing test is lost the same way.

## Decision

- **A provider setting, `keep_thinking`.** When true, each assistant turn in a request
  carries the thinking logged before it, as `reasoning_content` on the OpenAI-compatible
  wire. Off by default: not every server accepts it, and it makes requests bigger.
- **Assembly lists thinking; the daemon decides whether it is sent.** `Assemble` stays a
  pure function of the log: a `thinking` event after the last `summarize` becomes a
  `thinking` segment in log order. When the provider keeps thinking, the daemon sends a
  thinking segment as the reasoning of the `message` after it, if that is the
  assistant's, and drops it otherwise: a turn interrupted before its reply was logged
  leaves thinking that the person's next message follows. Several thinking segments
  before one message are joined, in order.
- **Every turn since the last summary.** Not only those since the person last wrote,
  which was Qwen3's older rule: then a new message from the person would strip thinking
  from the turns before it and change the request in the middle, and a server's prompt
  cache could only resume up to there. Every request stays the one before it plus what
  was logged since. A summary still ends the life of everything before it.
- **Thinking as logged.** A passage the repeat watch dropped from the log
  (`2026-09-25-thinking-loops-and-skill-reminders-design.md`) is not sent either.
- **Size estimates count it.** The next request's estimate counts the last reply's
  thinking when the provider keeps it; today it leaves thinking out because a request
  never carries it. Compaction and the reply cap read the estimate, so both stay right.
- **Summaries are unchanged.** The transcript a summary is written from still leaves
  thinking out: the summary is of what was done and decided, which the messages and
  tool calls hold, and thinking would double the transcript.

## Rejected

- **A session option.** Whether thinking can go back is a property of the server and
  model. A session option can come later if a model wants it for some sessions only.
- **Sending only the last few turns' thinking.** Each request would drop thinking from
  a turn the one before it carried, and a prompt cache would resume no further than
  that turn.
- **Assembly that knows the provider.** Assembly is vector-tested as a function of the
  log alone; the provider's choice belongs to the daemon.

## Failure modes

- **The context fills faster.** In `01M54PSM` thinking averaged 9,434 characters a call.
  Kept, that is 2,000 to 4,000 tokens a call on top of today's requests, so summaries
  come sooner (and are shorter since `2026-10-16-summary-length-design.md`). The hope,
  to be measured, is that the model stops working the same thing out again, which is
  where most of that thinking went.
- **A server that refuses `reasoning_content`.** The call fails as any refused request
  does, with the server's error in the session. The setting is off by default, so only
  a provider someone turned it on for can meet this.

## Proof

- Vectors: a thinking event is a `thinking` segment between the prompt and the message
  it produced; one before a reply of tool calls only is a segment before that message.
- With `keep_thinking`, the request's assistant message carries the thinking logged
  before it, across tool-call turns, and the wire request has `reasoning_content`.
  Without it, no message carries any.
- The next request's estimate is larger by the last reply's thinking only with
  `keep_thinking`.
- Live: with `keep_thinking: true` on the `strata` provider, Strata's log shows each turn
  reusing the previous request's prompt as before, and the next plan session has no call
  that works the plan out again after a tool call.
