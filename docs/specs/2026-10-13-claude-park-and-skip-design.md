# A Claude gate parks when Claude does not answer, and can be skipped

**Date:** 2026-10-13
**Status:** Approved by Kyle in conversation
**Amends:** the runner's Claude gates (`docs/specs/2026-09-30-orchestrated-runs-design.md`,
`docs/specs/2026-10-06-goals-design.md`, `docs/specs/2026-10-12-run-replan-design.md`);
the run and goal status shapes (`docs/specs/2026-10-11-run-page-design.md`).

## Problem

A run asks Claude at five gates: plan-review, verify-review, revise, replan-review and
final-review. A goal asks it at two: breakdown and check. When the Claude CLI fails, the
gate counts a failed try, and after three tries, one poll apart, the run fails or the goal
blocks. A rate limit, the weekly usage limit, or an outage at Anthropic is not something
the run did wrong, and none of them is over in three polls. Session `01M4TE3Z` on
2026-10-13 was sent to find out why runs failed at Claude's gates. Rate limits were the
reason, and PR #175 made the reason readable. Kyle chose the behaviour then: "Park the
run at that gate and keep polling, no attempt counted, until Claude answers again".

Kyle also wants to get a run past a gate without Claude. While Claude is limited for the
week, a run parked at plan-review can only wait.

## Decision

### Parking

- **The runner asks for JSON.** `ClaudeCLI.Args` adds `--output-format json`. The CLI
  then prints one result object last. `Ask` returns its `result`, trimmed. An empty
  result is "claude said nothing", as before.
- **`ClaudeUnavailable{Reason}` is the error for Claude not answering.** `Ask` returns it
  for:
  - a result with `is_error` and an `api_error_status` of 429, the rate limit and the
    usage limit (`api_error: "usage_limit_reached"`);
  - 401, logged out: retrying costs nothing and works again once the owner logs in;
  - 5xx, including 529 overloaded: Claude is down;
  - a call that runs past its timeout. With the API unreachable, the CLI prints nothing
    and keeps retrying, so a timeout is what an outage looks like.

  The reason is the CLI's `result`, which for a limit is its own message ("You've hit
  your weekly limit · resets 2pm"), or the status when there is no result. The timeout's
  reason is "claude did not answer within 15m0s".
- **Every other failure counts a try, as today:** another 4xx, such as a bad model name;
  a CLI that exits without a result object, such as on a bad flag; an `is_error` result
  with no status; and an answer that does not parse, since Claude did answer.
- **A gate that gets `ClaudeUnavailable` waits.** It returns `Outcome{Wait: true,
  Parked: reason}`, or `GoalOutcome` likewise. A wait costs no attempt, so the gate is
  asked again at the next poll, with no limit.
- **Parking is shown, not silent.** `Run.Parked` and `Goal.Parked` hold the reason.
  `Transition` and `GoalTransition` set it from a waiting outcome and clear it on any
  other outcome; they stay pure. A wait that changes `Parked` is saved and shown, where a
  wait saved nothing before. The runner logs once when a run or goal parks ("waiting for
  Claude at plan-review: …") and once when Claude answers again, not at every poll.
- **The status shapes gain `parked`** (omitted when empty), on both the run and the goal.
  The field is new and optional, so `StatusVersion` stays 1. The app shows it in the run
  page's Now card and on the goal page as "Waiting for Claude: <reason>".

### Skipping

- **The owner asks with a label**, as `/run` and `/goal` do: `run:skip` on a run's home,
  `goal:skip` on a goal's. The TUI's `/skip` and the app's **Skip Claude's check**
  button set it, replacing the home's other `run:*` or `goal:*` labels. Both offer it
  only while the home's label names a gate that can be skipped, and the TUI says why
  when it cannot.
- **The runner takes it at the start of a poll.** For a run at a Claude gate it sets
  `Run.Skip`, then relabels the home. The gate checks `Skip` before anything else it
  would do for Claude, and passes as follows:

  | Gate | Skipped means |
  |---|---|
  | plan-review | the plan stands as written |
  | verify-review | verify.sh stands as written; the run that only feeds Claude is not done |
  | revise | verify.sh stays; the revision request is removed, and the next fix session is told the owner kept verify.sh |
  | replan-review | the plan stands; the request is removed, and the session is told to carry on with the plan. It counts as a refusal, so the cap still holds |
  | final-review | no blockers and no notes; the run goes on to its PR |
  | goal check | the round is met; the goal goes on to its PR. The goal's branch is still synced first |

  `Skip` is cleared by any outcome that is not a failure, so it never carries past the
  gate it was given at. A goal at breakdown cannot be skipped, since the breakdown writes
  the briefs, and neither can anything that is not at a gate. The runner logs that the
  skip did nothing and relabels the home.
- **A skip is recorded.** The gate's history row has the result "skipped by the owner",
  and the runner logs it. `Run.Skipped` lists each gate skipped at least once. The PR
  body says which gates Claude did not review, instead of "Claude reviewed the plan, the
  check and the finished work". A skipped goal check's verdict has `skipped: true`, and
  the goal's PR body says the owner skipped the check instead of giving a verdict.
- Skipping a parked gate clears `Parked`.

## Rejected

- **Matching the CLI's text** ("You've hit your…") to spot a limit. It breaks the first
  time the CLI changes its wording. The JSON status is the CLI's own classification.
- **Parking on any error from `Ask`.** A bad model name would park forever instead of
  failing with its reason.
- **A limit on parking**, failing after some hours. Kyle chose plain parking, and the
  reason is on the phone, so a park that never ends is seen.
- **A push notification when a run parks.** The status shows it; a notification can
  follow if the status turns out not to be enough.
- **Checking Claude before a run starts.** A run's first steps are the local model's, and
  are worth doing while Claude is limited; the run parks at its first gate.
- **Keeping a skip until the run reaches a gate.** It would skip a gate the owner never
  saw. A skip that arrives elsewhere does nothing.
- **`/run skip`.** `/run <text>` takes the text as the brief.
- **A setting that turns the reviews off.** That is a standing policy; this is a choice
  made while looking at one run.
- **Recording a skip as Claude's approval.** The PR would say Claude reviewed work it
  never saw.

## How it fails

- A gate that always takes longer than the timeout parks at every poll instead of
  failing after three tries. The page says "claude did not answer within 15m0s", and the
  owner can skip the gate or raise `claude.timeout`.
- Each poll of a gate that times out holds the runner for the whole timeout, as a gate
  that times out does today.
- A skip given while Claude is answering lands after the answer. By then the step has
  moved on, so the skip does nothing and is logged; it never skips the next gate.
- A CLI old enough to lack `--output-format json` fails on the flag at every gate, with
  the CLI's message as the reason, and the run fails after three tries as today.

## What proves it

- `exec_test`, with the fake CLI printing real result objects: a success gives the answer;
  429, 401 and 529 give `ClaudeUnavailable` with the CLI's message; 404 gives a plain
  error; no result object gives a plain error; a timeout gives `ClaudeUnavailable`.
- `transition_test` and `goaltransition_test`: a parking wait sets `Parked` and costs no
  attempt; the next outcome clears it; a successful outcome clears `Skip`.
- `runner_test`: a rate-limited plan-review is still there after five polls, parked, not
  failed; it moves on once Claude answers. A `run:skip` at each run gate passes it as the
  table says, with no call to Claude. A skip off a gate does nothing. The PR body names
  a skipped gate.
- `goalrunner_test`: a rate-limited check parks instead of blocking; a skipped check goes
  to the goal's PR with a skipped verdict; a skip at breakdown does nothing.
- TUI tests for `/skip`, Android tests for the skip labels, the button's visibility and
  `parked` in the status testdata.
- Live: one real gate through the new `Ask` once Claude answers.
