# Planning steps draft in their file

**Date:** 2026-10-17
**Status:** Approved by Kyle in conversation
**Amends:** the plan, tasks and verify prompts (`clients/runs/prompts.go`).

## Problem

Liftoff's plan sessions stayed slow at effort `low`: 106, 32 and 127 minutes. In the
127-minute one, `01M54PSM` on 2026-10-17, 36 of 70 calls wrote under 500 tokens, but 7
wrote over 8,000, and the 10 largest were 59% of its output. Each large call was the same
thing: the whole plan worked out in the thinking, 40,000 to 60,000 characters ("Part 1:
Scrub dialog on the Launchpad…"), ending "Let me run these greps." After the greps, the
next call worked the whole plan out again from the start. It did so at least four
times, 6 to 8 minutes each.

Rho never sends thinking back (protocol spec §3.1), so after each tool call the model
has no record that it planned anything. Strata's Qwen template shows every earlier
turn with an empty `<think></think>` block.

## Decision

The plan, tasks and verify prompts each say: work it out in the file, not in your
thinking; write a first draft as soon as you know enough to, then check what you are
unsure of and improve the draft with edits; thinking can be dropped between tool calls.
The draft is then in the file and in the tool calls, which every later request carries.

The brief prompt does not get it: it writes from a transcript it is given, with no
reading in between. Work sessions do not: they write code, which is already in files.

## Rejected

- **Only sending thinking back.** It is the fuller fix and is specified separately
  (`2026-10-17-thinking-goes-back-design.md`), behind a provider setting. This prompt
  costs nothing and helps on any provider.
- **A turn limit for planning steps.** It stops a session that is slow; it does not make
  it faster, and a plan cut off is a plan done again.

## Proof

- The plan, tasks and verify prompts contain the instruction, naming their own file;
  the brief and work prompts do not.
- Live: the next plan session has no call over 8,000 output tokens that works the plan
  out again after a tool call, and plan.md is first written well before the session ends.
