# nabu — architecture map

Short orientation for anyone (human or agent) opening this repo. The full design with
every decision and rejected alternative is
`docs/specs/2026-09-11-nabu-architecture-design.md`; read it before
proposing changes. The wire contract is `protocol/spec.md`.

## In one paragraph

A long-lived Go daemon owns sessions, the agent loop, tools, and modules. Sessions are
append-only event logs; the daemon is the only writer; the model request is a pure
function of the log. Thin clients (Go TUI, headless CLI, Kotlin Android, later a Rust
GUI) speak one JSON-RPC-over-WebSocket protocol to it, reached over Tailscale. Policy
(gating, verification, memory, skills) lives in in-process modules behind Go
interfaces; the daemon core knows how to ask them and holds no opinions of its own.
The loop stops only when no module objects.

## Invariants

1. **The log is the state.** Every state transition appends an event. Crash recovery,
   replay, sync, and clients' views are all derived from it.
2. **Single writer.** Only `daemon/session` appends. Modules append through `Host`.
3. **Request = f(log).** Anything a module puts in front of the model is a `context`
   event first. Two daemons with the same modules and the same log build the same
   request byte for byte.
4. **Mechanism in core, policy in modules.** `agent` knows how to gate a tool call and
   how to ask whether it may stop; `modules/guard` and `modules/verify` decide. A test
   in `daemon/module` fails the build if a module imports daemon internals.
5. **No privileged tools.** Built-ins register through the same registry modules use.
6. **Stable prefix.** The prompt prefix changes only at compaction; dynamic state rides
   in tool results and in the Current state block after a summary.
7. **One process boundary.** Daemon ↔ client. Nothing inside the daemon crosses a
   process line.

## Layout

```
protocol/                 wire contract: spec.md, schema/, vectors/, Go types + vector runner
daemon/
  session/                event log, cursors, index, persistence
  agent/                  loop, assembly, stop gate, compaction, budget, restart
  provider/               OpenAI-compatible streaming client, limits, usage
  tools/                  built-ins incl. task.update
  module/                 Module + hook interfaces, Host, registry, dispatch, boundary test
  modules/                all.go (registration list) + skills/ guard/ verify/ report/ memory/ web/ ask/ watch/ vcs/ notes/ claude/
  api/                    WebSocket JSON-RPC surface, fan-out, deltas, client requests
  notify/                 FCM
  workspace/              workspace key, trust, permission plumbing
cmd/nabu/                 the single binary (daemon + CLI subcommands)
clients/                  go-tui/ android/ rust-gui/
docs/specs/               design specs: what was decided, and what was rejected
docs/plans/               implementation plans, one per piece of work
docs/proposals/           ideas not yet decided on
```

## Event types (16)

`session`, `message`, `thinking`, `tool_call`, `tool_result`, `options_change`,
`state_change`, `compaction`, `context`, `tasks`, `goal`, `check`, `stop_veto`,
`budget`, `notice`, `report`.
Schemas in `protocol/schema/event.json`; semantics in `protocol/spec.md`.

## Milestones

P0 protocol → P1 daemon + loop + modules + CLI (Claude Code can delegate with
`--done-when`) → P2 TUI (daily driver) → P3 memory → P4 Android → P5 FCM → P6 Rust GUI.

## Verifying

```
go build ./... && go vet ./... && go test ./...
```

That command is the project gate. The conformance vectors under `protocol/vectors/`
are the central correctness artifact and run as part of `go test ./protocol/`.

A live smoke test drives the real agent loop against a local OpenAI-compatible
server. It is skipped unless the base URL is set, so the gate stays hermetic:

```
NABU_LIVE_BASE_URL=http://localhost:8033/v1 NABU_LIVE_MODEL=qwen3.6-35b-a3b \
  go test ./daemon/agent/ -run Live -v -timeout 10m
```
