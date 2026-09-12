# nabu P0 — Protocol and Session Format

**Status:** Complete as of 2026-09-11 (revision 2). The revision-1 plan this file
replaced was written against subprocess plugins and seven event types; it is in git
history at 8843a6c. This document records what P0 delivers, where it lives, and how
it is verified, so the P1 plan can build on it without re-deriving anything.

**Goal:** Define and prove nabu's normative wire contract — the append-only session
event schema, session states and options, the JSON-RPC method set, error codes, the
state projection, request assembly, and the two normative text templates — backed by
a language-neutral conformance vector suite and a Go reference implementation.

**Architecture:** `docs/superpowers/specs/2026-09-11-nabu-architecture-design.md`
§5–§7 and §14. The protocol is the one process boundary in nabu (daemon ↔ clients);
the Go daemon, Go TUI, Kotlin Android client, and later Rust GUI all implement it.

## Delivered

| Artifact | Path | Notes |
|---|---|---|
| Normative spec | `protocol/spec.md` | 15 event types, ULIDs, cursors (`nabu_cursor_unknown` on unknown ids), projection, assembly order, Current state block, veto message, 19 methods incl. `hello` and ephemeral `delta`, 13 error codes, vector format |
| JSON Schema | `protocol/schema/event.json`, `protocol/schema/jsonrpc.json` | draft 2020-12; a Go test pins the type, method and error enums to the Go constants |
| Go package | `protocol/` | `types.go` (events + per-type data), `ulid.go`, `errors.go`, `validate.go`, `cursor.go`, `project.go`, `render.go`, `assemble.go`, `vectors.go` |
| Conformance vectors | `protocol/vectors/<family>/*.json` | 33 cases in 7 families: validation (11), event-ordering (2), cursor-semantics (5), projection (4), rendering (5), assembly (4), partial-sync (2) |
| Vector generator | scratch only, not committed | Vectors are plain data; regenerate by hand-editing or rewriting a generator. The JSON files are the source of truth. |
| Module contract | `daemon/module/` | Pulled forward from P1 because the request-assembly rules depend on it: `Module`, eleven hook interfaces, `Host`, `Registry` with recover/timeout isolation, import-boundary test |

## Verified by

```
go build ./... && go vet ./... && go test ./...
```

`go test ./protocol/` runs every vector (`TestVectors`, one subtest per file) and the
schema-agreement test. `go test ./daemon/module/` runs the registry isolation tests and
the boundary test.

## Decisions made while implementing (not in the spec before P0)

- **Unknown cursor is an error, not an empty set.** A client with a corrupt cursor
  must notice and reset rather than silently stop syncing.
- **`state_change` is an event.** Restart recovery needs to know from the log alone
  whether a session was mid-turn. Fifteen types, not fourteen.
- **Vectors are per-file, one operation each,** with `expect` shapes per operation and
  subset matching for `project` so vectors do not break when the projection gains
  fields.
- **`Goal: none` also when the last goal state is `cleared`.**
- **`tool_call.source` is `model` or `module:<name>` only.** Clients never originate
  tool calls; client edits arrive as `tasks`/`options_change` events with
  `source: client`.
- **Module hook return types:** `SessionStart` and `AfterCompaction` may return prefix
  blocks; `BeforeRequest` may return suffix blocks only and the registry drops
  anything else with a notice.

## Handed to P1

- `daemon/session`: JSONL persistence of `protocol.Event`, index, `events_after`.
- `daemon/agent`: the loop, using `protocol.Assemble` for the body and
  `module.Registry` for gates; both compaction stages; budget; graceful restart.
- `daemon/api`: the method set in `protocol.Methods`, `hello`, subscriptions,
  `delta` notifications, first-responder-wins requests.
- `daemon/tools`: built-ins as a `module.ToolProvider`; `task.update` snapshot logic
  with mechanical `evidence`.
- `daemon/modules/{skills,guard,verify,report}` and one line each in
  `daemon/modules/all.go`.
- `cmd/nabu`: `daemon`, `run --done-when --max-turns`, `status`, `attach`, `stop`,
  `resume`; auto-start of a detached daemon.

## Not in P0

The Kotlin vector runner (P4), any networking, any model call, any file I/O beyond
reading vectors.
