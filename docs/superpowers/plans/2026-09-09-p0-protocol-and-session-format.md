# nabu P0 — Protocol and Session Format Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Define and prove nabu's normative wire contract — the append-only session
event schema, the JSON-RPC method set, and the error codes — backed by a
language-neutral conformance vector suite and a Go reference implementation that runs it.

**Architecture:** The protocol is the contract that makes nabu's polyglot client set
safe: a Go daemon, a Go TUI, a Kotlin Android client, and a later Rust GUI must all
agree on the session format. P0 produces a normative written specification, JSON Schema
definitions, a directory of conformance vectors (input event logs plus expected
results), and a Go `protocol` package with the event types, cursor logic, and a vector
runner proving the vectors pass. No daemon, no networking, no agent loop.

**Tech Stack:** Go 1.26, standard library only where practical, `go test` for the
conformance runner, JSON Schema (draft 2020-12) for schema definitions, JSON/JSONL for
vectors.

---

## Decisions settled before writing this plan

- **Single-writer log.** The daemon is the sole writer; no client appends to the
  canonical log. This eliminates merge conflicts entirely.
- **Compaction is an event, not a deletion.** A `compaction` record stores the summary
  and the range of events it covers. The log retains everything; only the model request
  shrinks.
- **First-responder-wins for requests.** When the daemon broadcasts `permission.request`
  or `ui.ask`, the first client to respond wins; late answers receive an
  `"already_resolved"` error.
- **Budget is deferred.** The spec's per-run budget concept (referenced in §10 with a
  `budget-exhausted` exit code) is unresolved. No budget fields appear in session state
  or event schemas. This is called out explicitly in the spec document.
- **Vector data is language-neutral.** Vectors are plain JSON/JSONL files with no Go,
  Kotlin, or Rust types embedded. A Kotlin implementation reads the exact same files
  and checks the exact same expectations. Expectations are themselves data files.
- **JSON-RPC method naming.** Client-to-daemon methods use `nabu.session.*` and
  `nabu.request.*` prefixes. Daemon-to-client requests use `nabu.rpc.permission.request`
  and `nabu.rpc.ui.ask`. Error codes use snake_case with a `nabu_` prefix for domain
  errors.
- **Cursor format.** A cursor is `{session_id: string, last_event_id: string | null}`.
  `last_event_id` is `null` for a brand-new session (fetch all events) and set to the
  ID of the last event the client has seen for subsequent fetches.

## Conventions

- **Language/toolchain:** Go 1.26.4 on Windows. Module path
  `github.com/corporealshift/nabu`. The first task will need to run `go mod init` —
  the repo currently contains only `docs/` and `.gitignore`.
- **Test framework:** standard library `testing`. Run with `go test ./...`.
  Per-package: `go test ./protocol/ -v`. Use table-driven tests, which is idiomatic Go
  and fits the vector suite naturally.
- **Commit message style:** the owner uses `area: lowercase summary` — NOT conventional
  commits. Real examples from their other repo: `server: GET /api/stats for the settings
  screen`, `web: IndexedDB offline write queue and instruction timer parser`,
  `docs: phase 5`. So use e.g. `protocol: append-only event log types` or
  `protocol: cursor semantics and events-after-N`. Never `feat:` or `chore:`.
- **Shell:** steps will run on Windows. Prefer commands that work in both Git Bash and
  PowerShell, or state which shell a command needs.

---

## File structure

| Path | Responsibility |
|---|---|
| `protocol/spec.md` | **New.** Normative specification: event schema, JSON-RPC methods, error codes, cursor semantics, compaction semantics. |
| `protocol/schema/event.json` | **New.** JSON Schema (draft 2020-12) for all event types — shared properties plus per-type definitions. |
| `protocol/schema/jsonrpc.json` | **New.** JSON Schema for JSON-RPC 2.0 request and response messages. |
| `protocol/vectors/event-ordering/01-valid-chronological.json` | **New.** Vector: strictly chronological events render in insertion order. |
| `protocol/vectors/event-ordering/02-out-of-order-timestamps.json` | **New.** Vector: events with non-monotonic timestamps still render in insertion order (append-only log). |
| `protocol/vectors/cursor-semantics/01-fetch-all-new-session.json` | **New.** Vector: cursor with `last_event_id: null` returns all events. |
| `protocol/vectors/cursor-semantics/02-fetch-after-last-event.json` | **New.** Vector: a cursor pointing at the last event returns an empty set — there is nothing new to fetch. |
| `protocol/vectors/cursor-semantics/03-fetch-after-oldest-event.json` | **New.** Vector: cursor pointing at the first event returns everything after it. |
| `protocol/vectors/cursor-semantics/04-fetch-after-nonexistent-event.json` | **New.** Vector: cursor with an event ID not in the log returns an empty set. |
| `protocol/vectors/compaction/01-compacted-session-rendering.json` | **New.** Vector: a session with a compaction event renders the full log (pre-compaction events are still present). |
| `protocol/vectors/partial-sync/01-partially-synced-session.json` | **New.** Vector: a session where the client has synced to event 5 of 10 renders events 1–5 and marks the session as partially synced. |
| `go.mod` | **New.** Module root at the **repo root** (`github.com/corporealshift/nabu`), so P1's `daemon/` packages share one module. The protocol package imports as `github.com/corporealshift/nabu/protocol`. |
| `protocol/types.go` | **New.** Event types, cursor type, constants. |
| `protocol/types_test.go` | **New.** Table-driven tests for event parsing and validation. |
| `protocol/cursor.go` | **New.** Cursor type and `EventsAfter()` logic. |
| `protocol/cursor_test.go` | **New.** Table-driven tests for cursor semantics. |
| `protocol/vectors.go` | **New.** Vector runner: loads vector files, executes them, compares results. |
| `protocol/vectors_test.go` | **New.** `TestVectors` — runs every vector file. |

---

### Task 1: Go module and directory scaffolding

**Files:** Create `go.mod` at the **repo root**; create empty directories under `protocol/`.

This is the plumbing task: initialize the Go module and create the directory tree.

The `go.mod` goes at the **repo root**, not inside `protocol/`. The architecture spec's
repository layout (section 16) puts `daemon/`, `clients/`, and `protocol/` side by side,
so a module rooted at `protocol/` would leave every P1 daemon package outside the module.
One module at the root covers all of them; the protocol package's import path is
`github.com/corporealshift/nabu/protocol`.

- [ ] **Step 1: Initialize the module and create directories**

Run in Git Bash:
```bash
go mod init github.com/corporealshift/nabu
mkdir -p protocol/schema protocol/vectors/event-ordering protocol/vectors/cursor-semantics protocol/vectors/compaction protocol/vectors/partial-sync
```

Expected output:
```
go: creating new go.mod: module github.com/corporealshift/nabu
```

Verify:
```bash
cat go.mod
```

Expected:
```
module github.com/corporealshift/nabu

go 1.26
```

- [ ] **Step 2: Verify the module and the directory tree**

Run:
```bash
go env GOMOD && ls -d protocol/schema protocol/vectors/*
```

Expected: the absolute path of the repo-root `go.mod`, followed by the five directories
just created. Do not run `go build ./...` yet — `protocol/` contains no `.go` files at
this point, and Go reports "matched no packages" for an empty directory.

- [ ] **Step 3: Commit**

```bash
git add go.mod protocol/
git commit -m "protocol: module scaffolding and directory tree"
```

---

### Task 2: Normative protocol specification

**Files:** Create `protocol/spec.md`

This is the written contract. It covers the event schema, JSON-RPC method set, error
codes, cursor semantics, and compaction semantics. It is human-readable and normative
— a Kotlin or Rust implementer reads this document to know what to build.

> **Implementer note — this is the largest task in the plan.** Step 1 below writes
> roughly 620 lines in one file. Do not attempt it as a single write if your context or
> budget is tight. Write it in three passes, saving after each, and commit after the
> third:
>
> 1. Sections 1–4 (Transport, Session event schema, Cursor semantics, Compaction rendering)
> 2. Section 5 (JSON-RPC method set) — the largest single section, ~330 lines
> 3. Sections 6–8 (Error codes, Language-neutral conformance vectors, Open questions)
>
> The section numbering and content below are normative — write them verbatim. Splitting
> is purely about how many passes you take, not about what ends up in the file.

- [ ] **Step 1: Write the specification**

`protocol/spec.md`:
```markdown
# Nabu Protocol Specification

**Version:** 0.1.0
**Date:** 2026-09-09
**Status:** Normative — P0 baseline

This document defines nabu's wire contract. Every client (Go TUI, Kotlin Android,
Rust GUI) and the Go daemon MUST conform to it. The conformance vector suite
(`vectors/`) is the executable proof.

---

## 1. Transport

JSON-RPC 2.0 over WebSocket, bound to loopback and the Tailscale interface.
Connections from non-loopback addresses require a bearer token from config.

## 2. Session event schema

Every event in the append-only log carries these **required** properties:

| Property | Type | Description |
|---|---|---|
| `id` | string | Unique event identifier, globally unique across sessions. |
| `parent_id` | string \| null | ID of the predecessor event. `null` for the first event in the session. |
| `timestamp` | string (RFC 3339) | When the event was created. |
| `type` | string | One of the event types below. |
| `data` | object | Event-specific payload. Shape depends on `type`. |

The first event in every session MUST have `type: "session"` and `parent_id: null`.
Subsequent events chain via `parent_id` to the immediately preceding event.

### 2.1 Event types

#### `session` — session initialization

Marks the beginning of a session.

```jsonc
{
  "id": "evt-001",
  "parent_id": null,
  "timestamp": "2026-09-09T12:00:00Z",
  "type": "session",
  "data": {
    "workspace": "/home/user/project",
    "model": "qwen3.6-35b-a3b",
    "compaction_enabled": true
  }
}
```

| Field | Type | Description |
|---|---|---|
| `workspace` | string | The workspace path the session operates in. |
| `model` | string | The model provider identifier (e.g. `qwen3.6-35b-a3b`). |
| `compaction_enabled` | boolean | Whether auto-compaction is on for this session. |

**Budget note:** The session state conceptually carries a per-run budget field, but
this is deferred pending resolution of the open question in the architecture spec
(§16 "Open questions for Claude"). No budget field appears here.

#### `message` — a user or assistant message

Carries a single turn in the conversation.

```jsonc
{
  "id": "evt-002",
  "parent_id": "evt-001",
  "timestamp": "2026-09-09T12:00:01Z",
  "type": "message",
  "data": {
    "role": "user",
    "content": "Write me a function that sorts a list."
  }
}
```

| Field | Type | Description |
|---|---|---|
| `role` | string | `"user"` or `"assistant"`. |
| `content` | string | The message text. |

#### `tool_call` — a tool invocation by the assistant

```jsonc
{
  "id": "evt-003",
  "parent_id": "evt-002",
  "timestamp": "2026-09-09T12:00:02Z",
  "type": "tool_call",
  "data": {
    "tool": "read",
    "arguments": { "path": "/home/user/main.go" }
  }
}
```

| Field | Type | Description |
|---|---|---|
| `tool` | string | The tool name (e.g. `read`, `write`, `bash`, `edit`, `glob`, `grep`). |
| `arguments` | object | Tool-specific arguments. |

#### `tool_result` — the result of a tool invocation

```jsonc
{
  "id": "evt-004",
  "parent_id": "evt-003",
  "timestamp": "2026-09-09T12:00:03Z",
  "type": "tool_result",
  "data": {
    "tool": "read",
    "content": "package main\n\nfunc main() {}",
    "status": "ok"
  }
}
```

| Field | Type | Description |
|---|---|---|
| `tool` | string | The tool name (must match the paired `tool_call`). |
| `content` | string | The tool's output. |
| `status` | string | `"ok"` or `"error"`. |

#### `model_change` — switch to a different model

```jsonc
{
  "id": "evt-010",
  "parent_id": "evt-009",
  "timestamp": "2026-09-09T12:05:00Z",
  "type": "model_change",
  "data": {
    "from": "qwen3.6-35b-a3b",
    "to": "gpt-4o"
  }
}
```

| Field | Type | Description |
|---|---|---|
| `from` | string | The previous model identifier. |
| `to` | string | The new model identifier. |

#### `compaction` — a compaction event

Compaction is an event, **not a deletion**. The log retains all events; the summary
here records what was compacted.

```jsonc
{
  "id": "evt-020",
  "parent_id": "evt-019",
  "timestamp": "2026-09-09T12:30:00Z",
  "type": "compaction",
  "data": {
    "summary": "User asked for a sorting function. Assistant provided a Go implementation using bubble sort.",
    "range_start": "evt-002",
    "range_end": "evt-019"
  }
}
```

| Field | Type | Description |
|---|---|---|
| `summary` | string | The generated compaction summary. |
| `range_start` | string | ID of the first event covered by this compaction. |
| `range_end` | string | ID of the last event covered by this compaction. |

#### `report` — a run report

Emitted at session end. Core defines the schema; plugins supply the content.

```jsonc
{
  "id": "evt-030",
  "parent_id": "evt-029",
  "timestamp": "2026-09-09T12:35:00Z",
  "type": "report",
  "data": {
    "files_touched": ["main.go", "sort_test.go"],
    "exit_status": "completed"
  }
}
```

| Field | Type | Description |
|---|---|---|
| `files_touched` | string[] | Files modified during the session. |
| `exit_status` | string | One of: `"completed"`, `"blocked"`, `"error"`. |

### 2.2 Ordering

Events in the log are ordered by **insertion order** (append-only). The `timestamp`
field records when an event was created but does NOT determine render order. An event
with an earlier timestamp than its predecessor is still rendered after that predecessor
(because `parent_id` chains define the order, not timestamps).

## 3. Cursor semantics

A cursor tells the daemon which events the client has already seen.

### 3.1 Cursor format

```jsonc
{
  "session_id": "sess-abc123",
  "last_event_id": "evt-005"
}
```

| Field | Type | Description |
|---|---|---|
| `session_id` | string | The session the cursor belongs to. |
| `last_event_id` | string \| null | The ID of the last event the client has. `null` means "fetch all events" (brand-new client). |

### 3.2 `events_after` behavior

The daemon returns events whose `parent_id` chain starts after `last_event_id`:

- If `last_event_id` is `null`, return **all** events in the session.
- If `last_event_id` is a valid event ID present in the log, return all events whose
  `parent_id` chain leads from that event forward (i.e., events after it in insertion order).
- If `last_event_id` is an ID **not present** in the log, return an **empty set** (the
  client's state is too stale to resume from).

The returned events form a contiguous chain from the event following `last_event_id`
to the current head of the log.

### 3.3 Partial sync detection

A session is **partially synced** when the client's cursor points to an event that is
not the last event in the session. The daemon signals this by including a `synced`
boolean in the response: `false` means more events are available.

## 4. Compaction rendering

A compaction event does **not** remove events from the log. When rendering a session:

1. All events from `session` to the current head are present in the log.
2. A `compaction` event carries a `range_start` and `range_end` indicating which
   events were compacted.
3. The `events_after` response includes compaction events and all events within their
   range — compaction changes the *request* to the model, never the *record*.
4. Clients MAY choose to visually collapse compacted ranges, but they MUST be able to
   expand and show the full pre-compaction history.

## 5. JSON-RPC method set

All methods use JSON-RPC 2.0 over WebSocket. The daemon is the server; clients are
the callers.

### 5.1 Client-to-daemon methods

#### `nabu.session.list`

List all sessions the daemon knows about.

**Request:**
```jsonc
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "nabu.session.list",
  "params": {}
}
```

**Response:**
```jsonc
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "sessions": [
      {
        "session_id": "sess-abc123",
        "workspace": "/home/user/project",
        "state": "running",
        "event_count": 42,
        "created_at": "2026-09-09T12:00:00Z",
        "updated_at": "2026-09-09T12:35:00Z"
      }
    ]
  }
}
```

| Session field | Type | Description |
|---|---|---|
| `session_id` | string | Unique session identifier. |
| `workspace` | string | The workspace path. |
| `state` | string | `"idle"`, `"running"`, `"blocked"`, `"completed"`, `"error"`. |
| `event_count` | integer | Total events in the session log. |
| `created_at` | string (RFC 3339) | When the session was created. |
| `updated_at` | string (RFC 3339) | When the last event was appended. |

#### `nabu.session.events_after`

Fetch events after a cursor.

**Request:**
```jsonc
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "nabu.session.events_after",
  "params": {
    "session_id": "sess-abc123",
    "last_event_id": "evt-005"
  }
}
```

**Response:**
```jsonc
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "events": [
      { "id": "evt-006", "parent_id": "evt-005", "timestamp": "...", "type": "message", "data": { ... } }
    ],
    "synced": true
  }
}
```

| Field | Type | Description |
|---|---|---|
| `events` | event[] | Events after `last_event_id` in insertion order. |
| `synced` | boolean | `true` if the client has seen all events; `false` if more are available. |

#### `nabu.session.create`

Create a new session.

**Request:**
```jsonc
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "nabu.session.create",
  "params": {
    "workspace": "/home/user/project",
    "model": "qwen3.6-35b-a3b",
    "compaction_enabled": true
  }
}
```

**Response:**
```jsonc
{
  "jsonrpc": "2.0",
  "id": 3,
  "result": {
    "session_id": "sess-def456",
    "event": { /* the initial session event */ }
  }
}
```

#### `nabu.session.send_prompt`

Send a user prompt into a running or idle session.

**Request:**
```jsonc
{
  "jsonrpc": "2.0",
  "id": 4,
  "method": "nabu.session.send_prompt",
  "params": {
    "session_id": "sess-abc123",
    "content": "Write me a function that sorts a list."
  }
}
```

**Response:**
```jsonc
{
  "jsonrpc": "2.0",
  "id": 4,
  "result": {
    "event_id": "evt-042"
  }
}
```

#### `nabu.session.subscribe`

Subscribe to live events for a session.

**Request:**
```jsonc
{
  "jsonrpc": "2.0",
  "id": 5,
  "method": "nabu.session.subscribe",
  "params": {
    "session_id": "sess-abc123"
  }
}
```

**Response:**
```jsonc
{
  "jsonrpc": "2.0",
  "id": 5,
  "result": {
    "subscribed": true
  }
}
```

After subscribing, the daemon sends unsolicited notifications for new events:

```jsonc
{
  "jsonrpc": "2.0",
  "method": "nabu.session.event",
  "params": {
    "event": { /* new event */ }
  }
}
```

#### `nabu.session.interrupt`

Interrupt a running session (stop the agent loop mid-turn).

**Request:**
```jsonc
{
  "jsonrpc": "2.0",
  "id": 6,
  "method": "nabu.session.interrupt",
  "params": {
    "session_id": "sess-abc123"
  }
}
```

**Response:**
```jsonc
{
  "jsonrpc": "2.0",
  "id": 6,
  "result": {}
}
```

#### `nabu.session.stop`

Stop a session completely (graceful shutdown).

**Request:**
```jsonc
{
  "jsonrpc": "2.0",
  "id": 7,
  "method": "nabu.session.stop",
  "params": {
    "session_id": "sess-abc123"
  }
}
```

**Response:**
```jsonc
{
  "jsonrpc": "2.0",
  "id": 7,
  "result": {}
}
```

### 5.2 Daemon-to-client requests

These are requests the daemon sends to clients and expects a response. They use the
JSON-RPC request pattern (no `result` field in the outgoing message; the client
sends back a response).

#### `nabu.rpc.permission.request`

Asks a client to approve or deny a gated tool call.

**Daemon → Client:**
```jsonc
{
  "jsonrpc": "2.0",
  "id": 100,
  "method": "nabu.rpc.permission.request",
  "params": {
    "session_id": "sess-abc123",
    "tool": "bash",
    "command": "rm -rf /tmp/test",
    "risk_level": "high"
  }
}
```

| Field | Type | Description |
|---|---|---|
| `session_id` | string | The session this pertains to. |
| `tool` | string | The gated tool name. |
| `command` | string | The command or action being gated. |
| `risk_level` | string | `"low"`, `"medium"`, `"high"`. |

**Client response (approve):**
```jsonc
{
  "jsonrpc": "2.0",
  "id": 100,
  "result": {
    "verdict": "approved"
  }
}
```

**Client response (deny):**
```jsonc
{
  "jsonrpc": "2.0",
  "id": 100,
  "result": {
    "verdict": "denied"
  }
}
```

#### `nabu.rpc.ui.ask`

Asks a client for user input (e.g. a plugin needs clarification).

**Daemon → Client:**
```jsonc
{
  "jsonrpc": "2.0",
  "id": 101,
  "method": "nabu.rpc.ui.ask",
  "params": {
    "session_id": "sess-abc123",
    "question": "Which directory should I create the project in?"
  }
}
```

| Field | Type | Description |
|---|---|---|
| `session_id` | string | The session this pertains to. |
| `question` | string | The question text to present to the user. |

**Client response:**
```jsonc
{
  "jsonrpc": "2.0",
  "id": 101,
  "result": {
    "answer": "/home/user/my-project"
  }
}
```

### 5.3 First-responder-wins semantics

When the daemon broadcasts a request to multiple attached clients:

1. The daemon sends the request to all connected clients for that session.
2. The **first** client to send a matching JSON-RPC response wins — its verdict is
   applied.
3. Any **subsequent** responses receive an error with code `nabu_already_resolved`.
4. The winning client's response resolves the request; the daemon sends the verdict
   to the plugin/tool that initiated it.

## 6. Error codes

Standard JSON-RPC 2.0 error codes and nabu-specific codes:

| Code | Name | Description |
|---|---|---|
| `-32700` | `parse_error` | Invalid JSON was received. |
| `-32600` | `invalid_request` | The JSON sent is not a valid request object. |
| `-32601` | `method_not_found` | The method does not exist. |
| `-32602` | `invalid_params` | Invalid method parameters. |
| `-32603` | `internal_error` | Internal daemon error. |
| `-32001` | `session_not_found` | The session ID does not exist. |
| `-32002` | `session_not_running` | The session is not in a running state. |
| `-32003` | `permission_denied` | The caller lacks permission. |
| `-32004` | `already_resolved` | A request was answered by another client first. |

## 7. Language-neutral conformance vectors

The `vectors/` directory contains language-neutral test cases. Each vector is a JSON
file with:

- `name`: Human-readable name.
- `description`: What this vector tests.
- `input`: The event log (array of events).
- `operations`: Operations to perform (e.g. `events_after` with a cursor).
- `expectation`: The expected result.

A Kotlin implementation reads the same files, runs the same operations, and checks
the same expectations. See `vectors/event-ordering/01-valid-chronological.json` for
the format.

---

## 8. Open questions

**Per-run budget.** The architecture spec (§10) references a `budget-exhausted` exit
code and states "budget is reported as session state," but nabu's budget concept is
nowhere defined. This plan does NOT define budget semantics. The session event schema
omits any budget field. This must be resolved before P1.
```

- [ ] **Step 2: Verify the spec is readable and complete**

Read back the file and confirm it covers:
- Event schema (§2) with all 7 event types
- Cursor semantics (§3) with `events_after` behavior
- Compaction rendering (§4)
- JSON-RPC method set (§5) with all client-to-daemon and daemon-to-client methods
- Error codes (§6)
- Vector format (§7)
- Open question note (§8)

- [ ] **Step 3: Commit**

```bash
git add protocol/spec.md
git commit -m "protocol: normative specification document"
```

---

### Task 3: JSON Schema definitions

**Files:** Create `protocol/schema/event.json`, `protocol/schema/jsonrpc.json`

JSON Schema (draft 2020-12) definitions for every event type and JSON-RPC message
shape. These are reference schemas — they validate the vector data and the Go types'
JSON output.

- [ ] **Step 1: Write the event schema**

`protocol/schema/event.json`:
```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://github.com/corporealshift/nabu/protocol/schema/event.json",
  "title": "Nabu Session Event",
  "description": "An event in the append-only session log. All events carry id, parent_id, timestamp, and type.",
  "oneOf": [
    { "$ref": "#/$defs/sessionEvent" },
    { "$ref": "#/$defs/messageEvent" },
    { "$ref": "#/$defs/toolCallEvent" },
    { "$ref": "#/$defs/toolResultEvent" },
    { "$ref": "#/$defs/modelChangeEvent" },
    { "$ref": "#/$defs/compactionEvent" },
    { "$ref": "#/$defs/reportEvent" }
  ],
  "$defs": {
    "baseEvent": {
      "type": "object",
      "required": ["id", "parent_id", "timestamp", "type", "data"],
      "properties": {
        "id": { "type": "string", "description": "Unique event identifier." },
        "parent_id": { "type": ["string", "null"], "description": "ID of the predecessor event. null for the first event." },
        "timestamp": { "type": "string", "format": "date-time", "description": "RFC 3339 timestamp." },
        "type": {
          "type": "string",
          "enum": ["session", "message", "tool_call", "tool_result", "model_change", "compaction", "report"]
        },
        "data": { "type": "object" }
      },
      "additionalProperties": false
    },
    "sessionEvent": {
      "allOf": [
        { "$ref": "#/$defs/baseEvent" },
        {
          "properties": {
            "type": { "const": "session" },
            "data": {
              "type": "object",
              "required": ["workspace", "model", "compaction_enabled"],
              "properties": {
                "workspace": { "type": "string" },
                "model": { "type": "string" },
                "compaction_enabled": { "type": "boolean" }
              },
              "additionalProperties": false
            }
          }
        }
      ]
    },
    "messageEvent": {
      "allOf": [
        { "$ref": "#/$defs/baseEvent" },
        {
          "properties": {
            "type": { "const": "message" },
            "data": {
              "type": "object",
              "required": ["role", "content"],
              "properties": {
                "role": { "type": "string", "enum": ["user", "assistant"] },
                "content": { "type": "string" }
              },
              "additionalProperties": false
            }
          }
        }
      ]
    },
    "toolCallEvent": {
      "allOf": [
        { "$ref": "#/$defs/baseEvent" },
        {
          "properties": {
            "type": { "const": "tool_call" },
            "data": {
              "type": "object",
              "required": ["tool", "arguments"],
              "properties": {
                "tool": { "type": "string" },
                "arguments": { "type": "object" }
              },
              "additionalProperties": false
            }
          }
        }
      ]
    },
    "toolResultEvent": {
      "allOf": [
        { "$ref": "#/$defs/baseEvent" },
        {
          "properties": {
            "type": { "const": "tool_result" },
            "data": {
              "type": "object",
              "required": ["tool", "content", "status"],
              "properties": {
                "tool": { "type": "string" },
                "content": { "type": "string" },
                "status": { "type": "string", "enum": ["ok", "error"] }
              },
              "additionalProperties": false
            }
          }
        }
      ]
    },
    "modelChangeEvent": {
      "allOf": [
        { "$ref": "#/$defs/baseEvent" },
        {
          "properties": {
            "type": { "const": "model_change" },
            "data": {
              "type": "object",
              "required": ["from", "to"],
              "properties": {
                "from": { "type": "string" },
                "to": { "type": "string" }
              },
              "additionalProperties": false
            }
          }
        }
      ]
    },
    "compactionEvent": {
      "allOf": [
        { "$ref": "#/$defs/baseEvent" },
        {
          "properties": {
            "type": { "const": "compaction" },
            "data": {
              "type": "object",
              "required": ["summary", "range_start", "range_end"],
              "properties": {
                "summary": { "type": "string" },
                "range_start": { "type": "string" },
                "range_end": { "type": "string" }
              },
              "additionalProperties": false
            }
          }
        }
      ]
    },
    "reportEvent": {
      "allOf": [
        { "$ref": "#/$defs/baseEvent" },
        {
          "properties": {
            "type": { "const": "report" },
            "data": {
              "type": "object",
              "required": ["files_touched", "exit_status"],
              "properties": {
                "files_touched": {
                  "type": "array",
                  "items": { "type": "string" }
                },
                "exit_status": { "type": "string", "enum": ["completed", "blocked", "error"] }
              },
              "additionalProperties": false
            }
          }
        }
      ]
    }
  }
}
```

- [ ] **Step 2: Write the JSON-RPC schema**

`protocol/schema/jsonrpc.json`:
```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://github.com/corporealshift/nabu/protocol/schema/jsonrpc.json",
  "title": "Nabu JSON-RPC 2.0 Message",
  "description": "JSON-RPC 2.0 request, response, or error message over WebSocket.",
  "oneOf": [
    { "$ref": "#/$defs/request" },
    { "$ref": "#/$defs/response" },
    { "$ref": "#/$defs/errorResponse" },
    { "$ref": "#/$defs/notification" }
  ],
  "$defs": {
    "baseMessage": {
      "type": "object",
      "required": ["jsonrpc", "id"],
      "properties": {
        "jsonrpc": { "const": "2.0" },
        "id": { "type": ["integer", "string"] }
      },
      "additionalProperties": false
    },
    "request": {
      "allOf": [
        { "$ref": "#/$defs/baseMessage" },
        {
          "required": ["method"],
          "properties": {
            "method": {
              "type": "string",
              "enum": [
                "nabu.session.list",
                "nabu.session.events_after",
                "nabu.session.create",
                "nabu.session.send_prompt",
                "nabu.session.subscribe",
                "nabu.session.interrupt",
                "nabu.session.stop",
                "nabu.rpc.permission.request",
                "nabu.rpc.ui.ask"
              ]
            },
            "params": { "type": ["object", "null"] }
          }
        }
      ]
    },
    "response": {
      "allOf": [
        { "$ref": "#/$defs/baseMessage" },
        {
          "required": ["result"],
          "properties": {
            "result": { "type": ["object", "null"] }
          }
        }
      ]
    },
    "errorResponse": {
      "allOf": [
        { "$ref": "#/$defs/baseMessage" },
        {
          "required": ["error"],
          "properties": {
            "error": {
              "type": "object",
              "required": ["code", "message"],
              "properties": {
                "code": { "type": "integer" },
                "message": { "type": "string" }
              },
              "additionalProperties": false
            }
          }
        }
      ]
    },
    "notification": {
      "type": "object",
      "required": ["jsonrpc", "method", "params"],
      "properties": {
        "jsonrpc": { "const": "2.0" },
        "method": { "const": "nabu.session.event" },
        "params": {
          "type": "object",
          "required": ["event"],
          "properties": {
            "event": { "$ref": "event.json" }
          },
          "additionalProperties": false
        }
      },
      "additionalProperties": false
    }
  }
}
```

- [ ] **Step 3: Validate the schemas parse correctly**

Run (requires `python3` with `jsonschema` or any JSON Schema validator):
```bash
python3 -c "import json; json.load(open('protocol/schema/event.json')); print('event.json: valid')"
python3 -c "import json; json.load(open('protocol/schema/jsonrpc.json')); print('jsonrpc.json: valid')"
```

Expected:
```
event.json: valid
jsonrpc.json: valid
```

- [ ] **Step 4: Commit**

```bash
git add protocol/schema/event.json protocol/schema/jsonrpc.json
git commit -m "protocol: JSON Schema definitions for events and JSON-RPC messages"
```
---

### Task 4: Go event types and serialization

**Files:** Create `protocol/types.go`, `protocol/types_test.go`

The core Go types that mirror the event schema. Table-driven tests validate parsing
and round-trip serialization.

- [ ] **Step 1: Write the types**

`protocol/types.go`:
```go
// Package protocol defines nabu's session event types, cursor logic, and
// conformance vector runner. It is a language-neutral contract: the same
// event schema is expressed as JSON Schema in schema/ and validated by
// vectors/ in both Go and (later) Kotlin.
package protocol

import (
	"encoding/json"
	"fmt"
	"time"
)

// EventType is the type of a session event.
type EventType string

const (
	EventSession      EventType = "session"
	EventMessage      EventType = "message"
	EventToolCall     EventType = "tool_call"
	EventToolResult   EventType = "tool_result"
	EventModelChange  EventType = "model_change"
	EventCompaction   EventType = "compaction"
	EventReport       EventType = "report"
)

// ValidEventTypes returns all recognized event type strings.
func ValidEventTypes() []string {
	return []string{
		"session", "message", "tool_call", "tool_result",
		"model_change", "compaction", "report",
	}
}

// Event is a single entry in the append-only session log.
type Event struct {
	ID        string          `json:"id"`
	ParentID  *string         `json:"parent_id"`
	Timestamp time.Time       `json:"timestamp"`
	Type      EventType       `json:"type"`
	Data      json.RawMessage `json:"data"`
}

// SessionData is the data payload for a session event.
type SessionData struct {
	Workspace         string `json:"workspace"`
	Model             string `json:"model"`
	CompactionEnabled bool   `json:"compaction_enabled"`
}

// MessageData is the data payload for a message event.
type MessageData struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ToolCallData is the data payload for a tool_call event.
type ToolCallData struct {
	Tool      string                 `json:"tool"`
	Arguments map[string]interface{} `json:"arguments"`
}

// ToolResultData is the data payload for a tool_result event.
type ToolResultData struct {
	Tool    string `json:"tool"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

// ModelChangeData is the data payload for a model_change event.
type ModelChangeData struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// CompactionData is the data payload for a compaction event.
type CompactionData struct {
	Summary    string `json:"summary"`
	RangeStart string `json:"range_start"`
	RangeEnd   string `json:"range_end"`
}

// ReportData is the data payload for a report event.
type ReportData struct {
	FilesTouched []string `json:"files_touched"`
	ExitStatus   string   `json:"exit_status"`
}

// UnmarshalJSON implements custom JSON unmarshaling to validate the event
// type and parse the data field into the appropriate struct.
func (e *Event) UnmarshalJSON(data []byte) error {
	// First, decode into a raw map to extract the type.
	var raw struct {
		ID        string          `json:"id"`
		ParentID  *string         `json:"parent_id"`
		Timestamp string          `json:"timestamp"`
		Type      EventType       `json:"type"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("unmarshal event: %w", err)
	}

	// Validate the event type.
	valid := false
	for _, vt := range ValidEventTypes() {
		if vt == string(raw.Type) {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("unmarshal event: unknown type %q", raw.Type)
	}

	// Parse the timestamp.
	ts, err := time.Parse(time.RFC3339, raw.Timestamp)
	if err != nil {
		return fmt.Errorf("unmarshal event: invalid timestamp %q: %w", raw.Timestamp, err)
	}

	e.ID = raw.ID
	e.ParentID = raw.ParentID
	e.Timestamp = ts
	e.Type = raw.Type
	e.Data = raw.Data

	return nil
}

// MarshalJSON implements custom JSON marshaling. It delegates to the default
// behavior since all fields are properly tagged.
func (e Event) MarshalJSON() ([]byte, error) {
	type Alias Event
	return json.Marshal(&struct {
		Timestamp string `json:"timestamp"`
		Alias
	}{
		Timestamp: e.Timestamp.Format(time.RFC3339),
		Alias:     (Alias)(e),
	})
}

// Validate checks that the event has required fields populated.
func (e Event) Validate() error {
	if e.ID == "" {
		return fmt.Errorf("event %q: id is required", e.ID)
	}
	if e.Type == "" {
		return fmt.Errorf("event %q: type is required", e.ID)
	}
	if e.Data == nil {
		return fmt.Errorf("event %q: data is required", e.ID)
	}
	return nil
}

// ParseEvents decodes a JSON array of event objects into a slice.
func ParseEvents(data []byte) ([]Event, error) {
	var events []Event
	if err := json.Unmarshal(data, &events); err != nil {
		return nil, fmt.Errorf("parse events: %w", err)
	}
	return events, nil
}
```

- [ ] **Step 2: Write the tests**

`protocol/types_test.go`:
```go
package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseEvents(t *testing.T) {
	input := `[
		{
			"id": "evt-001",
			"parent_id": null,
			"timestamp": "2026-09-09T12:00:00Z",
			"type": "session",
			"data": {"workspace": "/tmp", "model": "qwen", "compaction_enabled": true}
		},
		{
			"id": "evt-002",
			"parent_id": "evt-001",
			"timestamp": "2026-09-09T12:00:01Z",
			"type": "message",
			"data": {"role": "user", "content": "hello"}
		}
	]`

	events, err := ParseEvents([]byte(input))
	if err != nil {
		t.Fatalf("ParseEvents: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	if events[0].ID != "evt-001" {
		t.Errorf("evt[0].ID = %q, want %q", events[0].ID, "evt-001")
	}
	if events[0].Type != EventSession {
		t.Errorf("evt[0].Type = %q, want %q", events[0].Type, EventSession)
	}
	if events[0].ParentID != nil {
		t.Errorf("evt[0].ParentID = %v, want nil", *events[0].ParentID)
	}
	if events[1].ParentID == nil || *events[1].ParentID != "evt-001" {
		t.Errorf("evt[1].ParentID = %v, want %q", events[1].ParentID, "evt-001")
	}
}

func TestParseEventsInvalidType(t *testing.T) {
	input := `[{"id":"e1","parent_id":null,"timestamp":"2026-09-09T12:00:00Z","type":"bogus","data":{}}]`
	_, err := ParseEvents([]byte(input))
	if err == nil {
		t.Fatal("expected error for invalid type, got nil")
	}
}

func TestParseEventsInvalidTimestamp(t *testing.T) {
	input := `[{"id":"e1","parent_id":null,"timestamp":"not-a-date","type":"session","data":{"workspace":"/","model":"m","compaction_enabled":true}}]`
	_, err := ParseEvents([]byte(input))
	if err == nil {
		t.Fatal("expected error for invalid timestamp, got nil")
	}
}

func TestEventRoundTrip(t *testing.T) {
	parentID := "evt-001"
	original := Event{
		ID:        "evt-002",
		ParentID:  &parentID,
		Timestamp: time.Date(2026, 9, 9, 12, 0, 1, 0, time.UTC),
		Type:      EventMessage,
		Data:      json.RawMessage(`{"role":"user","content":"hi"}`),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded Event
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID = %q, want %q", decoded.ID, original.ID)
	}
	if decoded.Type != original.Type {
		t.Errorf("Type = %q, want %q", decoded.Type, original.Type)
	}
	if decoded.ParentID == nil || *decoded.ParentID != *original.ParentID {
		t.Errorf("ParentID = %v, want %v", decoded.ParentID, original.ParentID)
	}
	if !decoded.Timestamp.Equal(original.Timestamp) {
		t.Errorf("Timestamp = %v, want %v", decoded.Timestamp, original.Timestamp)
	}
}

func TestEventValidate(t *testing.T) {
	tests := []struct {
		name    string
		event   Event
		wantErr bool
	}{
		{
			name:    "valid event",
			event:   Event{ID: "e1", Type: EventSession, Data: json.RawMessage(`{}`)},
			wantErr: false,
		},
		{
			name:    "missing id",
			event:   Event{Type: EventSession, Data: json.RawMessage(`{}`)},
			wantErr: true,
		},
		{
			name:    "missing type",
			event:   Event{ID: "e1", Data: json.RawMessage(`{}`)},
			wantErr: true,
		},
		{
			name:    "missing data",
			event:   Event{ID: "e1", Type: EventSession},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.event.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestParseAllEventTypes(t *testing.T) {
	type testCase struct {
		eventType EventType
		rawData   string
	}

	cases := []testCase{
		{EventSession, `{"workspace":"/x","model":"m","compaction_enabled":true}`},
		{EventMessage, `{"role":"user","content":"hi"}`},
		{EventToolCall, `{"tool":"read","arguments":{"path":"f.go"}}`},
		{EventToolResult, `{"tool":"read","content":"ok","status":"ok"}`},
		{EventModelChange, `{"from":"a","to":"b"}`},
		{EventCompaction, `{"summary":"sum","range_start":"e1","range_end":"e5"}`},
		{EventReport, `{"files_touched":["a.go"],"exit_status":"completed"}`},
	}

	for _, tc := range cases {
		t.Run(string(tc.eventType), func(t *testing.T) {
			input := `[{"id":"e1","parent_id":null,"timestamp":"2026-09-09T12:00:00Z","type":"` + string(tc.eventType) + `","data":` + tc.rawData + `}]`
			events, err := ParseEvents([]byte(input))
			if err != nil {
				t.Fatalf("ParseEvents(%s): %v", tc.eventType, err)
			}
			if len(events) != 1 {
				t.Fatalf("expected 1 event, got %d", len(events))
			}
			if events[0].Type != tc.eventType {
				t.Errorf("Type = %q, want %q", events[0].Type, tc.eventType)
			}
		})
	}
}
```

- [ ] **Step 3: Run the tests**

Run:
```bash
go test ./protocol/ -v
```

Expected: all 8 tests pass.

- [ ] **Step 4: Commit**

```bash
git add protocol/types.go protocol/types_test.go
git commit -m "protocol: event types, parsing, and validation"
```

---

### Task 5: Cursor logic

**Files:** Create `protocol/cursor.go`, `protocol/cursor_test.go`

The cursor type and the `EventsAfter` function that implements the semantics from
spec section 3.2: given a cursor and an event log, return the events after the cursor's
position.

- [ ] **Step 1: Write the cursor logic**

`protocol/cursor.go`:
```go
package protocol

import (
	"fmt"
)

// Cursor identifies a position within a session's event log.
type Cursor struct {
	SessionID   string
	LastEventID *string // nil means "from the beginning"
}

// EventsAfter returns the events that come after the cursor's position in the log.
//
// Rules (spec section 3.2):
//
//	- If LastEventID is nil, return all events (brand-new client).
//	- If LastEventID matches an event in the log, return events after it in insertion order.
//	- If LastEventID does not match any event, return an empty slice (client state is too stale).
func EventsAfter(events []Event, cursor Cursor) ([]Event, error) {
	if cursor.SessionID == "" {
		return nil, fmt.Errorf("events_after: session_id is required")
	}

	// Nil cursor: return all events.
	if cursor.LastEventID == nil {
		return events, nil
	}

	// Find the index of the event matching last_event_id.
	idx := -1
	for i, e := range events {
		if e.ID == *cursor.LastEventID {
			idx = i
			break
		}
	}

	// Not found: client's state is too stale.
	if idx == -1 {
		return []Event{}, nil
	}

	// Return everything after the found index.
	return events[idx+1:], nil
}

// IsFullySynced returns true if the cursor points to the last event in the log.
// A nil cursor (fetch-all) is considered fully synced once the log is non-empty.
func IsFullySynced(events []Event, cursor Cursor) bool {
	if cursor.LastEventID == nil {
		return len(events) == 0
	}
	if len(events) == 0 {
		return true
	}
	return events[len(events)-1].ID == *cursor.LastEventID
}

// CurrentCursor returns a cursor pointing at the last event in the log.
// If the log is empty, LastEventID is nil.
func CurrentCursor(events []Event) Cursor {
	if len(events) == 0 {
		return Cursor{LastEventID: nil}
	}
	lastID := events[len(events)-1].ID
	return Cursor{LastEventID: &lastID}
}
```

- [ ] **Step 2: Write the tests**

`protocol/cursor_test.go`:
```go
package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func makeEvent(id, parentID string, et EventType) Event {
	ts := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	p := &parentID
	if parentID == "" {
		p = nil
	}
	return Event{
		ID:        id,
		ParentID:  p,
		Timestamp: ts,
		Type:      et,
		Data:      json.RawMessage(`{}`),
	}
}

func TestEventsAfterNilCursor(t *testing.T) {
	events := []Event{
		makeEvent("e1", "", EventSession),
		makeEvent("e2", "e1", EventMessage),
		makeEvent("e3", "e2", EventToolCall),
	}

	cursor := Cursor{SessionID: "s1", LastEventID: nil}
	got, err := EventsAfter(events, cursor)
	if err != nil {
		t.Fatalf("EventsAfter: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 events, got %d", len(got))
	}
	if got[0].ID != "e1" || got[2].ID != "e3" {
		t.Errorf("events = %v, want [e1 e2 e3]", eventIDs(got))
	}
}

func TestEventsAfterMidLog(t *testing.T) {
	events := []Event{
		makeEvent("e1", "", EventSession),
		makeEvent("e2", "e1", EventMessage),
		makeEvent("e3", "e2", EventToolCall),
		makeEvent("e4", "e3", EventToolResult),
		makeEvent("e5", "e4", EventMessage),
	}

	cursor := Cursor{SessionID: "s1", LastEventID: strPtr("e3")}
	got, err := EventsAfter(events, cursor)
	if err != nil {
		t.Fatalf("EventsAfter: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
	if got[0].ID != "e4" || got[1].ID != "e5" {
		t.Errorf("events = %v, want [e4 e5]", eventIDs(got))
	}
}

func TestEventsAfterNonexistentEvent(t *testing.T) {
	events := []Event{
		makeEvent("e1", "", EventSession),
		makeEvent("e2", "e1", EventMessage),
	}

	cursor := Cursor{SessionID: "s1", LastEventID: strPtr("e999")}
	got, err := EventsAfter(events, cursor)
	if err != nil {
		t.Fatalf("EventsAfter: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 events for nonexistent cursor, got %d: %v", len(got), eventIDs(got))
	}
}

func TestEventsAfterLastEvent(t *testing.T) {
	events := []Event{
		makeEvent("e1", "", EventSession),
		makeEvent("e2", "e1", EventMessage),
	}

	cursor := Cursor{SessionID: "s1", LastEventID: strPtr("e2")}
	got, err := EventsAfter(events, cursor)
	if err != nil {
		t.Fatalf("EventsAfter: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 events when cursor is at last event, got %d", len(got))
	}
}

func TestEventsAfterEmptyLog(t *testing.T) {
	events := []Event{}

	cursor := Cursor{SessionID: "s1", LastEventID: nil}
	got, err := EventsAfter(events, cursor)
	if err != nil {
		t.Fatalf("EventsAfter: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 events for empty log, got %d", len(got))
	}
}

func TestIsFullySynced(t *testing.T) {
	events := []Event{
		makeEvent("e1", "", EventSession),
		makeEvent("e2", "e1", EventMessage),
	}

	tests := []struct {
		name     string
		cursor   Cursor
		expected bool
	}{
		{
			name:     "nil cursor on empty log",
			cursor:   Cursor{SessionID: "s1", LastEventID: nil},
			expected: true,
		},
		{
			name:     "nil cursor on non-empty log",
			cursor:   Cursor{SessionID: "s1", LastEventID: nil},
			expected: false,
		},
		{
			name:     "cursor at last event",
			cursor:   Cursor{SessionID: "s1", LastEventID: strPtr("e2")},
			expected: true,
		},
		{
			name:     "cursor in the middle",
			cursor:   Cursor{SessionID: "s1", LastEventID: strPtr("e1")},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsFullySynced(events, tt.cursor)
			if got != tt.expected {
				t.Errorf("IsFullySynced(%v) = %v, want %v", tt.cursor, got, tt.expected)
			}
		})
	}
}

func TestCurrentCursor(t *testing.T) {
	events := []Event{
		makeEvent("e1", "", EventSession),
		makeEvent("e2", "e1", EventMessage),
	}

	cursor := CurrentCursor(events)
	if cursor.LastEventID == nil || *cursor.LastEventID != "e2" {
		t.Errorf("CurrentCursor = %v, want LastEventID=%q", cursor, "e2")
	}

	emptyCursor := CurrentCursor([]Event{})
	if emptyCursor.LastEventID != nil {
		t.Errorf("CurrentCursor(empty) LastEventID = %v, want nil", emptyCursor.LastEventID)
	}
}

func TestEventsAfterMissingSessionID(t *testing.T) {
	events := []Event{makeEvent("e1", "", EventSession)}
	cursor := Cursor{SessionID: "", LastEventID: nil}
	_, err := EventsAfter(events, cursor)
	if err == nil {
		t.Fatal("expected error for empty session_id, got nil")
	}
}

func strPtr(s string) *string { return &s }
func eventIDs(events []Event) []string {
	ids := make([]string, len(events))
	for i, e := range events {
		ids[i] = e.ID
	}
	return ids
}
```

- [ ] **Step 3: Run the tests**

Run:
```bash
go test ./protocol/ -v -run Cursor
```

Expected: 9 cursor tests pass.

- [ ] **Step 4: Run all tests to confirm nothing regressed**

Run:
```bash
go test ./protocol/ -v
```

Expected: all 17 tests (8 types + 9 cursor) pass.

- [ ] **Step 5: Commit**

```bash
git add protocol/cursor.go protocol/cursor_test.go
git commit -m "protocol: cursor semantics and events_after"
```
---

### Task 6: Conformance vectors — event ordering

**Files:** Create `protocol/vectors/event-ordering/01-valid-chronological.json`,
`protocol/vectors/event-ordering/02-out-of-order-timestamps.json`

These vectors test that events render in insertion order (parent_id chain order),
not timestamp order. The append-only property means the log order is authoritative.

- [ ] **Step 1: Write vector 01 — strictly chronological events**

`protocol/vectors/event-ordering/01-valid-chronological.json`:
```json
{
  "name": "valid chronological events",
  "description": "Events with strictly increasing timestamps render in insertion order.",
  "input": [
    {
      "id": "evt-001",
      "parent_id": null,
      "timestamp": "2026-09-09T12:00:00Z",
      "type": "session",
      "data": {"workspace": "/tmp", "model": "qwen", "compaction_enabled": true}
    },
    {
      "id": "evt-002",
      "parent_id": "evt-001",
      "timestamp": "2026-09-09T12:00:01Z",
      "type": "message",
      "data": {"role": "user", "content": "hello"}
    },
    {
      "id": "evt-003",
      "parent_id": "evt-002",
      "timestamp": "2026-09-09T12:00:02Z",
      "type": "message",
      "data": {"role": "assistant", "content": "hi back"}
    }
  ],
  "operations": [
    {
      "name": "fetch_all",
      "operation": "events_after",
      "params": {"session_id": "sess-001", "last_event_id": null},
      "expectation": {
        "event_count": 3,
        "event_ids": ["evt-001", "evt-002", "evt-003"],
        "synced": true
      }
    }
  ]
}
```

- [ ] **Step 2: Write vector 02 — out-of-order timestamps**

`protocol/vectors/event-ordering/02-out-of-order-timestamps.json`:
```json
{
  "name": "out-of-order timestamps",
  "description": "Events with non-monotonic timestamps still render in insertion order (append-only log). The timestamp records creation time but does not determine render order.",
  "input": [
    {
      "id": "evt-001",
      "parent_id": null,
      "timestamp": "2026-09-09T12:00:00Z",
      "type": "session",
      "data": {"workspace": "/tmp", "model": "qwen", "compaction_enabled": true}
    },
    {
      "id": "evt-002",
      "parent_id": "evt-001",
      "timestamp": "2026-09-09T11:59:59Z",
      "type": "message",
      "data": {"role": "user", "content": "hello"}
    },
    {
      "id": "evt-003",
      "parent_id": "evt-002",
      "timestamp": "2026-09-09T12:00:05Z",
      "type": "message",
      "data": {"role": "assistant", "content": "hi back"}
    }
  ],
  "operations": [
    {
      "name": "fetch_all",
      "operation": "events_after",
      "params": {"session_id": "sess-002", "last_event_id": null},
      "expectation": {
        "event_count": 3,
        "event_ids": ["evt-001", "evt-002", "evt-003"],
        "synced": true
      }
    }
  ]
}
```

- [ ] **Step 3: Verify the vector files parse as valid JSON**

Run:
```bash
python3 -c "import json; json.load(open('protocol/vectors/event-ordering/01-valid-chronological.json')); print('OK')"
python3 -c "import json; json.load(open('protocol/vectors/event-ordering/02-out-of-order-timestamps.json')); print('OK')"
```

Expected: both print `OK`.

- [ ] **Step 4: Commit**

```bash
git add protocol/vectors/event-ordering/
git commit -m "protocol: conformance vectors for event ordering"
```

---

### Task 7: Conformance vectors — cursor semantics

**Files:** Create four files under `protocol/vectors/cursor-semantics/`.

These vectors test cursor behavior: fetch-all, fetch-after-N, fetch-after-the-first-event,
and fetch-after-a-nonexistent-event.

- [ ] **Step 1: Write all four cursor vectors**

`protocol/vectors/cursor-semantics/01-fetch-all-new-session.json`:
```json
{
  "name": "fetch all events for new session",
  "description": "A cursor with last_event_id null returns all events in the session.",
  "input": [
    {
      "id": "evt-001",
      "parent_id": null,
      "timestamp": "2026-09-09T12:00:00Z",
      "type": "session",
      "data": {"workspace": "/tmp", "model": "qwen", "compaction_enabled": true}
    },
    {
      "id": "evt-002",
      "parent_id": "evt-001",
      "timestamp": "2026-09-09T12:00:01Z",
      "type": "message",
      "data": {"role": "user", "content": "hello"}
    },
    {
      "id": "evt-003",
      "parent_id": "evt-002",
      "timestamp": "2026-09-09T12:00:02Z",
      "type": "message",
      "data": {"role": "assistant", "content": "hi"}
    }
  ],
  "operations": [
    {
      "name": "fetch_all",
      "operation": "events_after",
      "params": {"session_id": "sess-001", "last_event_id": null},
      "expectation": {
        "event_count": 3,
        "event_ids": ["evt-001", "evt-002", "evt-003"],
        "synced": true
      }
    }
  ]
}
```

`protocol/vectors/cursor-semantics/02-fetch-after-last-event.json`:
```json
{
  "name": "fetch after last event",
  "description": "A cursor pointing at the last event returns an empty set (nothing new to fetch).",
  "input": [
    {
      "id": "evt-001",
      "parent_id": null,
      "timestamp": "2026-09-09T12:00:00Z",
      "type": "session",
      "data": {"workspace": "/tmp", "model": "qwen", "compaction_enabled": true}
    },
    {
      "id": "evt-002",
      "parent_id": "evt-001",
      "timestamp": "2026-09-09T12:00:01Z",
      "type": "message",
      "data": {"role": "user", "content": "hello"}
    }
  ],
  "operations": [
    {
      "name": "fetch_at_head",
      "operation": "events_after",
      "params": {"session_id": "sess-002", "last_event_id": "evt-002"},
      "expectation": {
        "event_count": 0,
        "event_ids": [],
        "synced": true
      }
    }
  ]
}
```

`protocol/vectors/cursor-semantics/03-fetch-after-oldest-event.json`:
```json
{
  "name": "fetch after oldest event",
  "description": "A cursor pointing at the first event returns all subsequent events.",
  "input": [
    {
      "id": "evt-001",
      "parent_id": null,
      "timestamp": "2026-09-09T12:00:00Z",
      "type": "session",
      "data": {"workspace": "/tmp", "model": "qwen", "compaction_enabled": true}
    },
    {
      "id": "evt-002",
      "parent_id": "evt-001",
      "timestamp": "2026-09-09T12:00:01Z",
      "type": "message",
      "data": {"role": "user", "content": "hello"}
    },
    {
      "id": "evt-003",
      "parent_id": "evt-002",
      "timestamp": "2026-09-09T12:00:02Z",
      "type": "message",
      "data": {"role": "assistant", "content": "hi"}
    },
    {
      "id": "evt-004",
      "parent_id": "evt-003",
      "timestamp": "2026-09-09T12:00:03Z",
      "type": "tool_call",
      "data": {"tool": "read", "arguments": {"path": "f.go"}}
    }
  ],
  "operations": [
    {
      "name": "fetch_after_first",
      "operation": "events_after",
      "params": {"session_id": "sess-003", "last_event_id": "evt-001"},
      "expectation": {
        "event_count": 3,
        "event_ids": ["evt-002", "evt-003", "evt-004"],
        "synced": true
      }
    }
  ]
}
```

`protocol/vectors/cursor-semantics/04-fetch-after-nonexistent-event.json`:
```json
{
  "name": "fetch after nonexistent event",
  "description": "A cursor with an event ID not in the log returns an empty set (state too stale to resume).",
  "input": [
    {
      "id": "evt-001",
      "parent_id": null,
      "timestamp": "2026-09-09T12:00:00Z",
      "type": "session",
      "data": {"workspace": "/tmp", "model": "qwen", "compaction_enabled": true}
    },
    {
      "id": "evt-002",
      "parent_id": "evt-001",
      "timestamp": "2026-09-09T12:00:01Z",
      "type": "message",
      "data": {"role": "user", "content": "hello"}
    }
  ],
  "operations": [
    {
      "name": "fetch_stale_cursor",
      "operation": "events_after",
      "params": {"session_id": "sess-004", "last_event_id": "evt-999"},
      "expectation": {
        "event_count": 0,
        "event_ids": [],
        "synced": false
      }
    }
  ]
}
```

- [ ] **Step 2: Verify all four files parse as valid JSON**

Run:
```bash
for f in protocol/vectors/cursor-semantics/*.json; do python3 -c "import json; json.load(open('$f')); print('OK: $f')"; done
```

Expected: four lines of `OK: ...`.

- [ ] **Step 3: Commit**

```bash
git add protocol/vectors/cursor-semantics/
git commit -m "protocol: conformance vectors for cursor semantics"
```

---

### Task 8: Conformance vectors — compaction and partial sync

**Files:** Create `protocol/vectors/compaction/01-compacted-session-rendering.json`,
`protocol/vectors/partial-sync/01-partially-synced-session.json`

These vectors test that compaction does not remove events from the log, and that
a partially-synced client sees only the events it has synced.

- [ ] **Step 1: Write the compaction vector**

`protocol/vectors/compaction/01-compacted-session-rendering.json`:
```json
{
  "name": "compacted session renders full log",
  "description": "A compaction event does not remove events from the log. All events from session to head are present, including those within the compaction range. Compaction changes the request to the model, never the record.",
  "input": [
    {
      "id": "evt-001",
      "parent_id": null,
      "timestamp": "2026-09-09T12:00:00Z",
      "type": "session",
      "data": {"workspace": "/tmp", "model": "qwen", "compaction_enabled": true}
    },
    {
      "id": "evt-002",
      "parent_id": "evt-001",
      "timestamp": "2026-09-09T12:00:01Z",
      "type": "message",
      "data": {"role": "user", "content": "Write a sort function"}
    },
    {
      "id": "evt-003",
      "parent_id": "evt-002",
      "timestamp": "2026-09-09T12:00:02Z",
      "type": "message",
      "data": {"role": "assistant", "content": "Here is a bubble sort implementation."}
    },
    {
      "id": "evt-004",
      "parent_id": "evt-003",
      "timestamp": "2026-09-09T12:00:03Z",
      "type": "tool_call",
      "data": {"tool": "write", "arguments": {"path": "sort.go"}}
    },
    {
      "id": "evt-005",
      "parent_id": "evt-004",
      "timestamp": "2026-09-09T12:00:04Z",
      "type": "compaction",
      "data": {"summary": "User asked for a sort function. Assistant provided a Go bubble sort implementation written to sort.go.", "range_start": "evt-002", "range_end": "evt-004"}
    },
    {
      "id": "evt-006",
      "parent_id": "evt-005",
      "timestamp": "2026-09-09T12:00:05Z",
      "type": "message",
      "data": {"role": "user", "content": "Add tests"}
    }
  ],
  "operations": [
    {
      "name": "fetch_all_compacted",
      "operation": "events_after",
      "params": {"session_id": "sess-comp", "last_event_id": null},
      "expectation": {
        "event_count": 6,
        "event_ids": ["evt-001", "evt-002", "evt-003", "evt-004", "evt-005", "evt-006"],
        "synced": true
      }
    },
    {
      "name": "fetch_after_compaction_event",
      "operation": "events_after",
      "params": {"session_id": "sess-comp", "last_event_id": "evt-005"},
      "expectation": {
        "event_count": 1,
        "event_ids": ["evt-006"],
        "synced": true
      }
    }
  ]
}
```

- [ ] **Step 2: Write the partial sync vector**

`protocol/vectors/partial-sync/01-partially-synced-session.json`:
```json
{
  "name": "partially synced session",
  "description": "A client that has synced to event 5 of 10 sees only events 1-5. The synced flag is false, indicating more events are available.",
  "input": [
    {
      "id": "evt-001",
      "parent_id": null,
      "timestamp": "2026-09-09T12:00:00Z",
      "type": "session",
      "data": {"workspace": "/tmp", "model": "qwen", "compaction_enabled": true}
    },
    {
      "id": "evt-002",
      "parent_id": "evt-001",
      "timestamp": "2026-09-09T12:00:01Z",
      "type": "message",
      "data": {"role": "user", "content": "hello"}
    },
    {
      "id": "evt-003",
      "parent_id": "evt-002",
      "timestamp": "2026-09-09T12:00:02Z",
      "type": "message",
      "data": {"role": "assistant", "content": "hi"}
    },
    {
      "id": "evt-004",
      "parent_id": "evt-003",
      "timestamp": "2026-09-09T12:00:03Z",
      "type": "tool_call",
      "data": {"tool": "read", "arguments": {"path": "f.go"}}
    },
    {
      "id": "evt-005",
      "parent_id": "evt-004",
      "timestamp": "2026-09-09T12:00:04Z",
      "type": "tool_result",
      "data": {"tool": "read", "content": "package main", "status": "ok"}
    },
    {
      "id": "evt-006",
      "parent_id": "evt-005",
      "timestamp": "2026-09-09T12:00:05Z",
      "type": "message",
      "data": {"role": "assistant", "content": "Here is the file."}
    },
    {
      "id": "evt-007",
      "parent_id": "evt-006",
      "timestamp": "2026-09-09T12:00:06Z",
      "type": "message",
      "data": {"role": "user", "content": "add tests"}
    },
    {
      "id": "evt-008",
      "parent_id": "evt-007",
      "timestamp": "2026-09-09T12:00:07Z",
      "type": "tool_call",
      "data": {"tool": "write", "arguments": {"path": "f_test.go"}}
    },
    {
      "id": "evt-009",
      "parent_id": "evt-008",
      "timestamp": "2026-09-09T12:00:08Z",
      "type": "tool_result",
      "data": {"tool": "write", "content": "ok", "status": "ok"}
    },
    {
      "id": "evt-010",
      "parent_id": "evt-009",
      "timestamp": "2026-09-09T12:00:09Z",
      "type": "report",
      "data": {"files_touched": ["f.go", "f_test.go"], "exit_status": "completed"}
    }
  ],
  "operations": [
    {
      "name": "fetch_synced_to_5",
      "operation": "events_after",
      "params": {"session_id": "sess-partial", "last_event_id": "evt-005"},
      "expectation": {
        "event_count": 5,
        "event_ids": ["evt-006", "evt-007", "evt-008", "evt-009", "evt-010"],
        "synced": false
      }
    }
  ]
}
```

- [ ] **Step 3: Verify both files parse as valid JSON**

Run:
```bash
python3 -c "import json; json.load(open('protocol/vectors/compaction/01-compacted-session-rendering.json')); print('OK')"
python3 -c "import json; json.load(open('protocol/vectors/partial-sync/01-partially-synced-session.json')); print('OK')"
```

Expected: both print `OK`.

- [ ] **Step 4: Commit**

```bash
git add protocol/vectors/compaction/ protocol/vectors/partial-sync/
git commit -m "protocol: conformance vectors for compaction and partial sync"
```
---

### Task 9: Vector runner

**Files:** Create `protocol/vectors.go`

A Go package function that loads vector files from the `vectors/` directory tree,
parses them, executes the operations (using `EventsAfter`), and compares results
against the expected outcomes. This is the language-neutral test harness — the same
vector files that a Kotlin or Rust implementation would run.

- [ ] **Step 1: Write the vector runner**

`protocol/vectors.go`:
```go
package protocol

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// VectorOperation describes a single operation to execute against a vector's input.
type VectorOperation struct {
	Name        string          `json:"name"`
	Operation   string          `json:"operation"`
	Params      json.RawMessage `json:"params"`
	Expectation json.RawMessage `json:"expectation"`
}

// Vector is a single conformance test case.
type Vector struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Input       []Event             `json:"input"`
	Operations  []VectorOperation   `json:"operations"`
}

// VectorExpectation holds the expected result of an operation.
type VectorExpectation struct {
	EventCount int      `json:"event_count"`
	EventIDs   []string `json:"event_ids"`
	Synced     bool     `json:"synced"`
}

// LoadVector reads and parses a single vector file.
func LoadVector(path string) (*Vector, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load vector %s: %w", path, err)
	}

	var v Vector
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("parse vector %s: %w", path, err)
	}

	return &v, nil
}

// ExecuteOperation runs a single operation against the vector's input events.
func (v *Vector) ExecuteOperation(op VectorOperation) (VectorExpectation, error) {
	var result VectorExpectation

	switch op.Operation {
	case "events_after":
		var params struct {
			SessionID   string  `json:"session_id"`
			LastEventID *string `json:"last_event_id"`
		}
		if err := json.Unmarshal(op.Params, &params); err != nil {
			return result, fmt.Errorf("operation %s: parse params: %w", op.Name, err)
		}

		cursor := Cursor{
			SessionID:   params.SessionID,
			LastEventID: params.LastEventID,
		}

		events, err := EventsAfter(v.Input, cursor)
		if err != nil {
			return result, fmt.Errorf("operation %s: %w", op.Name, err)
		}

		result.EventCount = len(events)
		result.EventIDs = make([]string, len(events))
		for i, e := range events {
			result.EventIDs[i] = e.ID
		}
		result.Synced = IsFullySynced(v.Input, cursor)

	default:
		return result, fmt.Errorf("operation %s: unknown operation %q", op.Name, op.Operation)
	}

	return result, nil
}

// Validate checks that the operation's result matches the expectation.
func (v *Vector) Validate(op VectorOperation, got VectorExpectation) error {
	var want VectorExpectation
	if err := json.Unmarshal(op.Expectation, &want); err != nil {
		return fmt.Errorf("operation %s: parse expectation: %w", op.Name, err)
	}

	if got.EventCount != want.EventCount {
		return fmt.Errorf("operation %s: event_count = %d, want %d", op.Name, got.EventCount, want.EventCount)
	}

	if len(got.EventIDs) != len(want.EventIDs) {
		return fmt.Errorf("operation %s: event_ids length = %d, want %d", op.Name, len(got.EventIDs), len(want.EventIDs))
	}

	for i := range got.EventIDs {
		if got.EventIDs[i] != want.EventIDs[i] {
			return fmt.Errorf("operation %s: event_ids[%d] = %q, want %q", op.Name, i, got.EventIDs[i], want.EventIDs[i])
		}
	}

	if got.Synced != want.Synced {
		return fmt.Errorf("operation %s: synced = %v, want %v", op.Name, got.Synced, want.Synced)
	}

	return nil
}

// RunVector executes all operations in a vector and returns errors for any failures.
func (v *Vector) RunVector() error {
	for _, op := range v.Operations {
		got, err := v.ExecuteOperation(op)
		if err != nil {
			return fmt.Errorf("vector %q, operation %q: %w", v.Name, op.Name, err)
		}

		if err := v.Validate(op, got); err != nil {
			return fmt.Errorf("vector %q, operation %q: %w", v.Name, op.Name, err)
		}
	}
	return nil
}

// DiscoverVectors returns the paths of all vector files in the vectors directory.
func DiscoverVectors() ([]string, error) {
	var paths []string
	err := filepath.Walk("vectors", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".json") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover vectors: %w", err)
	}
	return paths, nil
}
```

- [ ] **Step 2: Verify the file compiles**

Run:
```bash
go build ./protocol/
```

Expected: no output, exit code 0.

- [ ] **Step 3: Commit**

```bash
git add protocol/vectors.go
git commit -m "protocol: vector runner for conformance tests"
```

---

### Task 10: Vector tests — run every vector file

**Files:** Create `protocol/vectors_test.go`

The `TestVectors` function discovers all vector files, loads each one, runs it,
and reports failures. This is the end-to-end proof that the Go implementation
conforms to the protocol specification.

- [ ] **Step 1: Write the test**

`protocol/vectors_test.go`:
```go
package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestVectors(t *testing.T) {
	paths, err := DiscoverVectors()
	if err != nil {
		t.Fatalf("discover vectors: %v", err)
	}

	if len(paths) == 0 {
		t.Fatal("no vector files found")
	}

	t.Logf("found %d vector files", len(paths))

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			v, err := LoadVector(path)
			if err != nil {
				t.Fatalf("load: %v", err)
			}

			if err := v.RunVector(); err != nil {
				t.Fatalf("run: %v", err)
			}

			t.Logf("PASS: %s (%d operations)", v.Name, len(v.Operations))
		})
	}
}

func TestVectorInputEventsParse(t *testing.T) {
	paths, err := DiscoverVectors()
	if err != nil {
		t.Fatalf("discover vectors: %v", err)
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}

			// Parse the input events separately to catch parse errors early.
			var wrapper struct {
				Input []jsonEvent `json:"input"`
			}
			if err := json.Unmarshal(data, &wrapper); err != nil {
				t.Fatalf("parse JSON: %v", err)
			}

			for i, je := range wrapper.Input {
				if je.Type == "" {
					t.Errorf("input[%d]: type is empty", i)
				}
				if je.ID == "" {
					t.Errorf("input[%d]: id is empty", i)
				}
			}
		})
	}
}

// jsonEvent is a minimal event for validation (no custom unmarshal).
type jsonEvent struct {
	ID        string      `json:"id"`
	ParentID  interface{} `json:"parent_id"`
	Timestamp string      `json:"timestamp"`
	Type      string      `json:"type"`
	Data      interface{} `json:"data"`
}

func TestDiscoverVectors(t *testing.T) {
	paths, err := DiscoverVectors()
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	expectedDirs := []string{
		"vectors/event-ordering",
		"vectors/cursor-semantics",
		"vectors/compaction",
		"vectors/partial-sync",
	}

	foundDirs := make(map[string]bool)
	for _, p := range paths {
		dir := filepath.Dir(p)
		foundDirs[dir] = true
	}

	for _, d := range expectedDirs {
		if !foundDirs[d] {
			t.Errorf("expected vector dir %q not found", d)
		}
	}
}
```

- [ ] **Step 2: Run the tests**

Run:
```bash
go test ./protocol/ -v
```

Expected: all vector tests pass. The output should show each vector file being
loaded and validated:

```
found 9 vector files
=== RUN   TestVectors/vectors/event-ordering/01-valid-chronological.json
    vectors_test.go:28: PASS: valid chronological events (1 operations)
=== RUN   TestVectors/vectors/event-ordering/02-out-of-order-timestamps.json
    vectors_test.go:28: PASS: out-of-order timestamps (1 operations)
=== RUN   TestVectors/vectors/cursor-semantics/01-fetch-all-new-session.json
    vectors_test.go:28: PASS: fetch all events for new session (1 operations)
=== RUN   TestVectors/vectors/cursor-semantics/02-fetch-after-last-event.json
    vectors_test.go:28: PASS: fetch after last event (1 operations)
=== RUN   TestVectors/vectors/cursor-semantics/03-fetch-after-oldest-event.json
    vectors_test.go:28: PASS: fetch after oldest event (1 operations)
=== RUN   TestVectors/vectors/cursor-semantics/04-fetch-after-nonexistent-event.json
    vectors_test.go:28: PASS: fetch after nonexistent event (1 operations)
=== RUN   TestVectors/vectors/compaction/01-compacted-session-rendering.json
    vectors_test.go:28: PASS: compacted session renders full log (2 operations)
=== RUN   TestVectors/vectors/partial-sync/01-partially-synced-session.json
    vectors_test.go:28: PASS: partially synced session (1 operations)
--- PASS: TestVectors (0.00s)
=== RUN   TestVectorInputEventsParse
--- PASS: TestVectorInputEventsParse (0.00s)
=== RUN   TestDiscoverVectors
--- PASS: TestDiscoverVectors (0.00s)
```

- [ ] **Step 3: Commit**

```bash
git add protocol/vectors_test.go
git commit -m "protocol: conformance vector tests"
```

---

### Task 11: Final gate — all tests pass

**Files:** No new files. Run `go test ./protocol/ -v` from the repo root.

This is the final verification: every test in the protocol package passes, including
the event parsing tests, cursor tests, and conformance vector tests.

- [ ] **Step 1: Run the full test suite**

Run:
```bash
go test ./protocol/ -v
```

Expected: all tests pass (types: 8, cursor: 9, vectors: 2).

- [ ] **Step 2: Verify the file tree is complete**

Run:
```bash
find protocol/ -type f | sort
```

Expected output:
```
protocol/cursor.go
protocol/cursor_test.go
protocol/schema/event.json
protocol/schema/jsonrpc.json
protocol/spec.md
protocol/types.go
protocol/types_test.go
protocol/vectors.go
protocol/vectors_test.go
protocol/vectors/compaction/01-compacted-session-rendering.json
protocol/vectors/cursor-semantics/01-fetch-all-new-session.json
protocol/vectors/cursor-semantics/02-fetch-after-last-event.json
protocol/vectors/cursor-semantics/03-fetch-after-oldest-event.json
protocol/vectors/cursor-semantics/04-fetch-after-nonexistent-event.json
protocol/vectors/event-ordering/01-valid-chronological.json
protocol/vectors/event-ordering/02-out-of-order-timestamps.json
protocol/vectors/partial-sync/01-partially-synced-session.json
```

- [ ] **Step 3: Commit everything**

```bash
git add protocol/
git commit -m "protocol: complete — types, cursor, vectors, and runner"
```

---

## Done when

- [ ] `protocol/spec.md` exists and covers all 7 event types, cursor semantics, JSON-RPC
  method set, error codes, and the open budget question.
- [ ] `protocol/schema/event.json` and `protocol/schema/jsonrpc.json` are valid JSON
  Schema (draft 2020-12) definitions.
- [ ] 9 conformance vector files exist across 4 categories (event ordering, cursor
  semantics, compaction, partial sync), each is valid JSON with `name`, `description`,
  `input`, `operations`, and `expectation` fields.
- [ ] `protocol/types.go` defines all event types with custom JSON unmarshaling that
  validates event type and timestamp.
- [ ] `protocol/cursor.go` implements `EventsAfter`, `IsFullySynced`, and `CurrentCursor`.
- [ ] `protocol/vectors.go` implements the vector runner: `LoadVector`, `ExecuteOperation`,
  `Validate`, `RunVector`, and `DiscoverVectors`.
- [ ] `protocol/vectors_test.go` discovers and runs every vector file.
- [ ] `go test ./protocol/ -v` passes with 0 failures, run from the repo root.
- [ ] All commits use the `area: lowercase summary` format.

## Not in this phase

Deliberately still absent, and all previously agreed:

- The daemon, the agent loop, providers, tools, or the WebSocket server (P1).
- The Kotlin implementation and its vector runner (P4). P0 only guarantees the vectors
  are language-neutral so Kotlin can later run the same files.
- The TUI, the Android app, the Rust GUI, plugins, skills, FCM.
- Per-run budget semantics (deferred pending the open question in the architecture spec).
- Any networking code, any WebSocket handling, any IPC.
- The plugin protocol (shares JSON-RPC framing but has a different method set).
