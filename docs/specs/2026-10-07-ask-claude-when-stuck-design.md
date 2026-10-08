# Asking Claude when a session has gone on too long

**Date:** 2026-10-07
**Status:** Approved by Kyle in conversation
**Amends:** the `claude` module (`daemon/modules/claude`).

## Problem

Task 4 of liftoff's "Navigation shell and Mission Control" run ran 8h40m and 344 turns.
The work was built and committed by turn ~150. Its last 5½ hours went round one failing
test file: about 15 rewrites of the whole file, several of them unchanged, and the same
"let me analyze all 6 failures" opening eight times. The cause was one fact it never
found (the view model's `init` launched on a scope the test could not reach). Asking
Claude would have found it at once. In 344 turns it called `claude.ask` zero times, and
the two loop notices it got were acknowledged and ignored.

A turn cap is not the answer: caps of 50 and 100 were removed on 2026-10-04 because they
cut off sessions that were still working (`2026-09-30-orchestrated-runs-design.md`).
Asking for help never cuts anything off.

## Decision

The `claude` module asks Claude on the model's behalf when a session has gone 100 turns
since a person last spoke, and again at 200. Then it stops asking.

### When

- **Turns** are assistant messages since the last `user` message, the same measure the
  loop module uses. Any user message starts the count again: Kyle's, a runner's prompt,
  or the runner's "you stopped short" message. So it applies to every session, attended
  or not. 100 turns without a word from anyone means nobody is steering.
- **At `auto_ask_after` turns** (default 100), and again at twice that, up to
  `auto_ask_max` asks (default 2) per stretch. `auto_ask_after: 0` turns it off.
- **Not in `ask` permission mode.** A person who approves calls is there to be asked,
  and a call needing approval would wait on them.
- **Not without the CLI.** If `claude` is not installed the module offers nothing
  already; this follows.

### How it reaches the model

Claude takes minutes; hooks get 30 seconds. And a `claude.ask` call logged when it
starts, with its result logged minutes later, would leave every request in between
carrying a tool call with no result, which providers reject. So the asking and the
recording are split:

1. **Ask in the background.** At `TurnEnd`, when the count reaches the mark and no ask
   for this mark is in flight or already in the log, the module builds the question and
   runs the CLI in its own goroutine, under the module's `timeout_seconds`. The model
   keeps working.
2. **Record at a turn boundary.** At `BeforeRequest`, when the answer has arrived, the
   module calls `claude.ask` through the host's tool caller with the same prompt. Its
   own `runAsk` finds that session's waiting answer for that prompt and returns it at
   once, so the hook stays within 30 seconds. The host logs an ordinary `tool_call` and
   `tool_result` pair with `source: module:claude`, adjacent, at a point where every
   other call has its result.

The model then sees a `claude.ask` call and its answer in the conversation, as if it had
asked. It stays there like any tool result, rather than vanishing after one request as a
`suffix` context block would. The transcript shows it, and the stats drill-down counts
it under `claude.ask`.

### The question

Built from the log since the person last spoke, at most 24 KB:

- a first line saying who is asking: "nabu is asking on behalf of a smaller local model
  that has spent N turns on this without anyone stepping in";
- **the task**: the user message that started the stretch, cut to 6 KB;
- **what it has been saying**: its last 10 assistant messages, each cut to 600 bytes;
- **what is failing**: the last 4 tool results with status `error`, or whose bash output
  says FAILED or `e:`, each cut to the last 40 lines;
- **where it keeps working**: the 5 files written or edited most often, with counts;
- **the ask**: "Read the repository. Say what is going wrong and what to do next, as
  specific steps: files, functions, commands. If the approach is wrong, say so."

Claude reads the repository itself, read-only, as `claude.ask` already allows.

### How it fails

- **Claude times out or fails:** the failure is recorded as the call's result, with the
  error the CLI gave, so the model and Kyle can see the ask was tried. The next mark may
  ask again.
- **The person speaks before the answer lands:** the waiting answer is dropped. The
  count has started again, and so has the reason for asking.
- **The session ends first:** dropped with the session.
- **The daemon restarts mid-ask:** the in-flight ask is lost. What has been asked is read
  back from the log (`claude.ask` calls with `source: module:claude` since the person
  spoke), so a mark already answered is never asked twice. A lost one is asked again at
  the next `TurnEnd`.
- **The gate refuses the recorded call** (a configured guard rule, say): the answer is
  not delivered, and the module logs why.

### Config

`auto_ask_after` (100) and `auto_ask_max` (2), next to the module's existing settings.

## Rejected

- **A turn cap that stops the step.** Tried and removed on 2026-10-04.
- **A notice telling the model to call `claude.ask`.** The model never used it in 344
  turns, and it acknowledged loop notices without acting on them.
- **Doing it in the runner.** It would cover runs only. In the module, it also covers
  goals, GitHub jobs and long interactive stretches.
- **Asking synchronously in a hook.** The 30-second hook limit, and the session would
  freeze for minutes. Raising the limit for every `BeforeRequest` would hide hung
  modules for minutes too.
- **Logging the call when it starts.** It leaves a call without a result in requests
  for minutes.
- **Delivering the answer as a `suffix` context block.** A suffix block reaches exactly
  one request, then it is gone.
- **Letting Claude fix the code.** The reviewer stays read-only, as the module decided.
- **Loop-module changes** (counting no-op writes, repeated opening sentences). Kyle
  turned these down: repeating itself is how the model reasons, and catching it would
  end sessions early.

## What proves it works

- `daemon/modules/claude`, table-driven, with a fake CLI as the existing tests use:
  - no ask before the mark; one at 100; a second at 200; none after `auto_ask_max`;
  - a user message starts the count again;
  - `ask` permission mode and `auto_ask_after: 0` never ask;
  - the answer is recorded at the next `BeforeRequest` as one adjacent call and result
    from `module:claude`, and `runAsk` returns the waiting answer without running the
    CLI again;
  - a mark already in the log is not asked again after a restart;
  - a failed ask records its error; an ask overtaken by a user message is dropped;
  - the question carries the task, the recent messages, the failures and the files, and
    stays under its cap.
- The full gate.
- Live: on the next session to pass 100 turns, the transcript shows the call and the
  model's next turns respond to it. The stats drill-down for `claude.ask` lists it.
