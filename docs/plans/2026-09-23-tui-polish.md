# TUI polish — plan

Spec: `docs/specs/2026-09-23-tui-polish-design.md` (issue 106). One branch, `tui-polish`,
one PR. Every step leaves `go build ./... && go vet ./... && go test ./...` green, and
each is its own commit.

All paths below are under `clients/go-tui/`.

## 1. Transcript layout: margin, blocks, prompt timestamps

**Depends on:** nothing.

**Changes:**
- `model.go`: `transcript` becomes `[]entry`, where `entry{text, aside string}`. The
  aside is right-aligned on the first wrapped line. `appendEventWithoutRefresh`, `note`
  and `rememberThought` add a blank entry before each new block, unless the transcript
  is empty or the event is a `tool_result`. The live preview (streaming text, or
  thinking) gets a blank line before it, just as a block would.
- `composer.go`: `wrapStyled` / `wrapAll` gain an entry-level wrapper, `wrapEntry`. It
  wraps to `width-2` (less the aside's width and a gap of two when there is an aside),
  prefixes the two-column margin to non-empty lines, and pads the first line so the
  aside ends at the right edge. Under 40 columns the aside is dropped.
- `render.go`: `renderMessage` puts a user prompt's timestamp in the aside, and
  assistant messages lose theirs. `renderEvent` returns entries. Tool results keep
  their two-space indent, which sits at column 4 once the margin is added.
- `thinking.go`: the thought slot index points at the entry after the blank.
- Tests that set or read `m.transcript` move to `entry`.

**Proves it:**
- A new table test in `render_test.go` or `model_test.go`: a sequence (message, tool
  call, tool result, tool call, tool result, message, note) produces blanks at exactly
  the expected indices, and none before a result.
- A new test: after wrapping, a prompt's first line has `visibleWidth` equal to the
  pane width and ends in the timestamp. Every line starts with two spaces.
- `TestMessageTimestampAndIdleAge` checks the aside, not the body.
- The existing wrap and pane tests (`wrap_test.go`, `pane_test.go`) still pass,
  including "no line wider than the pane".

## 2. Transcript content: state changes, report, thought hint

**Depends on:** 1, since it edits the same functions in `render.go`.

**Changes:**
- `render.go`: `EventStateChange` renders nothing for a change between running and
  idle, unless the reason is `interrupted`. `renderReport` becomes
  `<mark> <status> · tasks d/t · files n · commits n · tree dirty`, with `✓` green, `!`
  yellow (for blocked or paused) and `✗` red (for error).
- `thinking.go`: the collapsed thought reads `~ thought (N words)`, and the expanded
  one `~ thought`.

**Proves it:**
- A table test over state changes: idle→running `prompt` hidden, running→idle
  `turn_complete` hidden, running→idle `interrupted` shown, running→paused
  `daemon_shutdown` shown, →blocked shown, →completed shown.
- A table test over report exit statuses, checking the mark and the ` · ` separators.
- `thinking_test.go` is updated for the new hint text.

## 3. Bottom of the screen: composer box, status line, `?` panel

**Depends on:** nothing in 1–2 (a different region of the code), but land it after them
to keep the diff linear.

**Changes:**
- `composer.go`: `composerLines` draws the dim rounded box around the prompt, wrapping
  to `width-4-2-1`. When idle the box says `press i to type`. `composerHeight` counts
  the box's rows. `viewportHeight` is `height - composerHeight - 2`: one row for the
  gap above the box and one for the status line.
- `view.go`: `View` joins transcript, blank row, composer, status. `status()` takes
  ` · ` separators and drops the session id. It gains a right side (`statusHints`),
  chosen by mode and dropped when both sides do not fit `m.width`. `help()` is deleted.
  A new `keysPanel()` overlay lists keys, commands and the full session id.
- `model.go`: a `showKeys bool`.
- `update.go`: `?` in the transcript opens the keys panel. `onKey` routes to it before
  the picker: `?`, `esc` and `q` close it, and `ctrl+c` quits.
- `commands.go`: `helpText` is shared with the keys panel, so the list of commands is
  kept in one place.

**Proves it:**
- A new layout test (table): `lipgloss.Height(m.View()) == m.height` and no line is
  wider than `m.width`. It runs for idle, composing a three-line prompt, asking a
  question, and with the task pane shown, at 80×24 and 120×40.
- A new test: at 40 columns, the status line is no wider than 40, and the hints are
  gone.
- A new test: `?` opens the keys panel, `esc` closes it, and while it is open `q` does
  not quit.
- The existing `drive_test.go` and `composer_test.go` still pass, with strings updated
  where they matched the old help line or placeholder.

## 4. Overlays: padding, picker spacing, task pane

**Depends on:** 3, since `view.go` and the ask panel's chrome constants are touched
by both.

**Changes:**
- `view.go`: `overlayBox` padding becomes `Padding(1, 3)`. The picker adds a blank
  line between rows, and `pickerWindow` counts three lines per row and the extra
  padding rows. The task pane gets `PaddingLeft(2)`.
- `ask.go`: a separate `askBox` style keeps `Padding(0, 2)`. `askBoxWidthChrome` and
  `askBoxHeightChrome` are unchanged.
- `stats.go` inherits the padding through `overlayBox`, and its width math accounts for
  the wider padding.

**Proves it:**
- A picker test: with a height that fits exactly N sessions, `pickerWindow` returns N,
  and the rendered picker's height is ≤ `m.height`.
- The step 3 layout test still passes with the ask panel.
- The existing `ask_test.go` tests pass unchanged.

## 5. Look at it

**Depends on:** 1–4.

Run the full gate. Start a daemon, run a real session in the TUI, and look at the
transcript, picker, `?` panel, a permission prompt, and a question at 80 and 120
columns. Anything that looks wrong goes back to the step that owns it. This step has
no commit of its own.

## Not doing

- The Android client, the headless CLI, and colours.
- The local echo of a sent prompt stays (spec §4).
- No new events and no protocol change: this is rendering only.
