# Run decisions

**Date:** 2026-10-04
**Status:** Approved by Kyle in conversation
**Amends:** `2026-09-30-orchestrated-runs-design.md`: the plan step, the three Claude gates
and the PR body. A run still never stops to ask.

## Problem

A brief leaves things open. The run settles them without saying so, and nothing tells
Kyle what it chose.

Seen on 2026-10-04, in an orchestrated run of the bench task `11-merge-ranges` on Qwen3.6.
The task says to merge overlapping bookings, "think about what callers of a booking system
would expect". The plan got the edge the hidden check tests right:

> merging overlapping (including touching) intervals … The standard merge-intervals
> algorithm treats touching as overlap, which is the safer default.

Claude's plan review rewrote it:

> Touching ranges such as `[1,3)` and `[3,5)` share no instant … the brief asks only for
> overlaps to be merged. So touching ranges stay **separate**.

Claude's verify review then pinned that with `TestVerifyMergeTouchingStaysSeparate`. The
work sessions built what they were told, the check passed, and the final review raised
nothing. The run failed the hidden contract on exactly that edge.

There are two faults:
- **The default was wrong.** The reviewer chose the literal reading of the brief over
  what a user of the thing would expect.
- **The choice was invisible.** It was in `plan.md` and in `verify.sh`, but the PR would
  have said only that the check passes. Kyle would have found out from a bug.

## Decision

### Lean toward what users expect

When the brief leaves a behaviour open, every step that decides it chooses what the
people who use the thing would expect, not the narrowest reading of the brief's words.
The rule is in the prompts for `plan`, `plan-review`, `verify`, `verify-review`, `revise`
and `final-review`, in these words:

> When the brief leaves a behavior open, choose what the people who use this would
> expect, not the narrowest reading of the brief's words.

`tasks`, `work` and `fix` follow the plan and the check, so they need no rule of their own.

### The plan records its decisions

`plan.md` gets a section, `## Decisions`, with one entry for each behaviour the brief
leaves open. An entry says:
- the question;
- what the plan chose;
- the alternative;
- why the choice is what users would expect.

Only behaviour someone using the result could observe goes there. Implementation choices
stay in the body of the plan. If the brief settles everything, the section says so in one
line.

### The plan review records, and does not overwrite

`plan-review` keeps every entry in the section:
- **To change a decision,** it leaves the entry and adds
  `Changed by review: the plan chose X; the review chose Y, because Z.`, then moves the
  entry to the top of the section.
- **To add a decision the plan missed,** it marks the entry `Added by review`.

A disagreement between the plan and the reviewer is the clearest sign that the brief was
ambiguous, so it is the first thing Kyle reads.

If the plan had a `## Decisions` section and the review's rewrite has none, the runner
commits the rewrite with the plan's section appended, under a line saying the review
dropped it. The runner does not judge the entries. It only makes sure they do not
disappear.

### The check pins each decision

The `verify` prompt asks for one named test per decision. The comment above the test
names the decision it proves. `verify-review` checks that every decision has one. A
decision is then in force like everything else in the brief, and the PR can say which
test to change to reverse it.

### The PR reports them

The runner copies the `## Decisions` section from `plan.md` into the PR body, under
`## Decisions this run made`. It goes after the brief and before the final review's notes,
followed by:

> Each is pinned by a test in `verify.sh`. To change one, say so in a comment on this PR.

The comments job already handles comments on PRs labelled `nabu`, which every run's PR
is. Reversing a decision needs nothing new.

If `plan.md` has no section, the body says `The plan recorded no open decisions.`. A
section over 20,000 characters is cut there, with a pointer to `plan.md`. GitHub refuses
a PR body over 65,536 characters.

### The final review notes, it does not block

A decision the final review thinks goes against what users would expect becomes a
**note**, not a blocker. `verify.sh` pins the decision, so a blocker would send `work`
against a check it may not change, and the run would spend its revisions arguing with
itself. A note puts the disagreement in front of Kyle, which is what this design is for.

## How it fails

| What happens | Effect |
|---|---|
| The plan writes no `## Decisions` section | The review is asked to add one. If it does not, the PR says no decisions were recorded. |
| The review's rewrite drops the section | The runner appends the plan's section to the committed rewrite, as above |
| The model lists implementation trivia as decisions | A longer PR description. The prompt limits entries to observable behaviour. |
| A decision has no named test in `verify.sh` | `verify-review` adds one. If it does not, the decision is reported but not enforced. |

## Not doing

- **Stopping to ask.** The run stays unattended from start to PR. Kyle reviews the
  decisions at the end and changes them through PR comments.
- **A structured format.** The entries are prose in markdown. The runner copies the
  section and never parses the entries.
- **Decisions made during work.** `work` and `fix` sessions follow the plan. If they turn
  out to settle open questions too, that is a later change.

## Rejected

- **Stop and ask Kyle when the brief is ambiguous.** Kyle's call: be informed at the end
  instead, so the run stays unattended.
- **A separate `decisions.md`.** This was proposed first. A planning step writes exactly
  one file, and the runner throws away a session that changes any other. `plan-review`
  also rewrites exactly one file. A section of `plan.md` rides along with both. A second
  file would need both rules changed for no gain.
- **Making a contested decision a final-review blocker.** See "The final review notes, it
  does not block".
- **Leaning toward the literal reading.** It is the smallest change, and it lost the
  merge-ranges run. Kyle's call: lean toward what users expect.

## What proves it works

- **The section is extracted correctly:** with a section, without one, with sections
  after it, with CRLF, and when over the cap.
- **`prBody`:** includes the section and the line about comments when there is a section,
  and the "no open decisions" line when there is not.
- **A review that drops the section:** the committed `plan.md` has the plan's section
  appended.
- **Prompt tests:** the user-expectation rule is in all six prompts. The plan and review
  prompts describe the section and the `Changed by review` and `Added by review` marks.
  The verify prompts ask for a named test per decision.
- **Live:** rerun `11-merge-ranges` as an orchestrated run on Qwen3.6, in the scratch repo
  with a local origin, as on 2026-10-04.
  - `plan.md` has a `## Decisions` section with an entry for touching ranges, and any
    review change is marked.
  - The run's code passes the bench's hidden contract test.
  - The PR step cannot run there, since the repo has no GitHub remote, so the PR body is
    proven by the unit test.

## Plan

One PR, in this order. Each step leaves the gate green.

1. **Prompts:** the rule in the six prompts, and the section in the plan, plan-review and
   verify prompts. Proved by `TestSessionPrompts` and `TestClaudePrompts`.
2. **Extraction and the PR body:** `DecisionsSection` in `parse.go` and its use in
   `prBody`. Proved by the extraction and `prBody` tests.
3. **The dropped-section fallback** in the `plan-review` step. Proved by a runner test
   with a fake Claude whose rewrite has no section.
4. **The live run above,** reported in the PR.
