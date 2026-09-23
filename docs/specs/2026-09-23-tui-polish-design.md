# TUI polish

**Date:** 2026-09-23
**Author:** Kyle (corporealshift), with Claude
**Status:** Approved (issue 106). Mockups compared: https://claude.ai/artifact/QfKMdcyejzNp1TmF9Yis5S

## 1. The problem

The TUI is hard to read (issue 106). Every event gets one line and nothing separates them.
Each message starts with a timestamp, and every turn adds `running → idle` lines. The
composer looks like one more transcript line. Three lines of chrome (composer hint,
status, help) are stacked at the bottom, and the help line wraps on an 80-column
terminal.

## 2. Decision

Use Claude Code's spacing and layout, with the glyphs the TUI already uses: `›` `→` `~`
`✓` `!` `✗`. Four options were mocked up side by side, and this is the one Kyle chose:

```
  session in C:\Users\corpo\Documents\projects\nabu

  › fix the flaky attach test in the tui                        Sep 23 15:01

  ~ thought (120 words)

  → bash go test ./clients/go-tui/...
    --- FAIL: TestAttachReplay (0.02s) …

  → read_file clients/go-tui/update.go
    package tui …

  The attach raced the replay: the cursor moved before the batch was
  applied, so a live event could slip in ahead of it.

  ✓ completed · tasks 3/3 · files 1

╭──────────────────────────────────────────────────────────────────────────────╮
│ › press i to type                                                            │
╰──────────────────────────────────────────────────────────────────────────────╯
  ● connected · idle · context 12%                    s sessions · ? keys · q quit
```

### Transcript

- **Margin.** Transcript lines get a two-column left margin. It is added when a line is
  wrapped, not when it is rendered, so lines wrap to the pane width less two.
- **Blocks.** A blank line comes before each block. A block is whatever one event
  renders, or one local note. The exception is a tool result: it joins its call's block,
  directly under it and indented to column 4. The daemon runs tool calls one at a time
  (`daemon/agent/runner.go`), so a result always follows its own call. A multi-line
  message is one block; its own blank lines are kept.
- **Timestamps.** Only user prompts show a timestamp, as a dim `Jan 2 15:04` aligned to
  the right edge of the prompt's first line. The alignment has to happen at wrap time,
  since the width is not known when the event is rendered, so a transcript entry
  carries this text separately from its body. The prompt wraps narrower to leave room
  for it. Assistant messages no longer show a timestamp.
- **State changes.** Routine running↔idle transitions no longer appear, since the
  status line already shows the state. The daemon gives every transition a reason, so
  "hide the ones without a reason" would hide nothing. The rule is instead: a change
  between `running` and `idle` is hidden unless its reason is `interrupted`. Changes
  into `paused`, `blocked`, `error` or `completed` stay.
- **Report.** The report becomes one line. The mark says how the run ended: `✓` green
  for `completed`, `!` yellow for `blocked` or `paused`, `✗` red for `error`. Then come
  the counts, separated by ` · `, e.g. `✓ completed · tasks 3/3 · files 1 · commits 2`.
- **Hints leave the transcript.** `~ thought (120 words) · t to show` becomes
  `~ thought (120 words)`, and the expanded header is `~ thought`. The key moves to the
  `?` panel.

### Bottom of the screen

- **Composer box.** A dim rounded box (colour 8) holds the composer, with `› ` inside it.
  It fills the terminal width, with one blank row above it. When idle it shows
  `press i to type`. Its text wraps to the width inside the box. The ask panel still
  takes the composer's place, with its yellow border, as it does today.
- **Status line.** One line, under the box. The left side is the status as today, with
  ` · ` between the parts. The short session id moves to the `?` panel. The right side
  depends on the mode:
  - reading: `s sessions · ? keys · q quit`
  - composing: `enter send · esc cancel · /help`
  - session ended: `session ended · ? keys · q quit`

  When both sides do not fit, the right side goes. The status is what matters.
- **`?` keys panel.** A full-screen overlay, like `/stats`. It lists every transcript
  key (the ones the help line has today, plus scrolling), points to `/help` for the
  commands, and shows the full session id. `?`, `esc` or `q` closes it. It stands in
  for the help line that used to wrap. The commands are left out because keys and
  commands together, in the padded overlay, come to about 30 rows, taller than an
  80×24 terminal. Instead, `/help` prints the commands one per line, with the names
  aligned, rather than as one run-on line.

### Overlays

- The picker, the permission prompt, `/stats` and `?` all use `overlayBox`. Its padding
  grows to one row and three columns. The ask panel gets its own style that keeps the
  current padding (no vertical padding, two columns): it shares the screen with the
  transcript, so every row it adds is a row the transcript loses.
- The picker gets a blank line between sessions, and its window arithmetic counts
  three lines per row.
- The task pane gets two columns of left padding instead of one.

## 3. Rejected

- **Whitespace only.** This would add a margin and blank lines and change nothing else.
  It keeps the timestamps, the state lines and the three chrome lines, which are most
  of the noise.
- **The full Claude Code idiom.** `⏺` and `⎿` markers and `name(arg)` tool calls.
  Everyone would have to relearn the glyphs, and it reads no better than the in-between
  option.
- **Blank lines as padding in the renderer.** Having `renderEvent` return a leading `""`
  would put knowledge of the neighbouring event inside a function that only sees one.
  Where the blank goes depends on what came before, so it is decided where events are
  appended.

## 4. Not changed

- The local echo of a sent prompt (`› text`, dim) stays. It confirms that a queued
  prompt was received, before its message event arrives.
- The Android client. This is the Go TUI only.
- Colours stay the terminal's 0–15 palette.

## 5. How it fails

- A notice logged between a tool call and its result gets its own block. The result
  then lands under the notice, not under its call. This is rare (it takes a gate
  notice), and the result's indentation still marks it as output.
- On a very narrow terminal (under 40 columns), the right-aligned timestamp is dropped
  rather than squeezing the prompt.

## 6. What proves it

- Table tests for block spacing: which events get a blank line before them, and that a
  tool result gets none.
- A test that the timestamp's right edge meets the pane's right edge.
- A test that routine state changes are hidden and interruptions are not.
- Table tests for the report mark.
- A test that the status line never exceeds the terminal width.
- A layout test: `lipgloss.Height(View())` equals the terminal height with the
  composer idle, composing a multi-line prompt, asking, and with the task pane shown.
  The box, the gap and the status line all come out of the transcript's rows.
- The full gate, then the TUI run against a live daemon to look at it.
