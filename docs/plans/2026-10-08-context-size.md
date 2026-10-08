# Context size: plan

Spec: `docs/specs/2026-10-08-context-size-design.md`. One branch, `context-size`, off
main, with one commit per step. Each step leaves the gate green. Steps 3 to 5 each depend
on 2 and not on each other.

## 1. Per-provider thresholds and the normal window

Mechanism only. Until step 2 every session is normal, so with `normal_window` set a
session compacts against it; with nothing set, nothing changes.

- **Changes:**
  - `daemon/config/config.go`: `ProviderConfig` gains `NormalWindow int`
    (`normal_window`), `ClearAt float64` (`clear_at`) and `SummarizeAt float64`
    (`summarize_at`). Validation refuses `summarize_at` outside [0, 1], `normal_window`
    below 0, and a positive `clear_at` at or above the effective `summarize_at`, naming
    the provider and key.
  - `daemon/provider/provider.go`: `Config` gains the same three fields.
    `daemon/daemon.go` copies them across.
  - `daemon/agent/compaction.go`:
    - `thresholds(pcfg, cfg.Compaction) (clearAt, summarizeAt float64)`: the provider's
      values over the daemon's. `clearAt < 0` means never.
    - `compactWindow(pcfg, opts) int`: `NormalWindow` when it is set and below
      `ContextWindow`, else `ContextWindow`. (Step 2 adds the `large` case.)
    - `maybeCompact` measures against `compactWindow` and uses `thresholds`; with
      clearing off it goes straight from nothing to summarize. The
      compaction-disabled notice reports the window it measured against.
  - `daemon/agent/runner.go`: `replyCap` unchanged: it keeps `ContextWindow`.
  - `README.md`: the three settings in the provider config section, with the measured
    reason for turning clearing off on a local model.
- **Proves it:**
  - `daemon/agent` table test over `maybeCompact` with a fake log of a given last input
    size: with window 128K and normal 64K, 50K does nothing, 46K clears at the default
    0.70, 55K summarizes; with `clear_at: -1`, 50K does nothing and 55K summarizes;
    with nothing set, today's thresholds against 128K.
  - `replyCap` with a normal window set still allows up to `ContextWindow` minus the
    last input.
  - `daemon/config` table test: the settings load; each out-of-range value is refused
    with the provider and key in the error.
  - The full gate.
- **Depends on:** nothing.

## 2. The `context` session option

Contract and its implementation together, as the spec requires.

- **Changes:**
  - `protocol/types.go`: `Options.Context string` (`context,omitempty`); constants
    `ContextNormal = "normal"`, `ContextLarge = "large"`; `ValidContext`. Absent means
    normal, so existing logs are unchanged.
  - `protocol/project.go`: `options_change` with key `context` applies it.
  - `protocol/spec.md`: §3.1 (`context` in `options`, what it means, that
    `context_window` stays the model's), `options_change` keys, §7.3 defaults, §7.13
    keys and the invalid-value rule.
  - `protocol/schema/event.json`: `context` in the options object, the enum, and the
    `options_change` key enum.
  - `protocol/vectors/projection/08-context.json`: a session created with
    `context: large`, changed to normal; the projection ends normal.
  - `daemon/agent/manager.go`: `CreateOptions.Context`, resolved and validated in
    `Create`; `SetOption` accepts `context` with the same check.
  - `daemon/api/handler.go`: `CreateSessionOptions.Context`, passed through.
  - `daemon/agent/compaction.go`: `compactWindow` returns `ContextWindow` for a large
    session.
  - `clients/go-tui` and `clients/android` projections: read `context` in options and
    `options_change` (Android `Events.kt`, `Projection.kt`), so step 4 and 5 have it.
- **Proves it:**
  - The conformance vectors pass in Go (`protocol/vectors_test.go`) and on Android (the
    client's vector test).
  - `daemon/agent`: a large session at 55K does nothing, at 109K summarizes; switching
    large to normal at 60K summarizes at the next turn end.
  - `daemon/api`: create with `context: large` records it in the `session` event;
    `set_option` with `context: huge` is `nabu_invalid_params` and appends nothing.
  - The full gate, and the Android unit tests.
- **Depends on:** 1.

## 3. The runner copies `context` from the parent

- **Changes:**
  - `clients/runs/daemon.go`: `Create` and `CreateHome` take a `context string` and
    send it in options when it is not empty.
  - `clients/runs/runner.go` (step sessions, from the run home) and
    `clients/runs/goalrunner.go` (run homes, from the goal home): read the parent's
    state, pass its `Options.Context`. A failed read logs why and passes "", which the
    daemon takes as normal.
  - The interface the tests fake gains the parameter.
- **Proves it:** `clients/runs` tests with the existing fake daemon: a large run home
  gives large step sessions; a large goal home gives large run homes; a parent whose
  state cannot be read gives a normal session and a log line. The full gate.
- **Depends on:** 2.

## 4. TUI `/context`

- **Changes:** `clients/go-tui/commands.go`: `/context` says whether the session is
  normal or large; `/context normal|large` calls `set_option`; anything else is a usage
  line. `/help` lists it. The status line shows "large" when it is.
- **Proves it:** `commands_test.go`: each form gives the right action or message;
  `tui_test.go` or the drive test: the `set_option` call carries `key: context`. The full
  gate.
- **Depends on:** 2.

## 5. Android: choose it from the context badge

The session screen's top bar has no menu and is full, so the existing context badge is
where the choice goes.

- **Changes:**
  - `ui/Screens.kt`: tapping `ContextBadge` opens a small dialog, "Normal" or "Large",
    with one line each on what it means. The badge reads "L" after the percentage when
    the session is large.
  - `data/SessionRepository.kt` and `NabuViewModel.kt`: `setContext(sessionId, size)`
    through `set_option`. A refusal shows in the existing refusal banner.
- **Proves it:** a unit test with `FakeDaemon` that `setContext` sends `key: context`,
  and the projection test from step 2 for the badge state.
  `./gradlew.sh :app:testDebugUnitTest :app:assembleDebug` passes. Installed on the
  phone: the dialog sets it and the badge changes.
- **Depends on:** 2.

## 6. Live

- Merge, then at a gap install the daemon (rename and copy, as before) and the phone
  build.
- Set `local` in `~/.nabu/config.json`: `normal_window: 64000`, `clear_at: -1`. Restart
  the daemon and the runner.
- **Proves it:** the next run's step sessions show `summarize` compactions with the
  last input near 54K and no `clear_results`; the stats screen's compaction counts for
  them show summaries only.
- **Depends on:** 1–5, merged.

## Not doing

- The run step that chooses a run's context size (the spec's later work).
- `keep_turns` per provider.
- Clearing `write` arguments or clearing to a low-water mark.
- Showing fullness against the normal window in clients.
