# nabu P2 — TUI: Drive and Inspect Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> This plan specifies contracts and behaviours, not code. Write the failing test first
> from the stated behaviour, then the implementation. If a requirement is ambiguous or
> contradicts the real code, STOP and report rather than guessing.

**Goal:** Finish the TUI so nabu can be the daily driver: start and steer a run from the terminal, see what the agent thinks it is doing, and know it is alive while the model is slow.

**Architecture:** Additions to the existing Bubble Tea program under `clients/go-tui`. The model stays pure data and the update function stays pure; everything that touches the network goes through the answer-channel pattern already used for permission replies. No new dependencies.

**Tech Stack:** Go 1.26, the Bubble Tea stack already present, `clients/goclient`.

---

## Decisions settled by this plan

### The working indicator

**The problem it solves.** A turn against a local 35B model takes minutes, and the
first of those minutes produces no tokens at all. Today the screen simply stops
changing, which is indistinguishable from a hang.

An indicator appears in the status bar whenever the session state is `running`. It
shows a spinner frame, the word "working", and the elapsed time since the turn began
(`working 1m12s`). It is driven by a repeating tick, not by network activity, so it
keeps moving even while nothing arrives.

When the first delta of a turn arrives the indicator stays, because a long turn
continues after its first token. It clears when the session leaves `running`.

**Elapsed time is measured from the state change into `running`**, not from the prompt
being sent, so a queued session does not appear to have been working the whole time.

### Interrupt

`ctrl+x` sends `nabu.session.interrupt`. Chosen over `ctrl+c`, which quits, because
quitting and interrupting are opposite intentions and the cost of confusing them is
high: one leaves the run going, the other stops the turn.

It is only sent while the session is `running`; otherwise it is a no-op with a note,
since spec 7.7 makes interrupt a no-op server-side anyway and a silent keypress is
worse than a line saying nothing happened.

### The composer

`i` or `enter` opens a single-line input at the bottom. `enter` sends it as
`nabu.session.send_prompt`, `esc` cancels. The transcript keeps scrolling underneath.

**A prompt may be sent while the session is running.** Spec 7 is explicit that
`send_prompt` does not queue: the message is appended and the loop picks it up at the
next turn boundary. That is how a run is steered, so the composer must not refuse it.

Multi-line input is out of scope. A newline would have to compete with send, and the
common case is a sentence.

### Slash commands

Input beginning with `/` is a command rather than a prompt:

| Command | Effect |
|---|---|
| `/goal <text>` | `nabu.session.set_goal` |
| `/goal` | `nabu.session.clear_goal` |
| `/stop` | `nabu.session.stop` |
| `/sessions` | opens the session picker |
| `/help` | lists the commands |

An unknown command is a note, not a prompt. Sending `/gaol fix the tests` to the model
as a prompt would be worse than saying it is not a command.

### The session picker

`s` or `/sessions` opens a list of sessions from `nabu.session.list`, newest first,
showing id, state and workspace. `enter` attaches to the highlighted one, `esc`
cancels.

**Attaching switches the connection**, which means resubscribing and replaying the new
session's log from nothing. The model's transcript, cursor and streaming state all
reset. Getting this wrong shows one session's transcript under another's id, so the
reset is a single operation rather than field-by-field.

### The task pane and goal badge

The task pane is a right-hand column, shown when the session has tasks and the terminal
is at least 100 columns wide. Below that width it is hidden rather than squeezed: a
two-column layout in 80 columns makes both halves unreadable.

Each task renders as a status mark, its id and its title. `pending` is a space,
`in_progress` a caret, `done` a tick, `blocked` an exclamation, `failed` a cross.

The goal badge sits in the status bar when a goal is set, showing its state. An `unmet`
goal is the interesting one, because it means the stop gate refused to finish.

Tasks come from the `tasks` event, which the renderer currently drops. The model keeps
the latest snapshot; the event still contributes nothing to the transcript, because a
task list re-rendered inline on every update would drown the conversation.

### What stays out

Scrollback search, transcript export, mouse support, and editing a task from the TUI
(`nabu.session.update_tasks` exists, but deciding how a terminal edits a task list is
its own design). Multi-line composer input.

---

## File structure

| Path | Responsibility |
|---|---|
| `clients/go-tui/model.go` | **Modify.** Add composer, picker, tasks, goal, turn-start time and tick state. Add the reset that switching sessions needs. |
| `clients/go-tui/update.go` | **Modify.** Composer and picker key handling, slash-command parsing, tick handling, the new outbound actions. |
| `clients/go-tui/view.go` | **Modify.** Working indicator and goal badge in the status bar, composer line, picker overlay, task pane column. |
| `clients/go-tui/commands.go` | **New.** Slash-command parsing: text in, an action or an error out. Pure and directly testable. |
| `clients/go-tui/main.go` | **Modify.** Carry out the new actions against the client, and drive the tick. |
| `clients/go-tui/commands_test.go` | **New.** Every command, and what an unknown one does. |
| `clients/go-tui/model_test.go` | **Modify.** Composer, picker, tasks, indicator, interrupt. |

---

## Task 1: The working indicator

**Files:** Modify `model.go`, `update.go`, `view.go`, `main.go`. Test in `model_test.go`.

**Purpose:** Make it obvious the agent is alive during the minutes a local model takes
to produce its first token.

**Contract**

A tick message arrives about twice a second. The model records the time a turn entered
`running` and renders, in the status bar, a spinner frame plus "working" plus the
elapsed time.

**Behaviours that must hold** — each becomes a test:
- [ ] With the session `running`, the status bar contains "working"
- [ ] With the session `idle`, `completed`, `paused` or `blocked`, it does not
- [ ] The elapsed time is measured from the transition into `running`, not from process start
- [ ] The spinner frame advances between ticks, so a still screen is visibly alive
- [ ] The indicator survives the first delta: a turn that has started producing text is still working
- [ ] A tick with no running session changes nothing

**Verify:** `go test ./clients/go-tui/`

**Done when:** A running session shows a moving indicator with elapsed time, and a
finished one shows none.

**Commit:** `tui: a working indicator while the model thinks`

---

## Task 2: Interrupt

**Files:** Modify `model.go`, `update.go`, `main.go`. Test in `model_test.go`.

**Purpose:** Stop a turn that has gone wrong without leaving the terminal.

**Contract**

`ctrl+x` produces an interrupt action carried out against the daemon. The outbound
action type generalises the existing answer channel, which until now carried only
permission replies.

**Behaviours that must hold:**
- [ ] `ctrl+x` while `running` produces an interrupt action naming the session
- [ ] `ctrl+x` while not running produces no action and notes that there is nothing to interrupt
- [ ] `ctrl+c` still quits and does not interrupt
- [ ] Interrupting does not quit the program
- [ ] The transcript records that an interrupt was requested, so the later `interrupted` message is not a surprise

**Verify:** `go test ./clients/go-tui/`

**Done when:** A running turn can be stopped with a keypress and the view stays up.

**Commit:** `tui: interrupt the current turn`

---

## Task 3: The composer

**Files:** Modify `model.go`, `update.go`, `view.go`, `main.go`. Test in `model_test.go`.

**Purpose:** Start and steer a run without a second terminal.

**Contract**

`i` or `enter` opens a single-line input. Typing edits it; `enter` submits;
`esc` cancels and discards. Submitting non-command text produces a send-prompt action.

**Behaviours that must hold:**
- [ ] `i` opens the composer and the status bar shows it is open
- [ ] Typing appends to the buffer; backspace removes from it
- [ ] `enter` on non-empty text produces a send-prompt action with exactly that text
- [ ] `enter` on empty or whitespace-only text closes the composer and sends nothing
- [ ] `esc` closes and discards, sending nothing
- [ ] While the composer is open, `q` types a letter rather than quitting
- [ ] A prompt may be submitted while the session is running, per spec 7
- [ ] A permission prompt takes precedence: with one on screen the composer does not open

**Verify:** `go test ./clients/go-tui/`

**Done when:** A session can be prompted from the TUI, running or idle.

**Commit:** `tui: a composer for prompts`

---

## Task 4: Slash commands

**Files:** Create `commands.go` and `commands_test.go`. Modify `update.go`.

**Purpose:** Set a goal, stop a session and reach the picker without leaving the keyboard.

**Contract**

A function takes the composer's text and returns either an action or a message for the
user. Text not beginning with `/` is a prompt. `/goal <text>` sets a goal; `/goal`
alone clears it; `/stop` stops the session; `/sessions` opens the picker; `/help`
lists the commands.

**Behaviours that must hold:**
- [ ] Text without a leading slash is a prompt, verbatim
- [ ] `/goal the gate is green` produces a set-goal action carrying exactly that condition
- [ ] `/goal` with no argument produces a clear-goal action
- [ ] `/goal   ` with only whitespace also clears, rather than setting an empty goal
- [ ] `/stop` produces a stop action
- [ ] `/sessions` opens the picker and produces no network action
- [ ] `/help` produces a note listing the commands and no network action
- [ ] An unknown command produces a note naming it, and is never sent to the model
- [ ] A bare `/` is an unknown command, not a prompt

**Verify:** `go test ./clients/go-tui/`

**Done when:** Every command in the table works and an unknown one cannot reach the model.

**Commit:** `tui: slash commands for goal, stop and sessions`

---

## Task 5: The session picker

**Files:** Modify `model.go`, `update.go`, `view.go`, `main.go`. Test in `model_test.go`.

**Purpose:** Move between sessions without restarting the program.

**Contract**

`s` or `/sessions` opens a list, newest first, each row showing the short id, state and
workspace. Up and down move the highlight; `enter` attaches; `esc` closes.

Attaching resets the transcript, the cursor, the streaming buffer, the tasks, the goal
and any queued prompts in one operation, then the connection resubscribes and replays
from nothing.

**Behaviours that must hold:**
- [ ] `s` opens the picker and lists the sessions the model holds
- [ ] Up and down move the highlight and stop at the ends rather than wrapping
- [ ] `enter` produces an attach action for the highlighted session and closes the picker
- [ ] `esc` closes and produces no action
- [ ] Attaching a different session clears the transcript, cursor, streaming text, tasks and goal
- [ ] Attaching the session already attached is a no-op that does not clear anything
- [ ] An empty list says so rather than showing an empty box

**Verify:** `go test ./clients/go-tui/`

**Done when:** Switching sessions shows the new session's transcript and nothing from the old one.

**Commit:** `tui: a session picker`

---

## Task 6: The task pane and goal badge

**Files:** Modify `model.go`, `update.go`, `view.go`, `render.go`. Test in `model_test.go`.

**Purpose:** See what the agent believes it is doing, and whether the stop gate agrees.

**Contract**

A `tasks` event updates the model's task list without contributing to the transcript. A
`goal` event updates the goal. The task pane occupies a right-hand column when there
are tasks and the terminal is at least 100 columns wide. The goal badge appears in the
status bar when a goal is set.

**Behaviours that must hold:**
- [ ] A `tasks` event updates the pane and adds no transcript line
- [ ] Each status renders its own mark, and `done` is distinguishable from `pending` without colour
- [ ] The pane is hidden below 100 columns even when tasks exist
- [ ] The pane is hidden when there are no tasks, at any width
- [ ] A `goal` event puts the condition in the status bar
- [ ] A goal in state `unmet` is visually distinct, because it means the stop gate refused
- [ ] A cleared goal removes the badge

**Verify:** `go build ./... && go vet ./... && go test ./...`

**Done when:** Tasks and the goal are visible while the agent works, and a narrow
terminal still renders a readable transcript.

**Commit:** `tui: task pane and goal badge`

---

## Verifying

The project gate, from the repo root:

```
go build ./... && go vet ./... && go test ./...
```

CI runs that plus `gofmt -l .` on ubuntu-latest and windows-latest.

Beyond the gate, this slice is only done when it has been driven by hand against a live
daemon and a real model: prompt from the composer, watch the indicator move, set a goal,
interrupt a turn, and switch sessions. The bugs in the first slice — a screen that never
rendered, notifications dropped during a call — were all found that way and none of them
by a test.

## Not in this phase

Scrollback search, transcript export, mouse support, editing tasks from the terminal,
and multi-line composer input.
