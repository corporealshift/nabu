# Nabu — Architecture Design

**Date:** 2026-09-09
**Author:** Kyle (corporealshift), with Claude
**Status:** Approved — architecture baseline

## 1. Purpose

Nabu is a custom coding-agent harness that replaces `pi` (the `pi-coding-agent` CLI) as
the owner's daily driver. It is named after the Mesopotamian god of scribes and
record-keeping, because its central abstraction is an **append-only session log**.

Nabu consists of a long-lived local daemon plus several clients: a terminal UI, a
headless CLI, an Android app, and eventually a desktop GUI. Everything speaks one
protocol to the daemon.

### Motivations

- **Ownership and control.** The whole stack — daemon, protocol, clients, sync — is
  built from scratch and answers to nobody else's roadmap.
- **Capability ceiling.** `pi`'s architecture cannot be pushed from the outside into the
  capabilities needed: session sync, a first-class mobile client, and plugin depth.
- **Performance and footprint.** A native binary rather than a Node/TypeScript process.
- **Intrinsic value.** Building a serious agent harness is worth doing.

Nabu is primarily a personal tool. It may be released publicly later, but the design
should not be gold-plated for hypothetical external users.

### Non-goals (v1)

- Native Anthropic API support.
- Using the Claude Code CLI as a model backend.
- Compatibility with, or porting of, the existing ten `pi` extensions.
- A hosted relay or cloud sync service.
- Running the daemon on the VPS.
- The Android app originating its own sessions with its own on-device agent loop.
- MCP support in v1.
- Sophisticated offline write policies (auto-flush rules, HEAD-change detection,
  draft-confirmation flows).

## 2. Topology

One long-lived daemon per machine owns sessions, the agent loop, tool execution,
plugins, and history. Everything else is a thin client speaking one protocol: the Go
TUI, the headless CLI, the Android app, and later a Rust GUI.

The governing principle: **a session must outlive whichever client is looking at it.**
Closing the TUI mid-run does not stop the run. Reattaching replays from the client's
cursor.

### Rejected alternatives

**Monolith, one process per session** (what `pi` is): the TUI process *is* the agent;
headless is the same binary with no UI; sync is bolted on by a relay scraping session
files. Rejected because every delegation failure mode survives the rewrite — closing
the TUI kills or orphans the run — and the phone can only ever be a viewer.

**Daemon hosted on the owner's VPS:** rejected on facts. The repos live on the Windows
desktop, the local Qwen model server is on the Windows desktop, and the droplet is
1 vCPU / 957 MB and cannot compile Rust.

## 3. Languages

| Component | Language | Rationale |
|---|---|---|
| Daemon | Go | Goroutines and channels model supervising subprocess plugins, streaming SSE from providers, and many concurrent sessions with much less ceremony than async Rust. Compile times matter over a long project. |
| TUI | Go (Bubble Tea) | Same language as the daemon; thin client with no shared state. |
| Android | Kotlin / Jetpack Compose / Room | Native mobile. |
| Desktop GUI | Rust | A third client against the same protocol — the strongest possible proof that the protocol is a real contract and not merely whatever the Go TUI happens to need. |

The polyglot client set is deliberate. A Rust GUI written against the same protocol
proves the protocol is a real contract.

### Known consequence: sync logic is implemented twice

Rust would have allowed shared sync/merge logic through UniFFI; Go's gomobile is
effectively abandoned. The mitigation is not shared code but a **normative session-format
specification plus a shared conformance vector suite** — a directory of input logs and
expected results that both the Go and Kotlin implementations run in CI.

## 4. Model providers

**OpenAI-compatible chat completions only**, covering both local servers (llama.cpp /
Ollama / vLLM — the owner runs Qwen3.6-35B-A3B at `http://localhost:8033/v1`) and
hosted ones (OpenRouter, Groq, DeepSeek, Together). One wire format.

Native Anthropic API support and using the Claude Code CLI as a backend were both
explicitly considered and rejected for v1. A provider interface sits behind the
OpenAI-compatible implementation so a native Anthropic client can be added later
without restructuring.

### Concurrency limits are required, not optional

The local Qwen server is a single llama.cpp process; several concurrent sessions hitting
it will thrash. Config carries a `max-in-flight` value per provider (e.g. 1 for the
local model, higher for hosted). Sessions queue rather than degrade.

## 5. Sessions and the event log

Sessions are **append-only event logs**. Each record carries at minimum `{id, parentId,
timestamp, type}` and chains to its predecessor. Event types include (non-exhaustive):
`session`, `message`, `tool_call`, `tool_result`, `model_change`, `compaction`, and
`report`.

### The daemon is the sole writer

No client ever appends to the canonical log. This single constraint eliminates the
entire merge-conflict problem: there are no concurrent writers, so there is nothing to
reconcile.

### The log is the state

Every state transition in the agent loop appends an event. Crash recovery, replay, and
sync all fall out of that property rather than requiring separate machinery.

### Sync via cursors

Each client keeps a per-session cursor, asks the daemon for events after N, and appends
what it receives. Offline reading is a query against the local mirror.

### Offline writes via outbox

Offline writes never enter the canonical log directly. A prompt composed offline goes
into a client-local **outbox**. It becomes a session event only when the daemon accepts
it. Pending outbox items stay pending and trigger a notification when connectivity
returns; the user then sends them. Deliberately simple — no automatic-flush policy,
HEAD-comparison check, or draft-confirmation flow.

### Context management and compaction

Auto-compaction is on by default, with a per-session opt-out settable at session
creation and toggleable mid-session by any attached client. With compaction disabled,
a session that reaches the context limit hard-stops with a clear, explicit reason.

**Compaction is an event, not a deletion.** A `compaction` record stores the generated
summary and the range of events it covers. What gets sent to the model shrinks; the log
retains everything. Full pre-compaction history remains readable on every client, and
plugins can inspect what was dropped. Compaction changes the *request*, never the
*record*.

## 6. Transport and protocol

**JSON-RPC 2.0 over WebSocket**, bound to loopback plus the Tailscale interface.

A single transport serves every client. A separate local IPC path (named pipes on
Windows, Unix domain sockets elsewhere) was considered and rejected: it would mean two
transports to write, test, and keep in sync, and the Android client requires the network
transport regardless.

Connections from non-loopback addresses must present a bearer token from config.
Tailscale supplies encryption and device identity underneath.

### Bidirectional, load-bearing

The daemon does not merely stream events downward; it issues *requests* to clients and
waits for answers — `permission.request` before a gated tool call, `ui.ask` when a
plugin needs user input. The plugin protocol uses the same JSON-RPC framing with a
different method set, so the framing code is written once and used twice.

### Multi-client is the normal case

The TUI and the phone may both be attached to one live session. Events broadcast to all
subscribers. Requests needing an answer broadcast to all attached clients and resolve
**first responder wins**; late answers receive an "already resolved" response and the
client dismisses the prompt.

A direct consequence: a destructive command can be approved from a phone in a pocket.
Clients are expected to render risky permission prompts with friction proportionate to
the risk, and the Android client should apply more friction than the TUI.

## 7. Daemon internals

Go packages, each independently testable, each with one clear purpose:

| Package | Responsibility |
|---|---|
| `session` | Event log, cursors, index, persistence |
| `agent` | The agent loop |
| `provider` | OpenAI-compatible streaming client, retries with backoff, concurrency limits |
| `tools` | Built-in tools: bash, read, write, edit, glob, grep |
| `plugin` | Subprocess supervisor and plugin JSON-RPC |
| `api` | WebSocket surface, subscriptions, event fan-out |
| `notify` | FCM dispatch |
| `workspace` | Working directory, trust, permission plumbing |

### The agent loop

Assemble request → stream from provider → parse tool calls → gate → execute → append
results → repeat until there are no tool calls or a stop condition is reached. Every
transition appends to the log.

## 8. Plugins — microkernel

**The application is mostly plumbing; the real logic lives in plugins.**

Plugins are long-lived subprocesses speaking bidirectional JSON-RPC over stdio. A plugin
is any executable in any language. It is spawned once at daemon start and stays alive.
Bidirectionality lets a plugin both receive hooks and call back into the attached client
for user input.

### Rejected alternatives

**Embedded script runtime** (Lua or JS) — fastest DX and no IPC, but one blessed
language, weak sandboxing, and a bad plugin can wedge the daemon.

**WASM components** — sandboxed and portable, but host callbacks and async remain
awkward and the toolchain immaturity would consume effort better spent on the harness
itself.

### Hooks

`before_session_start`, `before_request`, `tool_call` (vetoable), `tool_result`,
`turn_end`, `session_end`. Plugins **declare their subscriptions at handshake**, so
the daemon only pays IPC cost for hooks something actually consumes. With many
concurrent sessions and a plugin-heavy design this is a real performance consideration,
not a micro-optimization.

### Mechanism in core, policy in plugins

The daemon knows *how* to gate a tool call — pause it, ask attached clients, apply the
verdict, handle timeout. It ships with no opinion about *what* should be gated.
bash-guard-style rules, plan mode, and lint gates are all plugins.

### No privileged path for built-in tools

bash/read/write/edit/glob/grep are built in because they are universal and hot, but they
register through the *same* tool-registry interface plugins use. If a plugin cannot
replace `bash`, the abstraction is fake.

### First-run safety

With zero plugins installed the agent is completely unguarded. This is coherent with
the architecture but must be a **loud first-run fact**, and a small default plugin
bundle ships so the tool is not sharp out of the box.

### Noted for later

If the plugin protocol is designed as a superset of MCP, MCP servers would work as
plugins essentially for free. Not in scope for v1.

## 9. Skills — a core feature

Skills are a **core daemon feature**, not a plugin: native discovery and loading from
configured paths, including `~/.claude/skills` for compatibility with existing Claude
Code skills.

This is a **deliberate departure from the microkernel stance** and the spec says so
plainly rather than rationalizing it. The reasoning: skills are how the owner actually
works, they sit on the hot path of every session, and a subtly broken skills plugin
silently degrading every session is a worse failure than a small, contained impurity in
core.

## 10. Headless mode and Claude interop

"Claude interop" means a specific thing here: **Claude Code must be able to delegate
work to nabu**, the way it delegates to `pi` today. It does *not* mean using Claude as a
model backend.

The design targets the documented failure modes of delegating to `pi`. Each row is a
real, recurring defect and the design answer to it:

| pi failure mode | nabu's answer |
|---|---|
| Determining whether `pi` is still running requires filtering `node.exe` processes by command line | `nabu status` queries the daemon. Sessions have IDs and states. No process archaeology. |
| Killing the launcher leaves a detached agent that races the caller's edits and clobbers committed work | The run lives in the daemon and is addressable. Killing the CLI merely detaches. `nabu attach <id>` resumes watching; `nabu stop <id>` genuinely stops it. |
| Piped output is buffered until exit, so an empty log conveys nothing | `--json` emits flushed JSONL per event. Silence means dead, not buffered. |
| Exit code 1 does not reliably indicate failure | Distinct exit codes: completed, blocked-needs-input, budget-exhausted, gate-refused, error. |
| A finite per-run budget forces blind repeated resumes | Budget is reported as session state; exhaustion has its own exit code and yields a resume token. |
| The agent reports "tests pass" while its fixes sit uncommitted | The run report (below). |

### The run report

At session end the daemon emits a `report` event whose purpose is to let the caller
verify outcomes *without* trusting the model's narration.

**The core defines the report's slot and schema; plugins supply every field.** Core
holds no opinion and computes nothing. This preserves the microkernel stance while
keeping the contract Claude depends on stable in shape: a caller can always determine
whether a report exists and parse it.

A `run-report` plugin ships in the default bundle and populates it with observed facts:
files touched, commits created, whether the working tree is dirty, and the exit status
of a configured verification command. Same pattern as the tool registry — core owns the
mechanism, ships a sensible default, privileges nothing.

Two alternatives were rejected: core natively knowing each workspace's verification
command and running it (too much opinion in core), and having no report schema at all
(the delegation contract would then vary by which plugins happened to be installed).

## 11. Clients

### TUI (Go, Bubble Tea)

Attaches to the daemon, subscribes to a session, renders the event stream. Because it
is a thin client it has properties `pi`'s TUI cannot: close it mid-run and the agent
continues; reattach and the log replays from the cursor; run two TUIs against one
session. Views: session list across workspaces, transcript, and a permission/ask prompt
overlay. It holds no session state of its own — it is purely a projection of the log.

### Android (Kotlin / Compose / Room)

Same protocol over Tailscale. Room mirrors the event log for offline reading; a separate
outbox table holds pending prompts. Screens: session list, transcript, composer,
permission prompts.

Two Android-specific requirements to be designed rather than discovered:

1. Permission prompts need more friction than the TUI equivalent.
2. A partially-synced session must render **honestly** — visibly truncated, never
   silently short.

### Desktop GUI (Rust)

A later milestone. A third client against the same protocol, requiring no daemon
changes.

## 12. Notifications

**Firebase Cloud Messaging.** Chosen over a tailnet-only foreground service (which
cannot fire when the app is force-stopped) and over poll-on-open (which never tells you
a run finished). Accepted cost: a Google Cloud project, a Firebase dependency in the
Android app, and outbound daemon access to FCM — a thin cloud edge in an otherwise
tailnet-only design.

**Hard constraint: FCM payloads carry identifiers and status only — never transcript
content.** For example "run 4f2a finished, 3 files changed" is acceptable; message text
is not. Session data stays on the tailnet; Google learns only that something happened.

Notifications cover both "an agent run finished" and "you have pending outbox items now
that connectivity is back."

## 13. Remote access and sync transport

**Direct over Tailscale.** The Android app connects straight to the daemon's API on the
owner's desktop. No relay, no accounts, no hosted sync service, no separate sync
protocol.

A relay on the owner's VPS was rejected: it is an entire additional service to build,
secure, deploy, and keep running; session content would transit a box requiring
hardening; and it does not solve the desktop-asleep case anyway. Tailscale already
provides encrypted transport and device identity — building a relay would amount to
reimplementing WireGuard with worse authentication.

Accepted limitations: the desktop must be awake to run or sync anything, and Tailscale
becomes a hard dependency of the product.

Offline *reading* of history is unaffected, because the Android client holds a full
local mirror.

## 14. Testing and verification

- Each daemon package is independently testable by design.
- **The conformance vector suite is the central correctness artifact:** a directory of
  input event logs with expected results, run by both the Go and Kotlin implementations
  in CI. It pins event schema, ordering, and how a partially-synced session renders. It
  is what makes the polyglot split safe. Budget roughly a day for it; it belongs to
  milestone P0.
- The append-only log makes the agent loop testable by replay: given a log, the derived
  state is deterministic.

## 15. Milestone decomposition

Each milestone gets its own brainstorm, spec, and implementation plan, and each ends
somewhere genuinely useful rather than half-built. This architecture spec plus P0 are
the output of the current design session.

### P0 — Protocol and session format

The normative specification: event schema, JSON-RPC methods, error codes, plus the
conformance vector suite that Go and Kotlin both run. Small and unglamorous; it is what
makes the polyglot split safe.

### P1 — Daemon, agent loop, headless CLI

Providers, built-in tools, session log, skills, WebSocket API, and the `run` / `status`
/ `attach` / `stop` commands. **Ends with Claude Code able to delegate to nabu instead
of pi** — real value at the first milestone.

### P2 — Plugin system and default bundle

Subprocess supervisor, hook dispatch, and the plugins that make the system safe and
self-reporting (`bash-guard`, `run-report`).

### P3 — TUI

The point at which nabu replaces pi as the daily driver.

### P4 — Android client

Room mirror, outbox, offline rendering. The largest single piece after P1.

### P5 — FCM notifications

### P6 — Rust desktop GUI

**P2 deliberately precedes P3:** gating lives in plugins, so until P2 exists the agent
is unguarded, and the owner would rather not daily-drive it in that state.

## 16. Repository layout (target)

```
nabu/
  daemon/                  Go daemon
    session/               event log, cursors, index, persistence
    agent/                 the agent loop
    provider/              OpenAI-compatible streaming client
    tools/                 built-in tools: bash, read, write, edit, glob, grep
    plugin/                subprocess supervisor and plugin JSON-RPC
    api/                   WebSocket surface, subscriptions, event fan-out
    notify/                FCM dispatch
    workspace/             working directory, trust, permission plumbing
  clients/
    go-tui/                Bubble Tea TUI
    android/               Kotlin / Compose / Room
    rust-gui/              later: Rust desktop GUI
  protocol/                normative spec, JSON-RPC methods, conformance vector suite
  docs/superpowers/specs/  this document
```

## 17. Decisions and rejected alternatives

| # | Decision | Chosen | Rejected and why |
|---|---|---|---|
| 1 | Topology | Daemon-owns-sessions | Monolith-per-session (delegation failure modes survive); VPS-hosted daemon (repos and model server on desktop, VPS cannot compile Rust) |
| 2 | Languages | Go for daemon and TUI, Rust for later GUI, Kotlin for Android | Rust for the daemon, which would have allowed sync logic to be shared with Android via UniFFI (Go's gomobile is effectively abandoned). Duplicate implementation accepted and mitigated with conformance vectors |
| 3 | Model providers | OpenAI-compatible only, with provider interface for later expansion | Native Anthropic API + Claude Code CLI as backend |
| 4 | Session storage | Append-only event log, daemon as sole writer; client outbox for offline prompts | Concurrent writers with merge logic (eliminated by single-writer constraint) |
| 5 | Context management | Auto-compaction by default with per-session opt-out; compaction as an event, not deletion | Compaction as log deletion (history must remain readable; plugins must inspect dropped events) |
| 6 | Transport | Single JSON-RPC-over-WebSocket for all clients | Separate local IPC + WebSocket (two transports to write, test, keep in sync) |
| 7 | Request resolution | Bidirectional protocol, first-responder-wins | Daemon-only streaming (plugins and permission gates need to ask clients) |
| 8 | Plugin model | Long-lived subprocesses with bidirectional JSON-RPC over stdio | Embedded script runtime (weak sandboxing, wedgable daemon); WASM (toolchain immaturity) |
| 9 | Core vs plugin | Microkernel: mechanism in core, policy in plugins; no privileged path for built-in tools | Core shipping gating logic (too much opinion in core) |
| 10 | Skills | Core feature, not plugin | Plugin (subtly broken plugin silently degrading every session is worse than a contained impurity) |
| 11 | Run report | Core-defined schema, plugin-supplied content | Core computing verification results (too much opinion); no schema at all (delegation contract would vary by plugin set) |
| 12 | Notifications | FCM with metadata-only payload constraint | Tailnet-only foreground service (cannot fire when app is force-stopped); poll-on-open (never tells you a run finished) |
| 13 | Remote transport | Direct over Tailscale | VPS relay (entire additional service; session content transit-hardened box; reimplementing WireGuard with worse auth) |
| 14 | Milestone ordering | P2 (plugins) before P3 (TUI) | P3 before P2 (unguarded agent would be daily-driven) |

## Open questions for Claude

**Per-run budget is referenced but never defined.** Section 10 specifies a
`budget-exhausted` exit code and states that "budget is reported as session state," but
nabu's budget concept is nowhere defined. This was inherited from `pi`, whose budget is a
per-invocation limit that forces repeated blind resumes — precisely one of the failure
modes nabu exists to fix. A daemon-owned session may not need a per-run budget at all.

Resolve before P1: does nabu have a budget; is it counted in tokens, steps, wall-clock
time, or cost; is it per-run or per-session; and does exhaustion pause a resumable
session or terminate it? If the answer is that no budget exists, the `budget-exhausted`
exit code and the resume-token mechanism in section 10 should be struck.

All other design decisions from the approval session are recorded above with their
rejected alternatives.
