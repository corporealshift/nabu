# Stats across sessions: plan

Spec: `docs/specs/2026-10-07-stats-across-sessions-design.md`. One branch,
`stats-across-sessions`, with one commit per step. Each step leaves the gate green.

## 1. Archived logs keep their session ids

`ArchivedLogs` returns bare event slices, so an archived session's calls could not say
which session they came from.

- **Changes:**
  - `daemon/session/store.go`: `ArchivedLogs` returns `[]Archived{ID string; Events
    []protocol.Event}` and keeps returning what it could read alongside the joined error.
  - `daemon/api/handler.go` `handleUsage`: takes `.Events` from each. It also stops
    throwing away every archived log when one of them fails to load: it uses what came
    back whatever the error.
- **Proves it:** a store test that archives two sessions and gets both ids back, and one
  unreadable archive still returns the other; the existing `nabu.usage` handler tests;
  the full gate.
- **Depends on:** nothing.

## 2. The measuring, in `daemon/stats`

Pure functions over logs. Nothing calls them yet.

- **Changes:** `daemon/stats/window.go` and `window_test.go`.
  - `type Log struct { ID string; Events []protocol.Event; Archived bool }`.
  - `type Kind string` with `KindAll`, `KindInteractive`, `KindRuns`. `kindOf(log)`
    reads the labels from `protocol.Project` of the whole log: `unattended` is a run.
  - `cut(events, first, last)`: the events whose timestamp falls in `[first, last)`.
  - `Totals(logs, first, last, loc, kind) Window`: cuts each log, skips empty cuts,
    runs `Of` on the rest and sums. Per tool it sums calls and errors and counts
    sessions. `per_day` comes from `Usage` over the cut logs. Tools are ordered as `Of`
    orders them.
  - `Calls(logs, tool, first, last, kind, sessionID, limit) ([]Call, truncated bool)`:
    pairs each `tool_call` with its `tool_result` by `call_id`. Newest first. Result cut
    to 400 bytes on a rune boundary. No result is `pending`. With `sessionID`, only that
    log, and no period or kind filter. `label` is `Options.Description`, else the first
    user message cut to 80 characters. `archived` comes from the log.
- **Proves it:** table-driven tests in `window_test.go`:
  - totals equal the sum of `Of` over the cut logs;
  - an event outside the period is not counted, and a log with nothing inside adds no
    session;
  - each kind keeps only its sessions;
  - calls: newest first, cut at 400 without splitting a rune, `limit` and `truncated`
    agree, `pending` without a result, `session_id` ignores the period, error status
    and kind carried through.
  - The full gate.
- **Depends on:** nothing. It does not need step 1, since it takes ids in `Log`.

## 3. The methods

- **Changes:**
  - `daemon/api/handler.go`: register `nabu.stats` and `nabu.stats.calls`. A shared
    `allLogs()` gathers live logs from `Store.List`/`Get` and archived ones from step 1,
    and counts what it could not read as `skipped`. Validate `kind` (`invalid_params`
    otherwise), require `tool`, default and cap `days` (7, 90) and `limit` (100, 500).
    The period is whole local days ending today, as `nabu.usage` computes it.
  - `protocol/spec.md`: §7.24 `nabu.stats` and §7.25 `nabu.stats.calls`, with the
    shapes from the spec. Like §7.21, marked not normative: clients ask for them.
  - `protocol/schema/jsonrpc.json`: both names in the method enum.
  - `docs/specs/2026-10-07-stats-across-sessions-design.md`: correct the "conformance
    vector" line. The vectors cover the projection and other things every client
    computes; `nabu.session.stats` and `nabu.usage` have none, because clients ask
    instead of computing. These follow them. Also add the `archived` field to a call.
- **Proves it:** `daemon/api/handler_test.go`: totals across a live and an archived
  session; `kind=runs`; `invalid_params` on a bad kind and on a missing tool; calls for
  `web.search` with `session_id`. The protocol schema tests pass with the new names.
  The full gate.
- **Depends on:** 1 and 2.

## 4. The phone asks

- **Changes:**
  - `clients/android/.../protocol/Stats.kt`: `WindowStats`, `WindowTool` (with
    `sessions`), `ToolCall`, `ToolCalls`.
  - `clients/android/.../data/SessionRepository.kt`: `windowStats(days, kind)` and
    `toolCalls(tool, days, kind, sessionId?, limit)`.
- **Proves it:** `SessionRepositoryTest` against `FakeDaemon`: the params sent and the
  results decoded for both. `./gradlew.sh :app:testDebugUnitTest`.
- **Depends on:** 3, for the shapes. It needs no daemon running.

## 5. The screens

- **Changes:**
  - `clients/android/.../ui/Stats.kt`: split out the pieces both screens share. Add
    `OverallStatsScreen` with period and kind chips, the tiles, `DayColumns` and
    tappable `ToolBars`. Make the per-session screen's `ToolBars` tappable too.
  - `clients/android/.../ui/ToolCalls.kt`: the call list, and a call opened in full
    with "Open session" (shown only when the session is not archived).
    `argumentsInShort(json)`: `query`, `url`, `question`, `prompt`, `path`, `command`,
    `pattern` in that order, else compact JSON, cut to one line.
  - `NabuViewModel.kt`: state and loading for both screens. `MainActivity.kt`:
    `Screen.OverallStats` and `Screen.ToolCalls(tool, sessionId?)`, and back
    navigation that returns to where you came from. `Screens.kt`: a "Stats" action
    beside "Archived" in the session list's top bar.
- **Proves it:** `ToolCallsTest` for `argumentsInShort`;
  `./gradlew.sh :app:testDebugUnitTest :app:assembleDebug`; installed on Kyle's phone
  against the new daemon, the 7-day `web.search` count matches
  `grep -c '"tool":"web.search","arguments"'` summed over the week's logs in
  `~/.nabu/sessions` and the archive.
- **Depends on:** 4.

## Not doing

- The TUI, and jumping to the call inside a transcript (both rejected for now in the
  spec).
- Changing `nabu.usage` or `nabu.session.stats` beyond step 1's fix.
- Caching. If the scan turns out slow on the phone, that is a new spec.
