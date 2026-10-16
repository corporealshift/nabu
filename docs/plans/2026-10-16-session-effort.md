# Session effort: plan

Spec: `docs/specs/2026-10-16-session-effort-design.md`. One branch, `session-effort`, off
main, with one commit per step. Each step leaves the gate green. Steps 3 to 5 each depend
on 2 and not on each other.

## 1. The provider sends an effort

Mechanism only. Nothing sets an effort until step 2, so no request changes.

- **Changes:**
  - `daemon/provider/provider.go`: `Request` gains `Effort string`. Empty sends nothing.
  - `daemon/provider/openai.go` `toWire`: `NoThinking` or `Effort == "none"` sends
    `chat_template_kwargs: {enable_thinking: false}`; any other non-empty effort sends
    `{reasoning_effort: <effort>}` as written. `NoThinking` wins over `Effort`, so a
    summary never thinks when `summary_thinking` is false.
- **Proves it:** `daemon/provider/openai_test.go` table test over `toWire`: empty sends no
  `chat_template_kwargs`; `none` sends `enable_thinking: false`; each of `low`, `medium`,
  `high`, `xhigh`, `max` sends `reasoning_effort` with that value; `NoThinking` with
  `Effort: "high"` sends `enable_thinking: false` only. The full gate.
- **Depends on:** nothing.

## 2. The `effort` session option

Contract and its implementation together, as the spec requires.

- **Changes:**
  - `protocol/types.go`: `Options.Effort string` (`effort,omitempty`); constants
    `EffortNone`, `EffortLow`, `EffortMedium`, `EffortHigh`, `EffortXHigh`, `EffortMax`;
    `ValidEffort` (empty is valid and means unset).
  - `protocol/project.go`: `options_change` with key `effort` applies it; `""` clears.
  - `protocol/validate.go`: `effort` in `session` options and `options_change` values is
    checked with `ValidEffort`.
  - `protocol/spec.md`: §3.1 (`effort` in `options`, unset means the server decides,
    the wire mapping, that the server gives the levels their meaning), the
    `options_change` key list, §7.3 defaults, §7.13 keys and the invalid-value rule.
  - `protocol/schema/event.json`: `effort` in the options object with its enum (`""`
    included), and in the `options_change` key enum.
  - `protocol/vectors/projection/10-effort.json`: a session created with `effort: low`,
    changed to `high`, then cleared with `""`; the projection ends with no effort.
  - `protocol/vectors/validation/21-effort-invalid.json`: `effort: "extreme"` is refused.
  - `daemon/agent/manager.go`: `CreateOptions.Effort`, validated in `Create`;
    `SetOption` accepts `effort` (a string; `""` allowed) with the same check, message
    `effort must be none, low, medium, high, xhigh, max or empty`.
  - `daemon/api/handler.go`: `CreateSessionOptions.Effort`, passed through.
  - `daemon/agent/request.go` and `runner.go`: `buildRequest` takes the session's
    effort and sets `Request.Effort`. `compaction.go`'s summary request leaves it
    empty, so summaries keep `summary_thinking`'s behaviour.
  - `clients/go-tui` and `clients/android` projections: read `effort` from options and
    `options_change` (Android `protocol/Events.kt`, `protocol/Projection.kt`), so steps 4
    and 5 have it.
- **Proves it:**
  - The conformance vectors pass in Go (`protocol/vectors_test.go`) and on Android (the
    client's vector test).
  - `daemon/api`: create with `effort: medium` records it in the `session` event;
    `set_option` with `effort: extreme` is `rho_invalid_params` and appends nothing;
    `set_option` with `""` appends an `options_change` that clears it.
  - `daemon/agent`: with the fake provider, a session created `effort: low` sends
    `Effort: "low"`; after `set_option` to `xhigh` the next request sends `xhigh`; a
    summary request sends no effort.
  - The full gate, and the Android unit tests.
- **Depends on:** 1.

## 3. The runner chooses each step's effort

- **Changes:**
  - `clients/runs/config.go`: `Config.Effort map[Step]string` (`effort`). `LoadConfig`
    refuses an unknown step (not one of the `Step` constants that start a session:
    brief, plan, verify, tasks, work, fix, ci-fix, reflect) or a value `ValidEffort`
    refuses, naming the key.
  - `clients/runs/daemon.go`: `Home` returns the home's `Options.Effort` alongside
    `Context`. `Create` and `CreateHome` take an `effort string` and send it in options
    when it is not empty (`stepOptions` gains it).
  - `clients/runs/runner.go` `begin`: replace `contextOf` with one read of the home that
    gives both its context and its effort; effort is the home's if set, else
    `Cfg.Effort[r.Step]`. An unreadable home logs why and uses the map's entry.
  - `clients/runs/goalrunner.go`: a run home copies the goal home's effort as it copies
    its context.
  - `clients/runs/ports.go` and the test fake: the new parameter.
  - `README.md`: the runner's `effort` map, with the measured reason (plan and verify
    on Qwen3.8 at Strata's default `xhigh`).
- **Proves it:** `clients/runs` tests with the existing fake daemon:
  - a home with `effort: high` gives a plan session `high` even though the map says
    `medium`;
  - a home with none gives the plan session the map's `medium`, and a work session
    (not in the map) none;
  - a goal home with `effort: low` gives its run homes `low`;
  - a home whose state cannot be read gives the map's entry and a log line;
  - `LoadConfig` refuses `{"effort": {"plann": "low"}}` and `{"effort": {"plan":
    "extreme"}}`, naming the key.
  - The full gate.
- **Depends on:** 2.

## 4. TUI `/effort`

- **Changes:** `clients/go-tui/commands.go`: `/effort` says the current level, or
  "default" when unset; `/effort none|low|medium|high|xhigh|max` calls `set_option`;
  `/effort default` sends `""`; anything else is a usage line. The command table and
  `/help` list it. `view.go`: the status line shows the level when one is set, next to
  "large". `update.go`/`tui.go`: the new action, as `actContext` does it.
- **Proves it:** `clients/go-tui/effort_test.go`, modelled on `context_size_test.go`: each
  form gives the right action or note; the fake daemon sees `set_option` with `key:
  effort` and the value (`""` for default); the status line shows `medium` after the
  projection applies it. The full gate.
- **Depends on:** 2.

## 5. Android: choose it from the session top bar

The run and goal pages reach their home's own session through Chat, which has the same
top bar, so one control serves sessions, runs and goals. (An earlier draft put copies on
the run and goal pages; Chat made them unnecessary.)

- **Changes:**
  - `ui/Screens.kt`: an effort chip beside `ContextBadge` in the session top bar. It
    reads "effort <level>", or "effort auto" when unset, and tapping it opens a dialog
    with Default and the six levels, saying that on a home every later session under it
    uses it and that on Strata high, xhigh and max are the same.
  - `data/SessionRepository.kt` and `ui/RhoViewModel.kt`: `setEffort(sessionId, level)`
    through `set_option`. A refusal shows in the existing refusal banner.
  - `MainActivity.kt`: the screen's `onEffort` calls it.
- **Proves it:** `data/EffortTest.kt` with `FakeDaemon`: `setEffort` sends `key: effort`
  with the level, and `""` for Default; `ui/EffortChipTest.kt`: the chip's text and the
  dialog's choices; the projection test from step 2 reads `effort`.
  `bash clients/android/gradlew.sh :app:testDebugUnitTest :app:assembleDebug` passes.
  Installed on the phone: the dialog sets it on a session and on a run's home, and the
  chip changes.
- **Depends on:** 2.

## 6. Live

- Merge, then in a gap between runs install the daemon and runner (rename and copy, as
  before) and the phone build.
- Add to `~/.rho/runner/config.json`:
  `"effort": {"brief": "medium", "plan": "medium", "tasks": "medium", "verify": "medium", "reflect": "low"}`.
  Restart the daemon and the runner.
- **Proves it:** the next run's plan and verify sessions show `effort: medium` in their
  `session` event; their output tokens per model call average well under the 2,043 of
  the last verify session; plan and verify each finish in a fraction of their recent
  1–3 hours. Work sessions show no effort and behave as before.
- **Depends on:** 1–5, merged.

## Not doing

- The rust-gui client.
- Effort for the GitHub watcher's review and comment sessions.
- Recording the effort on each model call.
- Changing Strata's config.
