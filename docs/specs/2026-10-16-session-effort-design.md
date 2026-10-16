# Session effort: how hard the model thinks, per session

**Date:** 2026-10-16
**Status:** Approved by Kyle in conversation
**Amends:** `protocol/spec.md` §3.1 and §7 (a new session option), and the runner's
config (a per-step effort map).

## Problem

Liftoff's plan and verify steps went from 5–10 minutes on Qwen3.6-35B-A3B to 1–3 hours
on Qwen3.8-Flash-Next served by Strata. Measured from the session logs of the
run-sorties-in-flight run (2026-10-16):

- **Almost all the time is the model.** Of 139, 60 and 100 minutes, tools took under 5;
  the rest was model turns (15–45 s each) and summaries.
- **The cache is fine.** 86–95% of prompt tokens were cached.
- **The output is not.** A plan session wrote 64K tokens and a verify session 122K,
  against 5–8K for the same steps on Qwen3.6. Thinking averaged 2,600 and 7,500
  characters a turn against 140, and single thoughts reached 50,000 characters.

Rho sends no thinking level, and Strata's config sets none, so its chat template falls
back to `xhigh` and adds "think carefully through the task, validate key assumptions,
consider plausible alternatives" to every system prompt. That suits a work session
writing code. It is wasted on a step that reads and writes a plan.

The only control rho has is `summary_thinking`, which turns thinking off for summaries.
Strata's config can set a level, but for every session at once.

## Decision

### Per session: `effort`

A new session option `effort` ∈ `none | low | medium | high | xhigh | max`. Absent (or
`""`) is the default and means **unset**: rho sends nothing and the server decides,
which is today's behaviour.

- **It is set at creation** like any option, recorded in the `session` event and in
  `options_change`, and **can change mid-session** with `set_option`. A change applies
  from the next model call. `set_option` with `""` clears it.
- **On the wire** it goes in `chat_template_kwargs`, the channel rho already uses for
  summaries, which llama-server and Strata both read:
  - `none` sends `enable_thinking: false`;
  - every other level sends `reasoning_effort: <level>`, passed through as written.
- **Summaries are not affected.** They follow the provider's `summary_thinking` as now.
- **The levels are the protocol's, their meaning the server's.** On Strata today the
  template knows `low`, `medium` and `xhigh`; Strata maps `high`, `xhigh` and `max` all
  to `xhigh`, and unset is also `xhigh`. So on Strata `high`, `xhigh` and `max` behave
  the same. They are kept distinct for servers that tell them apart. On llama-server
  with Qwen3.6 only `none` has an effect: its template does not read `reasoning_effort`.

The daemon records `effort` and sends it. Which session gets which level is decided by
whoever creates or sets the session, as with `context`.

### Choosing it

- **TUI:** `/effort none|low|medium|high|xhigh|max|default` sets it on the current
  session (`default` clears it); `/effort` with no argument says what it is. The header
  shows the level when one is set.
- **Android:** an effort control in the session's top bar, beside the context badge,
  offers the six levels and default. Goal and run homes are sessions, so the same
  control sets a goal's or a run's effort.
- **The runner:** `runner/config.json` gains an `effort` map keyed by step name:

  ```jsonc
  "effort": {"brief": "medium", "plan": "medium", "tasks": "medium",
             "verify": "medium", "reflect": "low"}
  ```

  A session the runner creates gets, in order:
  1. its home's `effort`, if the home has one;
  2. otherwise the map's entry for its step;
  3. otherwise nothing (unset).

  A run home copies the goal home's `effort` when it is created, as it copies `context`.
  So setting a goal's effort from a client makes every later step of every run under it
  use that level, and setting a run's home does the same for that run.

### Live config after this lands

The map above in `runner/config.json`. Work, fix and ci-fix stay out of it, so they keep
Strata's `xhigh`. Strata's own config is left as it is.

## How it fails

- **An unknown `effort`** in create or `set_option`: `rho_invalid_params`, and nothing is
  appended.
- **An unknown level, or a key that is not a step, in the runner's map:** the runner
  refuses to start and names the key, as other config errors do.
- **The runner cannot read a home** (archived or unreadable): the session gets the map's
  entry for its step, and the runner logs why. A step never fails over a setting.
- **A server that rejects `chat_template_kwargs` or the level:** the call fails as any
  provider error does, and the person sees it. Nothing is sent for an unset effort, so
  sessions that never set one cannot hit this.
- **An effort change during a model call:** that call finishes at the old level; the
  next uses the new one.

## Rejected

- **Setting `reasoning_effort` in Strata's config.** It changes every session at once:
  the planning steps get faster, but work sessions lose the thinking that pays off there.
- **A token budget per session** (`reasoning_budget_tokens`). Named levels mean the same
  thing to a person on every server; a budget is a number tuned to one model. Strata's
  config can still cap every request.
- **A per-provider default effort in rho's config.** The server's own config already
  does that.
- **The top-level OpenAI `reasoning_effort` field.** A second channel for the same thing,
  and llama-server does not read it.
- **Stopping at `high`.** Strata treats `xhigh` and `max` the same as `high`, but other
  servers do not, and the protocol should not be shaped by one server's template.
- **The step map always winning over a home's effort.** Then a person could not make one
  goal think harder without editing the runner's config.
- **A per-home step map** (`effort_steps` on a home). The most control, but a new option
  shape on the protocol and both clients, for a need nobody has yet.
- **Having the daemon copy `effort` from a parent.** The core gives `parent` no meaning
  (§3.1); copying is the runner's policy.

## Not doing

- The rust-gui client. It has no context-size control either.
- Effort for the GitHub watcher's review and comment sessions. They can be given a map
  of their own when someone measures them.
- Recording the effort on each model call. The `session` event and `options_change`
  already say what it was at any point in the log.

## What proves it works

- Protocol: `effort` round-trips through create and `set_option`; `""` clears it; an
  unknown value is `rho_invalid_params`; the spec, schema and a conformance vector change
  together.
- `daemon/provider`: each level becomes the right `chat_template_kwargs`; unset sends
  none; a summary request keeps `summary_thinking`'s behaviour whatever the effort.
- `daemon/agent`: a session's effort reaches the provider request, and a `set_option`
  reaches the next one.
- `clients/runs`: a home's effort beats the map; the map applies when the home has none;
  a step not in the map gets none; an unreadable home falls back to the map; a run home
  copies the goal home's effort; a bad map is refused at load.
- TUI and Android: the command and the control set the option (unit tests with the fake
  daemon each client already has).
- The full gate, and the Android build and unit tests.
- Live: with the map above, the next run's plan and verify sessions show `effort:
  medium` in their `session` event, average well under the 2,043 output tokens a call the
  last verify session wrote, and finish in a fraction of the time.
