# Stats across sessions

**Date:** 2026-10-07
**Status:** Approved by Kyle in conversation
**Amends:** `protocol/spec.md` §7 (two new methods), the Android Stats screen.

## Problem

Kyle keeps asking Claude to grep `~/.nabu/sessions` for which sessions used
`web.search`, `web.fetch` or `claude.ask`, what they asked and what came back. The
phone shows one session's tool counts (`nabu.session.stats`) and turns and tokens per
day (`nabu.usage`), but nothing answers "what happened last week, across everything",
and nothing shows the calls themselves.

Everything needed is already in the logs: `tool_call` carries the arguments and
`tool_result` the content. Nothing new needs storing.

## Decision

### Screens (Android)

- **Stats**, opened from the session list. The per-session Stats screen's layout, over a
  period and many sessions.
  - Filters: period (today, 7 days, 30 days; default 7) and kind (all, interactive,
    runs). A run is any session labelled `unattended` (`protocol.LabelUnattended`), so
    runs and goals and their steps all count as runs.
  - Tiles: sessions, turns, prompts, working time, tokens in, tokens out, vetoes.
  - Calls per day, the way `DayColumns` draws them.
  - Tool bars with calls and errors. Each bar is tappable.
  - The compactions and interruptions line.
  - Left out: the per-turn context chart and rereads. They describe one session's
    behaviour; summed across sessions they mean nothing.
- **Tool calls**, the drill-down. One tool's calls, newest first. A row shows the
  session's label or first prompt, the time, the arguments in short form, ok or error
  with its kind, and the first lines of the result. Tapping a row shows the whole call
  and result, with a button that opens the session.
- **The per-session Stats screen** gets the same drill-down: its tool bars open Tool
  calls filtered to that session.

### Methods (daemon)

- **`nabu.stats {days?, kind?}`** → totals over the period:

  ```jsonc
  {"days": 7, "kind": "all", "sessions": 23, "turns": 812, "prompts": 40,
   "tokens": {"input": 41000000, "output": 390000, "cached": 0},
   "tools": [{"tool": "read", "calls": 1200, "errors": 14, "sessions": 21}],
   "compactions": {"summarize": 3, "clear_results": 9},
   "vetoes": 6, "interruptions": 2, "working_seconds": 86000,
   "per_day": [{"date": "2026-10-01", "turns": 120, "input": 6000000, "output": 50000}],
   "skipped": 0}
  ```

  - `days` defaults to 7 and is capped at 90, as `nabu.usage` is. The period is whole
    calendar days in the daemon's time zone, ending today.
  - `kind` is `all` (default), `interactive` or `runs`. Anything else is
    `invalid_params`.
  - A session counts if it has any event in the period. Only its events in the period
    are counted: each log is cut to the period, then measured by the existing
    `stats.Of`, and the results are added up. The numbers therefore count exactly as
    they do per session.
  - `sessions` per tool is how many sessions called it at least once.
  - `per_day` is the same series `nabu.usage` returns for the period.
  - Archived sessions are included. Their work happened.
  - `skipped` is how many logs could not be read. They are left out rather than failing
    the call, and the count keeps the totals from being silently short.
- **`nabu.stats.calls {tool, days?, kind?, session_id?, limit?}`** → one tool's calls:

  ```jsonc
  {"calls": [{"session_id": "…", "label": "Room database and DAOs", "at": "…",
              "arguments": {"query": "Room createFromFile copies"},
              "status": "ok", "kind": "", "result": "…first 400 characters…"}],
   "truncated": false, "skipped": 0}
  ```

  - `tool` is required. An unknown tool returns an empty list, not an error.
  - With `session_id`, only that session's calls, whatever the period and kind.
  - Newest first. `limit` defaults to 100 and is capped at 500. `truncated` says more
    calls matched than were returned.
  - `result` is the result content cut to 400 characters. `status` and `kind` are the
    `tool_result`'s. A call with no result yet has status `pending`.
  - `label` is the session's description when it has one, else the start of its first
    prompt.
- `nabu.usage` and `nabu.session.stats` are unchanged.
- Both methods get a section in `protocol/spec.md`, a JSON schema and a conformance
  vector.

## Rejected

- **A separate Tools screen alongside Stats.** Two places to look for the same thing.
  Tools are a drill-down inside Stats.
- **Widening the per-session screen with an "all sessions" toggle.** It muddles both
  views.
- **One method returning the totals and every call.** A week of `read` and `bash` calls
  runs to thousands; the totals are cheap, and calls are fetched for the tool tapped.
- **The phone fetching events and counting them.** Megabytes over the phone's link, and
  the counting rules written twice.
- **A stored index.** The log is the state (invariant 1). Scanning a few hundred logs
  per request is fine at this volume, as `nabu.usage` already shows.
- **A generated HTML report.** Not on the phone, and stale as soon as it is written.
- **For now: jumping to the call inside the transcript.** The transcript only scrolls to
  its end today. Opening the session is enough to start with.
- **For now: the TUI.** Kyle drives nabu from the phone. The methods are there if the
  TUI wants them later.

## What proves it works

- `daemon/stats`, table-driven:
  - the totals equal the sum of `stats.Of` over each log cut to the period;
  - events outside the period are not counted, and a session with none inside is not
    counted at all;
  - `kind` keeps runs or interactive sessions only;
  - calls come newest first, results are cut to 400 characters, `limit` and
    `truncated` agree, a call with no result is `pending`, `session_id` ignores the
    period.
- `daemon/api`: handler tests for both methods, including `invalid_params` on a bad
  `kind` and a missing `tool`, and archived sessions being counted.
- Conformance vectors for both methods.
- `go build ./... && go vet ./... && go test ./...`.
- The app built and installed on Kyle's phone, showing last week's numbers. The
  `web.search` count is checked against a grep of `~/.nabu/sessions`.
