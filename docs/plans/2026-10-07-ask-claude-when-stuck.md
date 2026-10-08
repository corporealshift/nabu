# Asking Claude when a session has gone on too long: plan

Spec: `docs/specs/2026-10-07-ask-claude-when-stuck-design.md`. One branch,
`auto-ask-claude`, off main, with one commit per step. Each step leaves the gate green.
Everything is inside `daemon/modules/claude`: no core, protocol or client change.

## 1. The stretch and the question

Pure functions over a log. Nothing calls them yet.

- **Changes:** `daemon/modules/claude/stuck.go` and `stuck_test.go`.
  - `stretch(log) []protocol.Event`: the events after the last `user` message.
  - `turns(events) int`: assistant messages in it.
  - `asked(events) int`: `claude.ask` tool calls in it with `source: module:claude`.
  - `question(events, turns) string`: the prompt in the spec, with its parts and their
    caps (task 6 KB, 10 messages × 600 bytes, 4 failures × 40 lines, 5 files), at most
    24 KB overall. Cuts never split a rune. A failure is a `tool_result` with status
    `error`, or a bash result containing `FAILED` or a line starting `e:`.
- **Proves it:** table-driven tests:
  - the stretch starts after the last user message, and is the whole log without one;
  - turns and asks are counted within it only, and a model's own `claude.ask` is not
    counted as an automatic one;
  - the question names the turn count and carries the task, the last 10 messages, the
    last 4 failures, the 5 most-edited files with counts, and the closing ask;
  - a huge log still gives a question under 24 KB, cut on rune boundaries.
  - The full gate.
- **Depends on:** nothing.

## 2. Asking in the background, recording at the boundary

- **Changes:**
  - `claude.go`:
    - keep the `module.Host` from `Init`;
    - read `auto_ask_after` (100, and 0 turns it off) and `auto_ask_max` (2);
    - make the CLI call a field, `ask func(ctx, dir, prompt) (string, error)`, which
      defaults to the current exec code. `runAsk` uses it, so tests can stand in for
      the CLI without a subprocess;
    - keep the waiting answers per session: prompt, answer or error, and the turn
      count the ask was made at;
    - `runAsk` returns a waiting answer for that session and prompt at once and
      forgets it, before running anything.
  - `stuck.go`:
    - `TurnEnd`: skip if the feature is off, the session is in `ask` mode, an ask for
      this session is in flight or waiting, or `turns < after × (asked + 1)`, or
      `asked ≥ max`. Otherwise build the question and run `m.ask` in a goroutine under
      the module's timeout. The goroutine's context is not the hook's.
    - `BeforeRequest`: if a waiting answer exists and the stretch still has the turn
      count it was asked at or more (no user message since), call `claude.ask` through
      `host.Tools().Call` with the same prompt. A failed ask is delivered the same way:
      the waiting entry holds the error and `runAsk` returns it. If a user message came
      in between, drop it. Returns no blocks.
  - `README.md`, the "Asking Claude" section: the two settings, what triggers an ask,
    and that each one spends Claude Code allowance.
- **Proves it:** tests in `stuck_test.go` with a stand-in `ask` and a fake host whose
  tool caller calls `m.runAsk` and records the call and result into the fake session's
  log:
  - no ask at 99 turns; one at 100; none at 150; a second at 200; none at 300 with
    `auto_ask_max: 2`;
  - `auto_ask_after: 0` and `permission_mode: ask` never ask;
  - the answer is recorded at the next `BeforeRequest` as one call and result from
    `module:claude`, adjacent, and the stand-in `ask` ran once, not twice;
  - a user message before delivery drops the answer, and the count starts again;
  - a failing stand-in `ask` records the error as the call's result;
  - a log that already holds the automatic ask for 100 (a restart) does not ask again
    at 101;
  - no second ask starts while one is in flight.
  - Run with `-race`.
  - The full gate.
- **Depends on:** 1.

## 3. Live

- Build and install the daemon at a gap, as with the stats work.
- **Proves it:** the next session to pass 100 turns since anyone spoke shows a
  `claude.ask` call from `module:claude` in its transcript. Its next turns respond to the
  answer. The phone's Stats drill-down for `claude.ask` lists it.
- **Depends on:** 2, merged.

## Not doing

- Loop-module changes (turned down: repeating is how the model reasons).
- A turn cap or a time budget (removed 2026-10-04).
- Asking more than `auto_ask_max` times in a stretch, or escalating to stopping the
  session.
