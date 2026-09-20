# nabu P2 — TUI: Watch and Answer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> This plan specifies contracts and behaviours, not code. Write the failing test first
> from the stated behaviour, then the implementation. If a requirement is ambiguous or
> contradicts the real code, STOP and report rather than guessing.

**Goal:** A terminal client that attaches to a running session, renders its transcript live, and answers the daemon's permission prompts — enough to watch an agent work and approve what it asks for.

**Architecture:** A Bubble Tea client under `clients/go-tui/`, speaking the same JSON-RPC-over-WebSocket protocol as the CLI. It holds no session state: the daemon owns the log, and the TUI renders from events. A dropped connection is reconnected with backoff and the log replayed from the client's cursor.

**Tech Stack:** Go 1.26, Bubble Tea and its ecosystem, the existing coder/websocket transport.

---

## Decisions settled by this plan

The spec leaves several details open. This plan decides them:

### Shared client placement

The shared client lives at **`clients/goclient/`** as package `goclient`, a peer of the
TUI rather than part of it. `cmd/nabu` and `clients/go-tui` both import it.

**Why not inside `clients/go-tui/`.** That would make the CLI depend on the TUI, which
inverts the relationship: the CLI existed first and does not need a terminal UI. It is
also not buildable — `clients/go-tui/` is `package main` for the TUI binary, and a Go
directory holds one package.

**Why not `protocol/`.** That package is the normative wire contract shared with the
Kotlin and Rust implementations. A Go transport client is one language's convenience,
not part of the contract.

### Bubble Tea dependency versions

| Package | Why |
|---|---|
| `github.com/charmbracelet/bubbletea` | The framework: model, update, view |
| `github.com/charmbracelet/lipgloss` | Styling and layout |
| `github.com/charmbracelet/bubbles` | The viewport component, for a scrollable transcript |

Pin whatever `go get` resolves and record the versions in the commit. Add them in a
task that also runs `go mod tidy`.

**No logging dependency.** The repo already uses `log/slog` from the standard library.
A TUI owns the terminal, so it must not write logs to stdout at all; anything it needs
to record goes to a file or is dropped.

### How the transcript renders each event type

Each event type renders as follows. The render is purely presentational; no event is skipped or hidden.

| Event type | Rendered text |
|---|---|
| `session` | `[session] workspace=<workspace>` — shown once at the top, never again |
| `message` (user) | `> <content>` — blockquote style, one line per message. Truncated at 200 characters with `…` if longer. |
| `message` (assistant) | `<content>` — plain text. If `interrupted: true`, suffix `⚡` and a dim label `[interrupted]`. Assistant messages with `usage` carry no inline usage rendering; usage is in the status bar. |
| `tool_call` | `├─ <tool>` — indented, with the tool name. If the tool has a short identifier (e.g. `bash: ls -la`), show it; otherwise just the tool name. |
| `tool_result` | `│ └─ [<status>] <content>` — indented under the tool_call. `status` is `ok` or `error`. Content truncated at 120 characters. |
| `state_change` | `[<from> → <to>] <reason>` — dim text, shown only when the state actually changes |
| `options_change` | `[option: <key> <from> → <to>]` — dim text |
| `compaction` | `[compaction: <mode>]` — dim text |
| `context` | Not rendered in the transcript. Context blocks are internal to the model request. |
| `tasks` | Not rendered inline. A tasks event updates the task list in the model; the task pane (P2b) would display it. |
| `goal` | `[goal: <state>] <condition>` — shown when a goal is set or changes state |
| `check` | `[check: <name> <status>]` — dim text |
| `stop_veto` | `[veto: <module>] <reason>` — dim text |
| `budget` | Not rendered inline. Budget changes are reflected in the status bar. |
| `notice` | `[<level>] <message>` — dim text, level-coloured (warn=yellow, error=red, info=dim) |
| `report` | `[report] <exit_status>` — dim text at session end |

Events are rendered in log order with no gaps. The transcript is a single scrollable buffer.

### How streaming deltas compose with the final message

Deltas are ephemeral (spec §7.6): never logged, never replayed. The final `message` event carries the complete text and supersedes every delta of that `turn_id`.

The TUI renders deltas as a "live preview" line at the bottom of the transcript, showing `[streaming] <accumulated text>` with a blinking cursor indicator. When the final `message` event for that `turn_id` arrives, the preview line is replaced with the full assistant message rendered normally. If the TUI is disconnected when the message completes, it renders the full message on reconnect without ever having shown the preview.

Deltas for a `turn_id` that never produces a `message` event (e.g. the session is interrupted mid-stream) are discarded. The interrupted message is logged as a `message` event with `interrupted: true` and renders normally.

### What the permission overlay shows and how it is answered

The permission overlay appears as a centered modal dialog above the transcript. It shows:

- **Tool name** — bold, e.g. `bash`
- **Summary** — the one-line description from the request, e.g. `bash: rm -rf /tmp`
- **Risk tier** — coloured badge: `low` (green), `medium` (yellow), `high` (red)
- **Decision buttons** — `Approve` (default, Enter), `Deny` (Esc)

Key bindings:
- `Enter` or `y` — approve
- `Esc` or `n` — deny

When a permission request arrives while another is already on screen, the new request is queued behind it. The daemon sends requests with unique `request_id` values; the TUI tracks which request it is answering and queues any that arrive for a different `request_id` until the current one is resolved. When the TUI answers (approve or deny), it sends the JSON-RPC response with the correct `request_id`. Late answers to already-resolved requests receive `nabu_already_resolved` (error code -32004); the TUI dismisses the overlay.

### What the user sees while disconnected

When the connection drops, a dim banner appears at the top of the screen: `⚠ Disconnected — reconnecting…` with a retry countdown. The transcript buffer is frozen at the last known state. The status bar shows the last known session state and a `[disconnected]` badge.

When reconnected, the banner briefly changes to `Replaying events…` while events are fetched from the cursor, then disappears. If the replay catches up to the live stream, the banner disappears and the transcript continues. If the session ended during disconnection, the banner changes to `Session completed` and the transcript shows the final report event.

A disconnect that lasts longer than the daemon's request timeout (default 10 minutes) means any outstanding permission request times out on the daemon side. The TUI, when it reconnects, may receive `nabu_already_resolved` for a request it was still displaying. It dismisses the overlay and shows a notice: `Permission request timed out while disconnected`.

### How the TUI is tested

Bubble Tea programs consist of a model (pure data) and an update function (pure function: model + message → model). These are tested directly with injected messages and assertion on the resulting model state.

The client (WebSocket + JSON-RPC) is tested against a fake client that implements the same interface but talks to a scripted event stream or a mock WebSocket. The fake client does not bind a real port or require a running daemon.

Integration tests stand up a real daemon on port 0 and attach the client to it, the way
`cmd/nabu/main_test.go` already does. **No build tag**: a test that CI never runs is not
a test. They must skip cleanly if something they need is unavailable rather than being
excluded by default.

---

## File structure

| Path | Responsibility |
|---|---|
| `clients/goclient/client.go` | **New.** Package `goclient`: the shared JSON-RPC-over-WebSocket client. Dial and hello, calls, notification stream, and answering daemon-to-client requests. Used by both the CLI and the TUI. |
| `clients/goclient/client_test.go` | **New.** Tests against a scripted server, binding port 0. |
| `clients/go-tui/main.go` | **New.** Package `main`: flag parsing, dial, and the Bubble Tea program. |
| `clients/go-tui/model.go` | **New.** The model: transcript buffer, overlay state, connection state. Pure data. |
| `clients/go-tui/update.go` | **New.** The update function: key messages, client events, reconnect. Pure. |
| `clients/go-tui/view.go` | **New.** The view: transcript, overlay, status bar. Pure function of the model. |
| `clients/go-tui/render.go` | **New.** One event to one rendered line, per the table above. Pure and directly testable. |
| `clients/go-tui/model_test.go` | **New.** The model and update function driven with injected messages. |
| `clients/go-tui/render_test.go` | **New.** Every event type renders as specified. |
| `cmd/nabu/client.go` | **Delete.** Replaced by `clients/goclient`. |
| `cmd/nabu/commands.go` | **Modify.** Import `clients/goclient` instead of the local client. |

---

## Task 1: Shared client package

**Files:**
- Create: `clients/go-tui/client.go`
- Create: `clients/go-tui/client_test.go`

**Goal:** A shared JSON-RPC-over-WebSocket client that the CLI and TUI both import. It speaks `nabu.hello`, `nabu.session.subscribe`, `nabu.session.events_after`, and answers daemon-to-client requests (`nabu.rpc.permission.request`, `nabu.rpc.ui.ask`).

- [ ] **Step 1: Write the failing test for client construction and hello**

Append to `clients/go-tui/client_test.go` a test that creates a fake WebSocket connection (implementing the `github.com/coder/websocket.Conn` interface or wrapping a real one in a test harness) and verifies:
- `Dial(ctx, addr, token)` sends `nabu.hello` with `client: "go-tui"`, a version string, and `protocol.Version`
- The daemon's hello response is parsed and returned
- A protocol version mismatch (major version differs) returns an error
- A missing hello returns an error

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./clients/go-tui/ -run TestDialHello -v`
Expected: compile error `package clients/go-tui: no Go files in …`

- [ ] **Step 3: Create the shared client**

Create `clients/go-tui/client.go` with package `client`. The file contains:

A `Client` struct wrapping a `*websocket.Conn`, an ID counter, and a mutex.

`Dial(ctx, addr, token string) (*Client, error)` — dials the WebSocket, sends `nabu.hello` with `client: "go-tui"`, `client_version: "0.1.0"`, `protocol_version: protocol.Version`, reads the response, and returns an error on mismatch or failure.

`Subscribe(ctx, sessionID string) (<-chan Event, <-chan Delta, error)` — sends `nabu.session.subscribe`, returns a channel for `Event` notifications and a channel for `Delta` notifications. The channels are closed when the connection closes.

`EventsAfter(ctx, sessionID, lastEventID string) ([]protocol.Event, bool, error)` — calls `nabu.session.events_after` with `session_id` and `last_event_id`, returns the event list and the `synced` boolean.

`AnswerPermission(ctx, requestID, sessionID, verdict, reason string) error` — sends a JSON-RPC response to the daemon-to-client request `nabu.rpc.permission.request` with the verdict (`"approve"` or `"deny"`) and optional reason.

`AnswerAsk(ctx, requestID, sessionID, answer string) error` — sends a JSON-RPC response to `nabu.rpc.ui.ask`.

`Close() error` — closes the WebSocket with `StatusNormalClosure`.

An `OnRequest` method that registers a callback: `OnRequest(func(method string, params json.RawMessage))` — called whenever a daemon-to-client request arrives (a message with no `id` match but a method that is a request, not a notification).

The internal message loop (started in `Subscribe` or `Dial`) reads from the WebSocket and dispatches:
- Responses (message with `id` matching a pending call) → returned to the caller
- Notifications (`nabu.session.event`, `nabu.session.delta`) → sent to the appropriate channel
- Daemon-to-client requests (message with `id` and method `nabu.rpc.permission.request` or `nabu.rpc.ui.ask`) → dispatched to the `OnRequest` callback

An `Event` type alias for `protocol.Event` and a `Delta` struct `{SessionID, TurnID, Text string}`.

- [ ] **Step 4: Run the client tests — they should pass**

Run: `go test ./clients/go-tui/ -run TestDialHello -v`
Expected: pass.

- [ ] **Step 5: Write the failing test for subscribe and event delivery**

Append tests:
- `Subscribe` sends the correct method and params
- Events arrive on the event channel in order
- Deltas arrive on the delta channel
- A `nabu.session.unsubscribe` stops delivery

- [ ] **Step 6: Implement subscribe and event delivery**

The subscribe method sends `nabu.session.subscribe {session_id}` and returns two channels. The internal loop dispatches `nabu.session.event` to the event channel and `nabu.session.delta` to the delta channel.

- [ ] **Step 7: Write the failing test for events_after**

Append tests:
- `EventsAfter` with `last_event_id: ""` returns all events
- `EventsAfter` with a valid `last_event_id` returns only events after it
- `EventsAfter` with an unknown `last_event_id` returns `nabu_cursor_unknown`

- [ ] **Step 8: Implement events_after**

The method calls `nabu.session.events_after` and decodes the `{events, synced}` response.

- [ ] **Step 9: Write the failing test for answering permission requests**

Append tests:
- `AnswerPermission` with verdict `"approve"` sends the correct response
- `AnswerPermission` with verdict `"deny"` and a reason sends the correct response
- A late answer (after the request is resolved) receives `nabu_already_resolved`

- [ ] **Step 10: Implement AnswerPermission and AnswerAsk**

These methods send a JSON-RPC response envelope with the correct `request_id` and the verdict/answer.

- [ ] **Step 11: Run all client tests**

Run: `go test ./clients/go-tui/ -v`
Expected: all pass.

- [ ] **Step 12: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

---

## Task 2: Refactor CLI to use the shared client

**Files:**
- Modify: `cmd/nabu/client.go`

**Goal:** The CLI imports the shared client and delegates to it. No other file in `cmd/nabu/` changes its imports or behaviour. The CLI's exported surface remains identical.

- [ ] **Step 1: Write the failing test for CLI import**

The test is implicit: `go build ./cmd/nabu/` must succeed and the existing CLI commands must still work. There are no existing tests in `cmd/nabu/` to update.

- [ ] **Step 2: Refactor cmd/nabu/client.go**

Replace the contents of `cmd/nabu/client.go` with a thin shim that:
- Imports `github.com/corporealshift/nabu/clients/go-tui/client`
- Re-exports every function that external callers (i.e. `commands.go`) use: `dial`, `call`, `stream`, `close`, `version`
- Each function delegates to the shared package, e.g. `func dial(ctx, addr, token) (*client.Client, error) { return client.Dial(ctx, addr, token) }`
- The internal types (`rpcError`, `message`) are removed; the shared package does not expose them, and the CLI does not need them.

The `version` variable stays local to `cmd/nabu/client.go` (it is set via `-ldflags`).

- [ ] **Step 3: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors. The CLI binary must still build.

- [ ] **Step 4: Verify CLI commands still work**

Run: `go build -o /tmp/nabu ./cmd/nabu && /tmp/nabu --help`
Expected: help output is identical to before.

---

## Task 3: Bubble Tea model and rendering

**Files:**
- Create: `clients/go-tui/model.go`
- Create: `clients/go-tui/view.go`
- Create: `clients/go-tui/model_test.go`

**Goal:** The Bubble Tea model (pure data) and view (pure function of model). No I/O. The model holds the transcript buffer, permission overlay state, disconnection state, and session state projection.

- [ ] **Step 1: Write the failing test for model initialisation**

Append to `clients/go-tui/model_test.go` a test that creates a new model with `newModel()` and verifies:
- The transcript buffer is empty
- The permission overlay is not shown
- The session state is `idle` (zero value)
- The synced flag is false

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./clients/go-tui/ -run TestModelInit -v`
Expected: compile error `undefined: newModel`

- [ ] **Step 3: Create the model**

Create `clients/go-tui/model.go` with package `client` (the model lives in the same package as the client so it can use the types directly, or in a separate `tui` sub-package — the plan puts it in `clients/go-tui/` as the same package for simplicity).

A `Model` struct with:
- `events []protocol.Event` — the transcript buffer
- `deltaBuffer map[string]string` — pending deltas keyed by `turn_id`
- `streamingTurnID string` — the current streaming turn
- `sessionState protocol.State` — the last known session state projection
- `permissionOverlay *PermissionOverlay` — nil when no overlay is shown
- `permissionQueue []*PermissionRequest` — queued requests
- `disconnected bool` — connection state
- `synced bool` — whether the client is caught up
- `statusMessage string` — transient status messages (e.g. "Replaying events…")

A `PermissionOverlay` struct with:
- `request *PermissionRequest` — the current request being answered
- `selected bool` — whether the approve button is selected (false = deny)

A `PermissionRequest` struct with:
- `RequestID string`
- `SessionID string`
- `Tool string`
- `Summary string`
- `Risk string`

`newModel() Model` — returns an initialised model.

- [ ] **Step 4: Run the init test**

Run: `go test ./clients/go-tui/ -run TestModelInit -v`
Expected: pass.

- [ ] **Step 5: Write the failing test for event rendering**

Append tests:
- An assistant `message` event appends to the transcript buffer
- A `tool_call` event appends to the transcript buffer
- A `tool_result` event appends to the transcript buffer
- A `state_change` event updates the session state and appends to the transcript
- A `message` event for a `turn_id` that has pending deltas replaces the delta buffer entry (the delta preview is superseded)
- A `session` event at position 0 sets the workspace

- [ ] **Step 6: Implement event handling in the model**

Add a method `applyEvent(protocol.Event)` to the model that:
- Appends the event to the transcript buffer
- For `message` events with `role: assistant`, stores the content in `deltaBuffer[turn_id]` if the event carries a `turn_id` (note: the `message` event does not carry `turn_id` in the protocol — the delta notification does. The model matches deltas to messages by turn order: the last delta sequence before a message is for that message)
- For `state_change` events, updates `sessionState.State`
- For `session` events, captures the workspace

Actually, the model does not need to track `turn_id` for messages. The delta buffer is keyed by `turn_id` from the delta notification. When a `message` event arrives, the model does not need to look up the delta buffer — the delta was already rendered as a preview line. The model simply appends the message to the transcript. The view function handles the preview line separately.

Simplify: `applyEvent` appends the event to the transcript. The view function manages the delta preview line separately by tracking the last assistant message's rendered text.

- [ ] **Step 7: Write the failing test for delta handling**

Append tests:
- A delta for a new `turn_id` starts a streaming preview
- Subsequent deltas for the same `turn_id` append to the preview
- A `message` event for the streaming `turn_id` ends the preview

Since the model is pure data, the test injects delta events and verifies the delta buffer state.

- [ ] **Step 8: Implement delta handling**

Add a method `applyDelta(sessionID, turnID, text string)` to the model that:
- If `streamingTurnID` is empty, sets it to `turnID` and starts the preview
- Appends `text` to `deltaBuffer[turnID]`
- When a `message` event arrives for the streaming turn, clears `streamingTurnID`

Actually, the model does not know which `turn_id` a `message` event belongs to (the event does not carry it). The view function tracks this: it shows the delta preview as the last line of the transcript while streaming, and replaces it when a new message event arrives. The model simply tracks `deltaBuffer` and `streamingTurnID`.

- [ ] **Step 9: Write the failing test for view rendering**

Append tests:
- An empty model renders a blank screen with the status bar
- A model with one assistant message renders the message text
- A model with a permission overlay renders the overlay centered on screen
- A model with `disconnected: true` renders the disconnect banner

- [ ] **Step 10: Implement the view**

Create `clients/go-tui/view.go` with the `View() tea.Cmd` method on `Model` that renders:
- A disconnect banner at the top if `disconnected`
- The transcript buffer as a scrollable list
- The delta preview line (if streaming) at the bottom of the transcript
- The permission overlay (if shown) as a centered modal
- A status bar at the very bottom showing: session state, synced status, turn count, usage

The view uses `lipgloss` for styling. The transcript is rendered as a `bubbles/list.List` or a custom scrollable block.

- [ ] **Step 11: Run all model and view tests**

Run: `go test ./clients/go-tui/ -v`
Expected: all pass.

- [ ] **Step 12: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

---

## Task 4: Bubble Tea update function and key bindings

**Files:**
- Create: `clients/go-tui/update.go`

**Goal:** The Bubble Tea update function: handles key messages, client events, and drives reconnect logic.

- [ ] **Step 1: Write the failing test for key bindings**

Append to `clients/go-tui/model_test.go` (or a new `update_test.go`) tests that:
- Pressing `Enter` when no overlay is shown does nothing (no state change)
- Pressing `Enter` when the permission overlay shows approve returns an approval
- Pressing `Esc` when the permission overlay shows deny returns a denial
- Pressing `y` approves and `n` denies
- Scroll keys (Up, Down, PgUp, PgDn) move the transcript cursor

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./clients/go-tui/ -run TestKeyBindings -v`
Expected: compile error `undefined: Model.Update` or missing update logic.

- [ ] **Step 3: Implement the update function**

Create `clients/go-tui/update.go` with the `Update(msg tea.Msg) (Model, tea.Cmd)` method on `Model`:

Key bindings:
- `Enter` — if permission overlay is shown, approve; otherwise scroll to bottom
- `Esc` — if permission overlay is shown, deny; otherwise nothing
- `y` — approve permission
- `n` — deny permission
- `Up`/`Down` — scroll transcript
- `PgUp`/`PgDn` — page scroll
- `Ctrl+C` — quit

When a permission is approved or denied, the model sends the answer through the client (via a channel or callback) and clears the overlay.

- [ ] **Step 4: Write the failing test for client event handling**

Append tests:
- An event from the client channel appends to the transcript
- A delta from the client channel updates the streaming preview
- A permission request from the client channel shows the overlay
- A disconnect signal sets `disconnected: true`
- A reconnect signal sets `disconnected: false` and triggers a replay

- [ ] **Step 5: Implement client event handling in the update function**

The update function receives messages from the client goroutine via channels. When an event arrives, `applyEvent` is called. When a delta arrives, `applyDelta` is called. When a permission request arrives, the overlay is shown. When a disconnect signal arrives, `disconnected` is set.

The client goroutine (started in `main.go`) reads from the WebSocket and sends messages to the TUI via channels. The update function processes these messages.

- [ ] **Step 6: Write the failing test for reconnect logic**

Append tests:
- When `disconnected` is true and a reconnect succeeds, `synced` is set to true
- When `disconnected` is true and a reconnect fails, `disconnected` remains true
- A replay fetches events from the last cursor position

- [ ] **Step 7: Implement reconnect logic**

When a disconnect signal is received, the update function starts a goroutine that:
- Waits with exponential backoff (starting at 1s, doubling, max 30s)
- Calls `EventsAfter` with the last event ID
- If successful, applies the replayed events and sets `synced: true`
- If failed, retries with backoff

The backoff is implemented as a `tea.Cmd` that fires after the delay.

- [ ] **Step 8: Run all update tests**

Run: `go test ./clients/go-tui/ -v`
Expected: all pass.

- [ ] **Step 9: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

---

## Task 5: Main entry point and wiring

**Files:**
- Create: `clients/go-tui/main.go`

**Goal:** The Bubble Tea program entry point. Parses `--session` flag, dials the daemon, starts the client goroutine, runs `tea.NewProgram(model)`.

- [ ] **Step 1: Write the failing test for flag parsing**

There are no unit-testable functions in `main.go` (it's the entry point). The test is implicit: `go build -o /tmp/go-tui ./clients/go-tui/ && /tmp/go-tui --help` must show the `--session` flag.

- [ ] **Step 2: Create main.go**

Create `clients/go-tui/main.go` with:
- A `--session` flag (required)
- A `--addr` flag (default `127.0.0.1:8737`)
- A `--token` flag (optional, from config)
- A `--root` flag (for finding config, same as CLI)
- `main()` that:
  1. Parses flags
  2. Loads config from `~/.nabu/config.json` (using the same config loader as the CLI, or a simple JSON parse)
  3. Creates the model
  4. Dials the daemon with `client.Dial`
  5. Subscribes to the session
  6. Fetches events from cursor (null = all)
  7. Starts the client goroutine that pumps events, deltas, and requests into the model
  8. Runs `tea.NewProgram(model).FullScreen()`

The program runs in full-screen mode. The transcript fills the available space.

- [ ] **Step 3: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

- [ ] **Step 4: Verify the TUI builds**

Run: `go build -o /tmp/go-tui ./clients/go-tui/`
Expected: binary builds successfully.

---

## Task 6: Integration tests

**Files:**
- Create: `clients/go-tui/integration_test.go`

**Goal:** End-to-end tests that spin up a local daemon and attach the TUI client to it.

- [ ] **Step 1: Write the failing integration test**

Append to `clients/go-tui/integration_test.go` a test that:
1. Starts an in-memory daemon (using the existing test helpers)
2. Creates a session
3. Attaches the TUI client
4. Sends a prompt (using the CLI client)
5. Verifies the TUI client receives the events

The test is marked with `//go:build integration`.

- [ ] **Step 2: Run the integration test**

Run: `go test -tags integration ./clients/go-tui/ -v`
Expected: pass (the test framework is set up correctly).

- [ ] **Step 3: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

---

## Summary

**Files created:**
- `clients/go-tui/client.go` — the shared JSON-RPC-over-WebSocket client
- `clients/go-tui/model.go` — the Bubble Tea model (pure data)
- `clients/go-tui/view.go` — the Bubble Tea view (pure function of model)
- `clients/go-tui/update.go` — the Bubble Tea update function (key bindings, client events, reconnect)
- `clients/go-tui/main.go` — the entry point
- `clients/go-tui/client_test.go` — tests for the shared client
- `clients/go-tui/model_test.go` — tests for the model and update function
- `clients/go-tui/integration_test.go` — end-to-end tests

**Files modified:**
- `cmd/nabu/client.go` — refactored to import and delegate to `clients/go-tui/client`

**Bubble Tea dependencies pinned:**
- `github.com/charmbracelet/bubbletea v1.3.10`
- `github.com/charmbracelet/lipgloss v1.3.0`
- `github.com/charmbracelet/bubbles v0.21.0`
- `github.com/charmbracelet/log v0.4.2`

**Key decisions:**
- Shared client at `clients/go-tui/client.go` — a Go-specific shared implementation, not a protocol contract
- Transcript renders all event types except `context`, `tasks`, and `budget` inline
- Streaming deltas shown as a live preview, superseded by the final `message` event
- Permission overlay with approve/deny, queued when another is active
- Disconnect banner with exponential backoff reconnect and event replay
- Model and update function tested directly; client tested against a fake WebSocket; integration tests marked `//go:build integration`

**Symbols confirmed in source:**
- `protocol.Event` → `protocol/types.go` — `ID`, `ParentID`, `Timestamp`, `Type`, `Data`
- `protocol.EventType` → `protocol/types.go` — 15 constants
- `protocol.MessageData`, `ToolCallData`, `ToolResultData`, `StateChangeData`, `NoticeData` → `protocol/types.go`
- `protocol.SessionState` → `protocol/types.go` — `StateIdle`, `StateRunning`, `StateBlocked`, `StatePaused`, `StateCompleted`, `StateError`
- `protocol.PermissionMode` → `protocol/types.go` — `PermissionAsk`, `PermissionAuto`, `PermissionBypass`
- `protocol.State` → `protocol/project.go` — the session projection
- `protocol.Version` → `protocol/types.go` — `"1.0"`
- `protocol.RPCError` → `protocol/errors.go` — `Code`, `Message`, `Data`
- `protocol.CodeAlreadyResolved` → `protocol/errors.go` — `-32004`
- `protocol.CodeCursorUnknown` → `protocol/errors.go` — `-32005`
- `protocol.Methods` → `protocol/errors.go` — all method names
- `client.client` (existing) → `cmd/nabu/client.go` — the existing thin JSON-RPC client to extract
- `client.dial`, `client.call`, `client.stream` → `cmd/nabu/client.go` — the functions the CLI uses
- `api.Handler` → `daemon/api/handler.go` — the WebSocket handler
- `api.connState` → `daemon/api/subscriptions.go` — connection state
- `api.fanout` → `daemon/api/subscriptions.go` — event pump
- `api.permissionReply` → `daemon/api/requests.go` — `{Verdict, Reason}`
- `api.askReply` → `daemon/api/requests.go` — `{Answer}`
- `api.DefaultRequestTimeout` → `daemon/api/requests.go` — `10 * time.Minute`
- `guard.Module.GateTool` → `daemon/modules/guard/guard.go` — returns `module.Verdict`
- `module.Module`, `module.ToolGate`, `module.Verdict`, `module.Decision` → `daemon/module/module.go`
- `module.Session.State()` → `daemon/module/module.go` — returns `protocol.State`
- `module.Session.Workspace()` → `daemon/module/module.go` — returns `module.Workspace{Path, Key}`
- `module.Config` → `daemon/module/module.go` — `map[string]any` with accessors
- `daemon/api/jsonrpcNotification` → `daemon/api/handler.go` — `{JSONRPC, Method, Params}`
- `daemon/api/jsonrpcRequest` → `daemon/api/handler.go` — `{JSONRPC, ID, Method, Params}`

**Symbols NOT confirmed (gaps):**
- `github.com/coder/websocket.Conn` interface — the exact interface shape needs to be confirmed from the vendor directory or documentation. The TUI client uses `github.com/coder/websocket` for the dial and read/write, not an interface. The fake client for testing will need to wrap a real connection or implement the needed methods.
- `tea.NewProgram` and the Bubble Tea API — the exact method signatures for `tea.Model`, `tea.Init`, `tea.Update`, `tea.View`, `tea.Cmd` need to be confirmed from the Bubble Tea documentation. The plan assumes the standard Bubble Tea API.
- `lipgloss` styling API — the exact API for creating styled blocks needs to be confirmed from the lipgloss documentation.
- `bubbles/list` — the plan references it for the transcript scrollable, but a custom scrollable block may be simpler. The decision is left to the implementer.
- `daemon/api` test helpers — the plan references "existing test helpers" for integration tests. If none exist, the implementer will need to create a test daemon or use an in-memory setup.
