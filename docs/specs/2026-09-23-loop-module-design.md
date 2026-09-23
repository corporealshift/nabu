# The loop module

**Date:** 2026-09-23
**Author:** Kyle (corporealshift), with Claude
**Status:** Proposed in the PR that adds it. It builds part B of
`docs/proposals/2026-09-23-breaking-repetition-loops.md`; part A (tools that say when
nothing changed) is its own PR.

## 1. The problem

The model loops: a call, a thought, the same call again. The worst case in the logs is 32
writes of the same 1,259 bytes to `error.rs` in 13 minutes, with "I've been stuck in a loop"
written before nearly every one. Knowing it was looping did not help. What broke the loop
was a call that returned something new.

Repeating a call is not by itself a loop, though. Re-running a build after an edit is how a
fix is checked, and polling CI is how a run is waited on. In the logs, 54 of 81 repeated
observing runs saw their output change along the way. A detector that treats a repeat as a
loop would get in the way of the work it is meant to protect.

## 2. Decision

A `loop` module (`ToolGate`, `RequestHook`) sorts a repeat into one of three cases. Only the
first is treated as a loop:

| case | what it is | response |
|---|---|---|
| **identical change** | the same `write` or `edit`, same arguments, same result, and nothing else has touched the file | a notice, then a refusal, then the session stops `blocked` |
| **change not landing** | the same check failing with the same output, with changes made in between | a notice; never refused |
| **watching** | the same check, same output, nothing changed in between | a hint about `wait`, at a high threshold; never refused |

- **Everything is derived from the log** since the person's last message (§14.3: per-session
  state lives in the log). A word from the person starts every count again.
- **Only the model's calls count.** A module's calls are its own business.
- **Arguments are compared canonically**, so key order and spacing don't hide a repeat.
- **What resets an identical change:** a successful change to the same path, or any
  workspace-changing command (it could have touched the file).
- **A check "failed"** if its status is an error, or if it is a command whose output reads as
  an error. A build piped through `head` exits 0 whatever the build did. Search and view
  commands (`grep`, `cat`, `git diff`, …) don't count this way: their output may contain
  "error" because that is what was searched for.
- **Explicit waiting is never counted:** a command that `sleep`s or `watch`es, and the `wait`
  tool.

**Every message carries facts and changes each time.** A fixed nudge is one more thing to copy,
and "you're right, I'm looping" was already the model's own attractor. The notice names:
- the call and how many times it ran;
- how long since the first run;
- the result it keeps getting;
- the last failing check and its first error line;
- the files that error points at.

This is the one the module produces at the third `error.rs` write, from the replay in §5. It
names the error the model eventually found by itself, 13 minutes later:

> `write crates/breezeway-server/src/error.rs` has now run 3 times since the person's last
> message, with the same arguments and the same result each time ("wrote 1259 bytes to
> crates/breezeway-server/src/error.rs"). Nothing else has changed
> crates/breezeway-server/src/error.rs in the 52 seconds since the first. The last failing
> check was `bash cargo build --package breezeway-server 2>&1`, which said "error[E0433]:
> cannot find module or crate `breezeway_server` in this scope" (at
> crates\breezeway-server\src\routes\auth.rs:6, …). Repeating it cannot change anything, and
> the next identical call will be refused.

**Notices and refusals reach the model in two ways:**
- A notice is a suffix `context` event (invariant 3). An info `notice` is appended beside it,
  because the clients show notices and don't show context.
- A refusal is an ordinary denied `tool_result`, beginning `denied: loop:`. That prefix is how
  the module recognises its own refusals when it reads the log back.

### Thresholds (`modules.loop.*`)

| key | default | meaning |
|---|---|---|
| `enabled` | `true` | |
| `identical_after` | 3 | identical changes that run; the notice comes with the last of them, and the next is refused |
| `halt_after` | 2 | refusals of one change before trying it again stops the session |
| `not_landing_after` | 3 | identical failures, with changes between, before a notice |
| `watch_after` | 5 | identical outputs, nothing changed, before a hint; repeated at every multiple |

## 3. Core mechanism: `Halt`

A module could not stop a session before this change. `ToolGate` could only allow, deny, or
ask, and `UI.Ask` fails at once with no client attached, which is exactly when a loop runs for
an evening. So `module.Decision` gains **`Halt`**:
- It refuses the call as `Deny` does, and the rest of the model's batch does not run.
- The session then moves to `blocked`, with `Verdict.Summary` as the reason and a warn
  `notice` saying `stopping: …`.
- The phone and the TUI already show a blocked session, and a message resumes it.

This is mechanism with no opinion in it: core doesn't know why a gate halted. It is a Go
interface addition, not a wire change: `state_change` to `blocked` with a free-text reason
already exists. `Halt` short-circuits the other gates, like `Deny`.

## 4. The `wait` tool

Most polling is the model running the same command turn after turn. `wait {command, until?,
interval_seconds?, timeout_seconds?}` polls inside the daemon and returns once:
- **When it returns:** when the output differs from the first run, or when it matches `until`,
  or at the timeout (default 300 s, at most 1,800 s).
- **Cost:** one call instead of ten, and one turn of context instead of ten.
- **Loop detection:** a repeated identical check becomes stronger evidence of a loop, because
  real waiting has somewhere better to go.

`wait` is a built-in beside `bash` and shares its runner. **Guard judges `wait` as `bash`**,
by every rule written for `bash`, so it is no way around them. `module.ChangesWorkspace`
treats it the same way.

## 5. Checked against the logs

`NABU_LOOP_REPLAY=~/.nabu/sessions go test ./daemon/modules/loop/ -run Replay -v` replays
every session log, archived ones included, through the module. On 2026-09-23 it covered
42 sessions and 2,589 tool calls:

- **Refusals landed only on writes and edits:** 7 repeated changes, including the 32×
  `error.rs`, the `lib.rs` loops, and a failed edit to `golden.rs` retried unchanged.
  **0 refusals of any other call.**
- **The `error.rs` loop:**
  - it started at 21:48:01;
  - the first refusal would have come at 21:49:18;
  - `halt_after` would have stopped the session at 21:50:06;
  - the real loop ran until 22:01:29.
- **6 change-not-landing notices.** Each named a build or test failing identically after
  1–4 edits, with the error's first line and location.
- **12 watching hints:**
  - 5 identical reads of a file nothing had changed;
  - `cargo build` re-run with no edits;
  - repeated `git diff`.

## 6. Rejected

- **Treating any repeated call as a loop.** Two-thirds of repeated checks were the model
  working (§1).
- **Denying a check.** A check is how the model finds out whether it is done. Even a check
  that keeps failing is only commented on.
- **Stopping through `UI.Ask`.** It fails with no client attached, and a modal question is
  the wrong shape for "this session stopped".
- **Oscillation** (A, B, A, B to one file) isn't detected: each write changes the file, so no
  single change is identical. It didn't appear in the logs. If it does, it's a fourth case.
