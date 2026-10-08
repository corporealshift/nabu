# Context size: per-provider compaction and a normal or large session

**Date:** 2026-10-08
**Status:** Approved by Kyle in conversation
**Amends:** the compaction stages in `2026-09-11-nabu-architecture-design.md` (thresholds
move from the daemon to each provider; stage 0 can be turned off), and `protocol/spec.md`
§3.1 and §7 (a new session option).

## Problem

Task 4 of liftoff's navigation run spent two sessions (398 and 436 turns) with its
context pinned just above the stage 0 threshold. Measured from their logs:

- **Clearing fired every 4 to 5 turns**: 73 and 99 times. The context grows about 800 to
  1,100 tokens a turn, and the median clear freed 1,100 to 1,500 tokens, about one turn's
  growth. Clearing shrinks only tool results. The model's own messages and its whole-file
  `write` arguments (730 KB in one session) stay, so it never got far below 70%.
- **Every clear broke the server's prompt cache.** A clear changes messages a few turns
  back, so the server reprocesses everything after them. The turn after a clear took 2×
  and 3.4× as long as the turns around it at the same size.
- **It made the model forget what it had read.** Results older than 4 turns are stubbed,
  and about half of all reads (45 of 95, 65 of 129) re-read a file read more than 4
  turns earlier. That fed the loops.
- **It kept the context in the slow range.** Over 60K tokens for most turns; above 80K a
  turn averaged 129 s against 20 s under 30K.

Without stage 0, the context would grow from a summary (about 15K) to the summarize
threshold and start again: about 90 turns per cycle at 128K, averaging less than the
90K it sat at. The request only grows between summaries, so the cache holds.

The thresholds are hardcoded daemon-wide, so none of this can be changed for the local
model without changing it for every provider. And some tasks do want the big window: one
that must hold a lot of documentation at once is better served by summarizing late.

## Decision

### Per provider: the thresholds and a normal window

Each provider entry in `config.json` gains three optional settings:

```jsonc
"local": {
  "context_window": 128000, // what the server holds; unchanged
  "normal_window": 64000,   // a normal session compacts as if the window were this
  "clear_at": -1,           // fraction; 0 = default 0.70; below 0 = never clear
  "summarize_at": 0         // fraction; 0 = default 0.85
}
```

- `normal_window` of 0, or one not below `context_window`, means normal and large are the
  same size.
- `clear_at` below 0 turns stage 0 off for that provider. A session that reaches
  `summarize_at` summarizes, as now.
- `clear_at` at or above `summarize_at` is refused at startup: stage 0 could never run.
- `keep_turns` stays a daemon default (4). With clearing off it does nothing, and
  nothing yet needs it per provider.
- A provider that sets none of these behaves exactly as today. The daemon-wide
  `agent.CompactionConfig` remains the fallback for unset values.

### Per session: `context`, normal or large

A new session option `context` ∈ `normal | large`, default `normal`.

- **The window compaction measures against** is `normal_window` for a normal session and
  `context_window` for a large one. The thresholds are fractions of that. For local: a
  normal session summarizes at about 54K, a large one at about 109K.
- **The reply cap** (`replyCap`) still uses `context_window`: it protects the server's
  real limit, which does not change with the option.
- **It can change mid-session** with `set_option`. Large to normal over the new line
  summarizes at the next turn end, the same as any session crossing the threshold.
- **It is set at creation** like any option, and recorded in the `session` event and
  `options_change`.
- **`context_window` in the `session` event stays the model's real window.** Clients go
  on showing fullness against it. A denominator that changed with the option would make
  the same number mean different things in one session.

The daemon records `context` and applies it to compaction. It is a mechanism, like
`compaction_enabled`: what a session is for, and so which size it gets, is decided by
whoever creates or sets it.

### Choosing it

- **TUI:** `/context normal|large` sets the option on the current session; `/context`
  with no argument says which it is.
- **Android:** tapping the context badge in the session's top bar offers normal or
  large. The badge marks a large session.
- **The runner:** every session it creates copies `context` from its parent: a goal's run
  homes from the goal home, and a run's step sessions from the run home. Marking a goal
  or a run's home large then makes all of its work large. The core attaches no meaning to
  `parent`, so the copying is the runner's, not the daemon's.

### Live config after this lands

`local`: `normal_window: 64000`, `clear_at: -1`. `strata` is left as it is until measured.

## How it fails

- **A config value out of range** (`summarize_at` above 1, `clear_at` at or above
  `summarize_at`, `normal_window` below 0): the daemon refuses to start and names the
  provider and the key, as other config errors do.
- **An unknown `context` value** in create or `set_option`: `nabu_invalid_params`, and
  nothing is appended.
- **A summary that cannot shrink the context enough** (a single huge message): unchanged
  from today. Summarizing is what already happens at the upper threshold.
- **The model is switched to a provider with different settings mid-session:** the
  thresholds follow the provider of the current model, read at each turn end, as
  `context_window` already is.
- **The runner cannot read a parent's options** (an archived or unreadable home): it
  creates the session `normal` and logs why, rather than failing the step.

## Rejected

- **Removing stage 0.** It is cheap and still right where the provider bills per token
  and caching works differently. It should not run on this model; nothing says it is
  wrong everywhere.
- **A raw token count per session** (`context_tokens: 90000`). Two named sizes are what a
  person or, later, a run step can sensibly choose between, and the numbers stay in one
  place.
- **Expressing normal as a lower fraction** (summarize at 0.42 of the real window).
  "64K is normal" says what is meant; a fraction of a fraction does not.
- **Changing the daemon-wide defaults instead.** Every provider would change with the
  local one, and a session still could not choose.
- **Clients showing fullness against the normal window.** See above: the denominator
  would change with the option.
- **Making the daemon copy `context` from a parent at creation.** The core gives `parent`
  no meaning (§3.1); this is a client's policy.
- **Clearing `write` arguments, or clearing down to a lower mark in one go.** Both would
  make stage 0 work better. With stage 0 off for the local model they are not needed now.

## Not doing

- The run step that decides a run's context size. This makes it possible: the step would
  set `context` on the run home, and the runner's copying does the rest.
- `keep_turns` per provider.

## What proves it works

- `daemon/agent`, table-driven:
  - a normal session summarizes at `summarize_at` × `normal_window`; a large one at
    `summarize_at` × `context_window`;
  - with `clear_at` below 0, no `clear_results` is ever appended, and the session
    summarizes at its threshold;
  - an unset provider value falls back to the daemon default;
  - switching large to normal over the line summarizes at the next turn end;
  - the reply cap still uses `context_window` in a normal session.
- `daemon/config`: the three settings load; out-of-range values are refused with the
  provider and key named.
- Protocol: `context` round-trips through create and `set_option`; an invalid value is
  `nabu_invalid_params`; the spec, schema and a conformance vector change together.
- `clients/runs`: a run home and its steps copy `context` from their parent; an
  unreadable parent gives `normal`.
- TUI and Android: the command and the menu set the option (unit tests with the fake
  daemon each client already has).
- The full gate, and the Android build and unit tests.
- Live: with `local` set as above, the next run's step sessions show summaries near 54K
  and no `clear_results`.
