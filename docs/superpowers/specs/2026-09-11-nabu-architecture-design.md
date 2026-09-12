# Nabu — Architecture Design (revision 2)

**Date:** 2026-09-11
**Author:** Kyle (corporealshift), with Claude
**Status:** Approved 2026-09-11 — replaces the 2026-09-09 baseline (deleted; see git history at aebd85a)
**Supersedes also:** the uncommitted 2026-09-11 memory/tasks/verification addendum,
which is folded in here.

## Revision notes

Revision 1 was approved on 2026-09-09. This revision keeps its topology, log,
protocol, clients, and transport decisions, and changes two things:

1. **Extension mechanism: in-process Go modules replace subprocess plugins.** The
   owner is the only developer. The microkernel *stance* (mechanism in core, policy in
   swappable units, no privileged path for built-ins, the log as the only state) is
   kept in full. The *subprocess boundary* is dropped: it existed for third-party and
   polyglot extension authors who will not exist, and it cost a process supervisor on
   Windows, a second RPC surface, IPC on every hook, and a plugin build/install story.
   There is no "default bundle"; the modules in the tree are the product.
2. **Three features added:** a task ledger, a verification stop gate with a
   fresh-context judge, and a memory module. The open budget question is resolved as
   the loop bound.

Every section below was re-derived against the baked-in design. Where a revision-1
decision depended on the plugin boundary, it was re-decided and is marked
**(re-decided)**. A final pass looked for things that must be in core from the start
and found five, marked **(core gap)**: an `options_change` event, token usage on
assistant messages, a protocol handshake, ephemeral streaming deltas, and daemon
lifecycle/config/storage. Section 22 records the owner's answers of 2026-09-11.

## 1. Purpose

Nabu is a custom coding-agent harness that replaces `pi` (the `pi-coding-agent` CLI)
as the owner's daily driver. It is named after the Mesopotamian god of scribes and
record-keeping, because its central abstraction is an **append-only session log**.

Nabu consists of a long-lived local daemon plus several clients: a terminal UI, a
headless CLI, an Android app, and eventually a desktop GUI. Everything speaks one
protocol to the daemon.

### Motivations

- **Ownership and control.** The whole stack — daemon, protocol, clients, sync — is
  built from scratch and answers to nobody else's roadmap.
- **Capability ceiling.** `pi`'s architecture cannot be pushed from the outside into
  the capabilities needed: session sync, a first-class mobile client, verification
  that does not trust the model's narration, and memory across sessions.
- **Performance and footprint.** A native binary rather than a Node/TypeScript process.
- **Intrinsic value.** Building a serious agent harness is worth doing.

Nabu is a personal tool built by one person. It may be released publicly later, but
the design must not be gold-plated for hypothetical external users or extension
authors. Extending nabu means editing nabu.

### Non-goals (v1)

- Native Anthropic API support.
- Using the Claude Code CLI as a model backend.
- Compatibility with, or porting of, the existing ten `pi` extensions.
- A hosted relay or cloud sync service.
- Running the daemon on the VPS.
- The Android app originating its own sessions with its own on-device agent loop.
- MCP support.
- Any out-of-process extension mechanism (subprocess plugins, embedded scripting, WASM).
- Sophisticated offline write policies (auto-flush rules, HEAD-change detection,
  draft-confirmation flows).
- Embedding-based memory retrieval, or memory shared across machines.

## 2. Topology

One long-lived daemon per machine owns sessions, the agent loop, tool execution,
modules, and history. Everything else is a thin client speaking one protocol: the Go
TUI, the headless CLI, the Android app, and later a Rust GUI.

**The daemon/client protocol is the only process boundary in nabu.** Inside the
daemon there is one binary. This boundary is kept because it is what makes
high-quality Android, TUI, and GUI clients possible, and because a session must
outlive whichever client is looking at it: closing the TUI mid-run does not stop the
run; reattaching replays from the client's cursor.

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
| Daemon and all modules | Go | Goroutines and channels model streaming SSE from providers and many concurrent sessions with little ceremony. One binary, one debugger, one test run. Compile times matter over a long project. |
| TUI | Go (Bubble Tea) | Same language as the daemon; thin client with no shared state. |
| Android | Kotlin / Jetpack Compose / Room | Native mobile. |
| Desktop GUI | Rust | A third client against the same protocol — the strongest possible proof that the protocol is a real contract and not merely whatever the Go TUI happens to need. |

The polyglot *client* set is deliberate. The daemon is monoglot, also deliberately:
there is no language boundary inside it to maintain.

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

### Every model call goes through `provider`

This includes calls made by modules — the verification judge and the memory curator
(§10, §11). Modules reach the model only through the host API, so the concurrency
limit and usage accounting hold for every call the daemon makes, not just the agent
loop's.

## 5. Sessions and the event log

Sessions are **append-only event logs**. Each record carries `{id, parent_id,
timestamp, type, data}` and chains to its predecessor.

### Event types

| Type | Purpose | Appended by |
|---|---|---|
| `session` | Session created: `{workspace, workspace_key, options: {model, compaction_enabled, permission_mode}}` | core |
| `message` | A `user` or `assistant` message; assistant messages carry `usage: {input_tokens, output_tokens, cached_tokens?}` and `interrupted: true` when cut short | core |
| `tool_call` | A tool invocation; `source` is `model` or `module:<name>` | core |
| `tool_result` | The result, `ok` or `error` | core |
| `options_change` | A session option changed mid-session: `{key, from, to, source}` for `model`, `compaction_enabled`, `permission_mode`. **(core gap)** Revision 1 had only `model_change` and no event for the compaction toggle it promised. | core |
| `compaction` | Context reduced; `mode: clear_results \| summarize`; the log keeps everything | core |
| `context` | A block injected into the model request outside of messages: `{source, slot: prefix \| suffix, content}` | core, on behalf of modules |
| `tasks` | Full snapshot of the task list after a change (§9) | core |
| `goal` | Run-level completion condition and its state (§10) | core, and the `verify` module for verdicts |
| `check` | A verification check ran: `{name, kind, task_id?, status, summary, output?}` | modules |
| `stop_veto` | A module objected to the loop stopping: `{module, reason}` | core |
| `budget` | Loop bound set or raised (§12) | core |
| `notice` | Something a human should see: module disabled after a panic, first-run warnings, restart interruption: `{source, level, message}` | core and modules |
| `report` | Run report at stop or pause (§13) | core, with module contributions |

### The daemon is the sole writer

No client ever appends to the canonical log. Modules run inside the daemon and append
through the host API, which stamps `id`, `parent_id`, `timestamp`, and `source`.
There are no concurrent writers, so there is nothing to reconcile.

### The log is the state

Every state transition in the agent loop appends an event. Crash recovery, replay, and
sync all fall out of that property. The corollary that revision 1 left implicit is now
explicit: **the model request is a pure function of the log.** Anything a module puts
in front of the model is a `context` event first. Two daemons with the same modules
and the same log assemble byte-identical requests.

### Sync via cursors

Each client keeps a per-session cursor, asks the daemon for events after N, and appends
what it receives. Offline reading is a query against the local mirror.

### Offline writes via outbox

Offline writes never enter the canonical log directly. A prompt composed offline goes
into a client-local **outbox**. It becomes a session event only when the daemon accepts
it. Pending outbox items stay pending and trigger a notification when connectivity
returns; the user then sends them. Deliberately simple.

### Session states

`idle`, `running`, `blocked` (needs user input, or verification vetoes are
outstanding and the loop stopped making progress), `paused` (budget exhausted or
daemon restarted mid-turn; resumable), `completed`, `error`.

## 6. Context management

Auto-compaction is on by default, with a per-session opt-out settable at session
creation and toggleable mid-session by any attached client. With compaction disabled,
a session that reaches the context limit hard-stops with a clear, explicit reason.

**Compaction is an event, not a deletion.** A `compaction` record stores what was
done and the range of events it covers. What gets sent to the model shrinks; the log
retains everything. Full pre-compaction history remains readable on every client, and
modules can inspect what was dropped. Compaction changes the *request*, never the
*record*.

Two stages, both events:

- **Stage 0, `clear_results`:** when usage crosses a lower threshold, tool results
  older than the last K turns are replaced in the request by a one-line stub. This is
  the cheapest and safest reduction and runs first.
- **Stage 1, `summarize`:** when usage crosses the upper threshold, older history is
  summarized by a model call. Modules subscribed to `before_compaction` may return
  strings the summary prompt must preserve. The summary is followed in the request by
  a deterministic **Current state** block (goal, task list, budget) rendered from the
  log, so task and goal state never depend on the summary's fidelity.

### Request assembly order (normative)

Static system prompt → `context` blocks with `slot: prefix`, in log order → compaction
summary and Current state block, if any → messages and tool events after the last
compaction → `context` blocks with `slot: suffix` for this request → outstanding
`stop_veto`s rendered with the fixed template (§10.3).

The prefix is fixed at session start and changes only at compaction. Dynamic state
(tasks, recalled memories) never goes into the prefix. The tool catalog is fixed at
session start. This is what keeps llama.cpp's prompt-prefix cache warm across turns.

## 7. Transport and protocol

**JSON-RPC 2.0 over WebSocket**, bound to loopback plus the Tailscale interface. A
single transport serves every client. A separate local IPC path was considered and
rejected: two transports to write and test, and Android needs the network transport
regardless.

Connections from non-loopback addresses must present a bearer token from config.
Tailscale supplies encryption and device identity underneath.

### Bidirectional, load-bearing

The daemon does not merely stream events downward; it issues *requests* to clients and
waits for answers — `permission.request` before a gated tool call, `ui.ask` when a
module needs user input. **(re-decided)** Revision 1 reused this framing for the
plugin protocol; there is no plugin protocol now, so the framing has one user.

### Multi-client is the normal case

The TUI and the phone may both be attached to one live session. Events broadcast to all
subscribers. Requests needing an answer broadcast to all attached clients and resolve
**first responder wins**; late answers receive `already_resolved` and the client
dismisses the prompt.

A direct consequence: a destructive command can be approved from a phone in a pocket.
Clients render risky permission prompts with friction proportionate to the risk, and
the Android client applies more friction than the TUI.

### Handshake **(core gap)**

The first message on every connection is `nabu.hello {client, client_version,
protocol_version}` → `{daemon_version, protocol_version, capabilities[]}`. A
mismatched major protocol version is refused with `nabu_protocol_mismatch`. Three
client codebases in three languages need this on day one, not after the first
incompatible change.

### Streaming deltas are not events **(core gap)**

Clients need token-by-token rendering; the log needs whole messages. Subscribed
clients receive `nabu.session.delta {session_id, turn_id, text}` notifications while
the model streams. Deltas are ephemeral: never logged, never replayed, and superseded
by the final `message` event, which carries the complete text. A client that missed
the deltas (reconnected, or the phone) renders from the event and loses nothing.

### Interrupt and steer

`nabu.session.interrupt` cancels the current provider stream and tool execution; the
partial assistant text is logged as a `message` with `interrupted: true`, and the
session returns to `idle`. `nabu.session.stop` ends the session. `send_prompt` while
`running` does not queue: the `message` is appended immediately, and because every
request is assembled from the log, the loop picks it up at the next turn boundary.
That is how the owner steers a running agent from the TUI or the phone.

### Method set (additions to revision 1)

`nabu.hello`, `nabu.session.set_goal`, `nabu.session.clear_goal`,
`nabu.session.state` (returns `{state, options, goal, tasks, budget, turns, usage}`),
`nabu.session.resume {budget?}`, `nabu.session.set_option {key, value}`, and
`nabu.session.update_tasks` for client-side task edits (appended with
`source: client`). The veto-message template and the Current state block format are
normative text in `protocol/spec.md`.

## 8. Daemon internals

Go packages, each independently testable, each with one clear purpose:

| Package | Responsibility |
|---|---|
| `session` | Event log, cursors, index, persistence |
| `agent` | The agent loop: request assembly, streaming, tool dispatch, stop gate, compaction, budget |
| `provider` | OpenAI-compatible streaming client, retries with backoff, concurrency limits, usage |
| `tools` | Built-in tools: bash, read, write, edit, glob, grep, task.update — registered through the same registry modules use |
| `module` | The `Module` interface, hook interfaces, `Host` interface, registry, dispatch, isolation |
| `modules/*` | The modules themselves (§14) |
| `api` | WebSocket surface, subscriptions, event fan-out |
| `notify` | FCM dispatch |
| `workspace` | Working directory, workspace key, trust, permission plumbing |

### The agent loop

Assemble request → stream from provider → parse tool calls → gate → execute → append
results → repeat. When the model emits no tool calls, the loop does **not** stop; it
asks the stop gate (§10.1). Every transition appends to the log.

### Daemon lifecycle, storage, and identifiers **(core gap)**

- `nabu daemon` runs the daemon in the foreground. Every other CLI command connects
  to a running daemon and, if none is listening, **starts one detached** and waits for
  its handshake. Claude Code's delegation must never fail with "daemon not running."
  `nabu daemon stop` performs the graceful shutdown below.
- Storage lives under `~/.nabu/`: `config.toml`, `sessions/<id>.jsonl` (one
  append-only file per session) plus `sessions/index.json`, `memory/`,
  `modules/<name>/`, `daemon.log` (structured, `log/slog`), `daemon.pid`, and
  `daemon.port`.
- Session and event ids are ULIDs: time-ordered, unique without coordination, and
  safe to generate on any client for outbox items before the daemon assigns the
  canonical id.

### Configuration **(core gap)**

TOML. `~/.nabu/config.toml` is the base; a trusted workspace may add
`<workspace>/.nabu/config.toml`, which overlays it. Untrusted workspaces are
prompted on first use and their overlay is ignored until trusted. Sections:
`[daemon]` (bind, token, log level), `[providers.<name>]` (base URL, key,
`max_in_flight`, `tasks_enabled`), `[budget]`, and one `[modules.<name>]` section per
module with at least `enabled`. Every module config value has a default; a missing
section means "on, with defaults."

### Permission modes

A session option, `permission_mode: ask | auto | bypass`, set at creation and
changeable mid-session through `set_option` (logged as `options_change`). `ask` is the
default; `auto` lets the `guard` module approve edits inside the workspace and
low-risk commands without asking; `bypass` approves everything and appends a
`notice` when set. The mode is core state because clients render and toggle it; what
each mode permits is the `guard` module's policy.

### Graceful restart

Because modules are compiled in, changing one means restarting the daemon, and the
daemon owns live sessions. Restart is therefore designed, not left to chance:

- On shutdown the daemon cancels in-flight provider streams, appends a `notice` to
  each interrupted session, marks it `paused` with reason `interrupted`, and exits.
  Nothing is lost: the log has every event up to the interruption.
- On start, `paused` sessions are listed, not resumed. Interactive clients show a
  resume affordance. `nabu run` reconnects with backoff and auto-resumes its session
  once, so a restart during a delegated run costs one turn, not the run.
- Clients treat a dropped connection as "reconnect and fetch from cursor," never as
  "the run ended."

## 9. Tasks

### 9.1 Model

A session has at most one task list. It is small (tens of items) and is a
**snapshot** in the log: every change appends a `tasks` event containing the full
list. Clients render the latest `tasks` event with no reducer.

```jsonc
{
  "type": "tasks",
  "data": {
    "revision": 4,
    "source": "model",                 // model | client | module:<name>
    "tasks": [
      {
        "id": "t1",
        "title": "Add cursor validation to events_after",
        "status": "done",              // pending | in_progress | blocked | done | failed | cancelled
        "done_when": "go test ./protocol/ passes with the new 04-nonexistent vector",
        "check": "go test ./protocol/",   // optional command; exit 0 = pass
        "blocked_by": [],
        "note": "",
        "evidence": "evt-041"          // id of the passing check event; set by core, never the model
      }
    ]
  }
}
```

`done_when` is prose: the observable condition under which the task is finished.
`check` is an optional command that decides it mechanically. **Evidence is
mechanical:** when core applies a transition to `done`, it sets `evidence` to the most
recent passing `check` event carrying that `task_id`, or leaves it empty. Core records
whether evidence exists and holds no opinion on whether it is required.

### 9.2 Tool

One built-in tool: `task.update { tasks: [ {id?, title, status, done_when?, check?,
blocked_by?, note?} ] }`. Whole-list replace semantics; ids are stable so modules and
clients can diff; missing ids are assigned. The tool result echoes the rendered current
list, which is how the model sees task state on later turns without prefix rewriting.
There is no `task.read`.

One tool rather than four: fewer tool schemas in the prompt for a 35B model, one event
type, no dependency-graph machinery in v1. `blocked_by` is in the schema so
multi-session ownership can be added later without a format change.

### 9.3 Visibility

TUI task pane and Android task card render the latest `tasks` event. `nabu status
<id>` prints goal, task counts, and the current `in_progress` task. The report carries
`tasks: {total, done, open: [...]}`.

### 9.4 Switchability

`tasks.enabled` per provider or model, default `true`. In August 2026 both Claude
Code and Codex turned task tools off by default for frontier models (they track
multi-step work in prose and the tool calls cost roughly 25% more tokens); a local
35B model is the class that still benefits. When off, the tool is not registered and
the `verify` module's open-task rule is inert.

## 10. Verification — how the loop knows the work is done

The single most-documented failure mode of coding agents is the model deciding it is
finished: premature victory, "tests pass" while the fix sits uncommitted. The
consensus mechanism across Claude Code's Stop hook and `/goal`, Anthropic's
long-running-agent harness, and the 2026 harness literature is a **stop gate**, with
the rule that *the model that did the work does not decide it is finished*.
Deterministic checks beat judged checks; judged checks beat self-report.

### 10.1 The stop gate (core)

The loop's stop condition is "no tool calls **and no veto**."

```
loop:
  assemble request from log
  stream from provider; parse tool calls
  if tool calls:
      gate → execute → append tool_result events → continue
  else:
      verdicts = StopGate modules, in registration order   (each: allow | veto{reason})
      if no veto:                       stop → completed
      elif budget allows and progress:  append stop_veto events → continue
      else:                             stop → blocked
```

- **Progress detector:** `no_progress_turns` (default 3) consecutive assistant turns
  with no tool calls and an unchanged set of veto reasons ends the loop as `blocked`.
- **Vetoes are turns** and count against the budget (§12).
- **All gates are asked**, not short-circuited, so the model sees every objection at
  once rather than one per turn.
- With no `StopGate` modules enabled the loop behaves as revision 1 described, and
  the daemon appends a `notice` at session start saying so.

Stop-gate modules receive `{goal, tasks, last_assistant_message, turns_since_user,
veto_count}` and a session handle for reading the transcript.

### 10.2 Task-level definition of done (`verify` module)

- **`require_done_when`** (on under `nabu run` and whenever a goal is set; off for a
  plain interactive session unless configured on): a `task.update` that introduces a
  task without `done_when` is denied through the tool gate with the reason "every task
  needs a done_when: the observable condition that proves it finished." This is what
  makes the agent *look for the verification step before starting*: it cannot write a
  plan without writing the check for each step. The owner chose not to have it nag
  during casual interactive use.
- **Mechanical checks:** when a task with a `check` moves to `done`, the module runs
  the command in the workspace before allowing the call. Exit 0: it appends a passing
  `check` event and allows. Non-zero: the call is denied with the tail of the output;
  the task stays `in_progress` and the model sees why.
- **Judged checks:** a task with `done_when` but no `check` may move to `done`
  unevidenced. The stop-gate judge is told which tasks are unevidenced.
- **Open-task veto:** at the stop gate, any `pending` or `in_progress` task produces a
  veto listing them. `blocked` tasks must carry a `note`; they are reported, not
  vetoed.

### 10.3 Run-level goal and the judge (`verify` module; core holds the state)

- **Setting a goal.** `nabu run --done-when "…"`, TUI `/goal …`, or
  `nabu.session.set_goal`. Core appends a `goal` event (`state: set`) and carries it in
  session state. It survives compaction and resume. One goal per session.
- **Judging.** At the stop gate, if a goal is set, the module calls the host's model
  API with the condition, the task list with evidence, and the recent transcript. The
  judge runs in a **fresh context** and returns exactly one of `met`, `unmet{reason}`,
  `impossible{reason}`. `unmet` becomes a veto; the other two append a `goal` event and
  allow the stop. `verify.judge_model` selects the model, defaulting to the session's
  provider; on the local llama.cpp server the call serializes behind the loop through
  `max-in-flight`, which is fine because the loop is paused at the gate.
- **Veto rendering.** Each veto is a `stop_veto` event. Request assembly renders
  outstanding vetoes as one fixed-template user-role message. The template is normative
  so replay is deterministic and the log never contains a forged user message.
- **Deterministic first.** Open tasks, failed checks, and a dirty tree are evaluated
  before paying for a judge call.

### 10.4 Workspace-level checks (`verify` and `report` modules)

Per workspace: `verify.command` (the canonical project gate) and
`verify.require_clean_tree` (default on). At the stop gate the module runs the gate
and vetoes on failure; a dirty tree vetoes with "commit or explain uncommitted
changes." This targets the pi failure mode "tests pass but the fix is uncommitted."

## 11. Memory

### 11.1 Placement

Memory is the `memory` module. Nothing in `agent`, `session`, or the protocol knows it
exists: a memory write is a `tool_call` with `source: module:memory`, a recalled index
is a `context` event. It can be disabled in config, and a second memory module could
be written and compared against it. **(re-decided)** Revision 1's argument for keeping
skills *out* of the extension mechanism (a subtly broken plugin silently degrading
every session) does not apply to in-tree modules that are compiled, tested, and
restarted with the daemon; the same reasoning makes memory a module without hesitation.

### 11.2 Scope and storage

```
~/.nabu/memory/
  global/                 # user-level: who the owner is, how they like to work
    MEMORY.md
    *.md
  ws/<workspace-key>/     # per repository
    MEMORY.md
    *.md
```

`workspace-key` is a slug of the git common dir when the workspace is inside a
repository (so worktrees and nested dirs share memory), else of the absolute path. The
`workspace` package computes it once per session.

File format is **identical to Claude Code auto memory**: frontmatter with `name`,
`description`, `type: user | feedback | project | reference`, `modified`; a body
stating the fact, then `**Why:**` and `**How to apply:**`. `MEMORY.md` is one line per
memory, capped at 200 lines / 25 KB. The owner already lives with this format; its
discipline is the "decisions, not descriptions" rule from the 2026 rate-distortion
memory work; and `memory.import_dirs` can point at an existing Claude Code memory
directory **read-only**, so nabu starts a project already knowing what Claude knows.

The memory directory is a git repository; the module commits after any session that
wrote to it. Rollback, diff, and "which session taught it this" come free.

### 11.3 Recall

1. **Session start:** both indexes (global, then workspace) are returned from
   `SessionStart` as a `context` block, `slot: prefix`. Returned again from
   `AfterCompaction` following a `summarize` compaction.
2. **On demand:** `memory.recall {query, scope?}` runs BM25 over name, description,
   and body and returns the top five files in full. No embeddings: the corpus is small,
   BM25 is deterministic, and it needs no model.
3. **Automatic (later):** at `before_request` on a user turn, BM25 over the prompt
   injects at most three matching *descriptions* as a `slot: suffix` block. Off until
   measured.

### 11.4 Write

- `memory.save {scope, name, type, description, body}` is model-initiated. The
  module's injected instructions say when: a correction from the user, a confirmed
  approach, a decision not derivable from code or git, a pointer outside the repo.
  Never architecture, file layout, or anything the repo records.
- `memory.forget {name}` deletes the file and its index line; git keeps history.
- **Curator pass** (`memory.curator`, default on): at `session_end` and at
  `before_compaction`, the module asks the configured model in a fresh context whether
  the events since the last pass contain anything a future session would act on
  differently. At most three memories per pass; same-subject files are updated, not
  duplicated. The curator writes through the host's tool API, so every write is a
  logged `tool_call` attributed to `module:memory`: visible, gateable, reversible. If
  open tasks remain at `session_end`, it writes a `project` memory named
  `<workspace>-in-progress`. That is how a *new* session on the same repo picks up
  where the last one stopped.
- **Consolidation:** near the index cap, the curator merges duplicates, drops old and
  contradicted entries, and rewrites the index.

### 11.5 What memory is not

Not the session log (already durable and replayable). Not a scratchpad for large tool
outputs (that is §6). Not shared across machines in v1.

## 12. Budget — the loop bound

With a stop gate, an unmeetable goal plus a judge that keeps saying "not yet" is an
infinite loop against the local model. The budget is the loop bound: a rail, not a
quota.

- **Unit:** turns (one model round trip). Optional caps on tokens and USD, enforced
  only for providers that report them. Module model calls count toward token caps.
- **Scope:** per session, set at creation (`nabu run --max-turns N`; TUI default from
  config; `0` = unlimited). Recorded as a `budget` event; `nabu resume <id>
  --max-turns M` appends another and continues.
- **Exhaustion:** the session moves to `paused`; nothing is killed; the CLI exits with
  the `paused` code and prints the resume command. The report is emitted on pause as
  well as on stop.
- **Defaults:** `max_turns` unlimited interactively and 60 for `nabu run`;
  `max_consecutive_vetoes` 5; `no_progress_turns` 3.

This keeps the budget-exhausted exit code and the resume mechanism and removes pi's
"blind repeated resumes" failure mode, because the report says where the run stopped
and why.

## 13. Headless mode and Claude interop

"Claude interop" means **Claude Code must be able to delegate work to nabu**, the way
it delegates to `pi` today. It does *not* mean using Claude as a model backend.

| pi failure mode | nabu's answer |
|---|---|
| Determining whether `pi` is still running requires filtering `node.exe` processes by command line | `nabu status` queries the daemon. Sessions have IDs and states. |
| Killing the launcher leaves a detached agent that races the caller's edits | The run lives in the daemon and is addressable. Killing the CLI detaches. `nabu attach <id>` resumes watching; `nabu stop <id>` genuinely stops it. |
| Piped output is buffered until exit | `--json` emits flushed JSONL per event. Silence means dead. |
| Exit code 1 does not reliably indicate failure | Exit codes mirror the session state after the run: `completed`, `blocked`, `paused`, `error`. A denied tool call is an error result the model sees; if it cannot proceed, the run ends `blocked` with the denial in the report's `checks`. |
| A finite per-run budget forces blind repeated resumes | Budget is a turn cap that pauses; the report says exactly where and why (§12). |
| The agent reports "tests pass" while its fixes sit uncommitted | The stop gate refuses to stop on a dirty tree or a failed gate (§10.4); the report records it (below). |
| The agent declares done early | `--done-when` sets a goal judged in a fresh context (§10.3). |

### The run report

At stop or pause the daemon emits a `report` event so the caller can verify outcomes
*without* trusting the model's narration.

**(re-decided)** Revision 1 had core define the schema and plugins supply every field.
Now core fills the fields that are derivable from the log — `exit_status`, `goal`,
`tasks`, `checks` — and `Reporter` modules fill the fields that require observing the
workspace: `files_touched`, `commits`, `tree_dirty`, and the final `verify.command`
result. Core still computes nothing about the workspace. The schema is fixed in the
protocol; every field is named; unknown fields are rejected.

```jsonc
{
  "exit_status": "completed",
  "goal":   {"condition": "...", "state": "met", "reason": "..."},
  "tasks":  {"total": 6, "done": 6, "open": []},
  "checks": [{"name": "verify.command", "status": "pass", "summary": "ok  ./... 2.1s"}],
  "files_touched": ["..."], "commits": ["a1b2c3"], "tree_dirty": false
}
```

## 14. Modules

**(re-decided)** Replaces revision 1 §8 "Plugins — microkernel."

**The daemon is mostly plumbing; the real logic lives in modules.** A module is a Go
package under `daemon/modules/` that implements the `Module` interface and any of the
hook interfaces. Modules are compiled into the daemon, registered in one list, and
enabled or disabled by config. There is no bundle, no marketplace, no install step.
Extending nabu means adding a package to the list and rebuilding.

### 14.1 Interfaces

```go
package module

type Module interface {
    Name() string
    Init(h Host, cfg Config) error
}

// Optional hook interfaces, asserted at registration (the http.Hijacker pattern).
// Every returned Context becomes a `context` event before it reaches the model.
// Prefix blocks may only come from SessionStart and AfterCompaction (the prefix is
// fixed between compactions); BeforeRequest may return suffix blocks only.
type SessionStarter  interface { SessionStart(ctx, Session) ([]Context, error) }    // prefix blocks: skill index, memory index
type RequestHook     interface { BeforeRequest(ctx, Session) ([]Context, error) }   // suffix blocks for this request only
type ToolGate        interface { GateTool(ctx, Session, ToolCall) Verdict }         // Allow | Deny{reason} | Ask{prompt, risk}
type ToolProvider    interface { Tools() []Tool }                                   // registered through the same registry as built-ins
type ToolObserver    interface { ToolResult(ctx, Session, ToolCall, ToolResult) }
type TurnObserver    interface { TurnEnd(ctx, Session) }
type StopGate        interface { BeforeStop(ctx, Session, StopInfo) StopVerdict }   // Allow | Veto{reason}
type CompactionHook  interface { BeforeCompaction(ctx, Session, Range) (preserve []string); AfterCompaction(ctx, Session) ([]Context, error) }
type ResumeHook      interface { SessionResume(ctx, Session) error }
type SessionEnder    interface { SessionEnd(ctx, Session) }
type Reporter        interface { Report(ctx, Session) (ReportFields, error) }
```

Subscription is the type assertion. A module that does not implement `StopGate` costs
the stop gate nothing. Revision 1's subscription-at-handshake, and the IPC cost it
existed to control, are gone.

### 14.2 The host API

Modules see the daemon only through `Host`:

| Facility | What it gives a module |
|---|---|
| `Model` | `Complete(ctx, req)` through `provider`, with limits and usage accounting |
| `Session` | Read events after a cursor; append `check`, `goal` verdicts, `notice`; current goal, tasks, budget |
| `Tools` | Call any registered tool as `source: module:<name>`, through the same gate the model's calls go through |
| `UI` | `Ask(ctx, prompt)` → `nabu.rpc.ui.ask` to attached clients, first responder wins |
| `Workspace` | Path, workspace key, per-module data dir `~/.nabu/modules/<name>/` |
| `Config` | The module's config section |

### 14.3 Isolation and ordering

- Every hook call runs under `recover()` with a per-hook timeout from config
  (`module.hook_timeout`, default 30 s; `StopGate` and `ToolGate` may declare a longer
  one because they run test commands). A module that panics or times out is disabled
  for the rest of the session and a `notice` is appended. The session continues.
- Hooks run synchronously on the session's goroutine, in registration order. Modules
  are singletons and must be safe across concurrent sessions; per-session state
  belongs in the log, not in the module.
- Gates are asked in order; `Deny` short-circuits a tool gate, `Ask` is asked once
  even if several modules return it. Stop gates are all asked (§10.1).

### 14.4 The boundary is enforced, not just described

A test in the `module` package walks the import graph of every package under
`daemon/modules/` and fails if any imports a daemon package other than `module` (plus
the standard library and third-party code). Mechanism/policy is a compile-time rule.

### 14.5 No privileged path for built-in tools

bash/read/write/edit/glob/grep/task.update are built in because they are universal and
hot, but they register through the *same* `ToolProvider` registry. If a module cannot
replace `bash`, the abstraction is fake.

### 14.6 Rejected alternatives

**Subprocess plugins over JSON-RPC stdio** (revision 1's choice). Bought language
freedom, third-party isolation, and hot reload — for extension authors who do not
exist. Cost a process supervisor on Windows (spawn, handshake, health, job objects so
children die with the parent; the pi orphan problem is a supervision failure), a second
RPC surface that grew a method for every capability a plugin needed, IPC on every
hook, cross-process debugging, and N binaries whose versions must match. The stance
survives; the boundary does not.

**Embedded script runtime** (Lua or JS) and **WASM components**: rejected in revision
1 for sandboxing and toolchain reasons; the stronger reason now is that they solve a
multi-author problem nabu does not have.

**A door left open, not built:** the hook interfaces and `Host` are the contract. An
adapter that implements them by proxying to a subprocess over JSON-RPC is how
non-Go code or MCP servers would attach later. When MCP is wanted, an `mcp` module
implementing `ToolProvider` *is* that adapter, and it is the one place a subprocess
boundary belongs.

### 14.7 The modules

| Module | Hooks | Does |
|---|---|---|
| `skills` | `SessionStarter`, `ToolProvider`, `CompactionHook` | Discovers skills from configured paths (including `~/.claude/skills` for Claude Code compatibility); injects the skill index as a prefix `context` at session start and again after a `summarize` compaction; registers `skill.load` so the model can read a skill body on demand. **(re-decided)** Revision 1 made skills a core exception to the plugin rule; with modules in-tree the reason is moot, and skills are the first proof that the module API is sufficient. |
| `guard` | `ToolGate` | bash-guard rules: allow / deny / ask by command pattern, path, and risk tier. Replaces the "unguarded first run" problem: it is always compiled in, and disabling it appends a `notice`. |
| `verify` | `ToolGate`, `StopGate`, `Reporter` | §10: `require_done_when`, mechanical task checks, open-task veto, goal judge, workspace gate, dirty-tree veto. |
| `report` | `Reporter` | `files_touched`, `commits`, `tree_dirty`. |
| `memory` | `SessionStarter`, `ToolProvider`, `CompactionHook`, `SessionEnder`; later `RequestHook` for automatic recall | §11. |

Nothing here is a "default." These are the modules. Each has a config section and an
`enabled` flag.

## 15. Clients

### TUI (Go, Bubble Tea)

Attaches to the daemon, subscribes to a session, renders the event stream. Close it
mid-run and the agent continues; reattach and the log replays from the cursor; run two
TUIs against one session. Views: session list across workspaces, transcript, task
pane, goal badge, check results inline, and a permission/ask overlay. `/goal` sets a
goal. It holds no session state of its own.

### Android (Kotlin / Compose / Room)

Same protocol over Tailscale. Room mirrors the event log for offline reading; a
separate outbox table holds pending prompts. Screens: session list, transcript, task
card with tap-to-complete (sent as `update_tasks`, `source: client`), composer,
permission prompts.

Two Android-specific requirements to be designed rather than discovered:

1. Permission prompts need more friction than the TUI equivalent.
2. A partially-synced session must render **honestly** — visibly truncated, never
   silently short.

### Desktop GUI (Rust)

A later milestone. A third client against the same protocol, requiring no daemon
changes.

## 16. Notifications

**Firebase Cloud Messaging.** Chosen over a tailnet-only foreground service (cannot
fire when the app is force-stopped) and over poll-on-open (never tells you a run
finished). Accepted cost: a Google Cloud project, a Firebase dependency in the Android
app, and outbound daemon access to FCM.

**Hard constraint: FCM payloads carry identifiers and status only — never transcript
content.** "run 4f2a paused, 3/6 tasks done" is acceptable; message text is not.

Notifications cover "a run finished or paused," "a run needs input," and "you have
pending outbox items now that connectivity is back."

## 17. Remote access and sync transport

**Direct over Tailscale.** The Android app connects straight to the daemon's API on the
owner's desktop. No relay, no accounts, no hosted sync service.

A relay on the owner's VPS was rejected: an entire additional service to build, secure,
deploy, and keep running; session content transiting a box requiring hardening; and it
does not solve the desktop-asleep case anyway.

Accepted limitations: the desktop must be awake to run or sync anything, and Tailscale
is a hard dependency of the product. Offline *reading* is unaffected.

## 18. Testing and verification

- Each daemon package is independently testable by design. Modules are tested against
  a fake `Host`, which is a struct, not a protocol.
- **The conformance vector suite is the central correctness artifact:** input event
  logs with expected results, run by both the Go and Kotlin implementations in CI. It
  pins event schema, ordering, cursor semantics, compaction rendering including the
  Current state block, task snapshot rendering, and partial-sync rendering. It belongs
  to P0.
- **Request assembly is tested by replay:** given a log and a module set, the
  assembled request is deterministic and asserted byte-for-byte.
- The module import-boundary test (§14.4) runs in CI.
- Every test in the repo's own gate (`go test ./...`) is what `verify.command` runs
  when nabu works on nabu.

## 19. Milestones

Each milestone gets its own brainstorm, spec, and implementation plan, and each ends
somewhere genuinely useful. **(re-decided)** Revision 1 put the plugin system (P2)
before the TUI (P3) because the agent was unguarded without plugins. The guard module
is now compiled in from P1, so the TUI moves up and nabu becomes the daily driver one
milestone sooner.

| Milestone | Delivers | Ends with |
|---|---|---|
| **P0 — Protocol and session format** | Event schema (14 types), session states and options, JSON-RPC methods including `hello` and the ephemeral `delta` notification, error codes, the veto template and Current state block, conformance vectors, Go `protocol` package. | The contract that makes the polyglot client split safe. |
| **P1 — Daemon, loop, modules, CLI** | `session`, `agent` (loop, stop gate, both compaction stages, budget, graceful restart), `provider`, `tools`, `module`, modules `skills`, `guard`, `verify`, `report`, the WebSocket API, and `run` / `status` / `attach` / `stop` / `resume`. | Claude Code delegates to nabu with `--done-when`, gets a verifiable report, and the agent is guarded from the first run. |
| **P2 — TUI** | Bubble Tea client with transcript, task pane, goal, permission overlay. | nabu replaces pi as the daily driver. |
| **P3 — Memory** | The `memory` module: files, index, recall, curator, consolidation, Claude Code import. | Sessions on a repo start already knowing what the last one learned. |
| **P4 — Android** | Room mirror, outbox, offline rendering, task card. | The phone is a real client. |
| **P5 — FCM notifications** | | |
| **P6 — Rust desktop GUI** | | |

## 20. Repository layout (target)

```
nabu/
  daemon/
    session/        event log, cursors, index, persistence
    agent/          loop, assembly, stop gate, compaction, budget, restart
    provider/       OpenAI-compatible streaming client, limits, usage
    tools/          built-ins incl. task.update, registered like any module's tools
    module/         Module + hook interfaces, Host, registry, dispatch, isolation, boundary test
    modules/
      all.go        the registration list
      skills/
      guard/
      verify/
      report/
      memory/
    api/            WebSocket surface, subscriptions, fan-out
    notify/         FCM dispatch
    workspace/      working directory, workspace key, trust, permission plumbing
  clients/
    go-tui/
    android/
    rust-gui/
  protocol/         normative spec, JSON schemas, conformance vectors, Go protocol package
  docs/superpowers/specs/
```

## 21. Decisions and rejected alternatives

| # | Decision | Chosen | Rejected and why |
|---|---|---|---|
| 1 | Topology | Daemon-owns-sessions; the daemon/client protocol is the only process boundary | Monolith-per-session (delegation failure modes survive); VPS-hosted daemon (repos and model server on desktop) |
| 2 | Languages | Go daemon and modules, Go TUI, Kotlin Android, Rust GUI later | Rust daemon (UniFFI sharing) — duplicate sync logic accepted, mitigated by conformance vectors |
| 3 | Model providers | OpenAI-compatible only, behind a provider interface; every call including module calls goes through it | Native Anthropic API; Claude Code CLI as backend |
| 4 | Session storage | Append-only log, daemon sole writer, client outbox for offline prompts | Concurrent writers with merge logic |
| 5 | Context management | Two compaction stages as events; deterministic Current state block after the summary | Compaction as deletion; task state depending on summary fidelity |
| 6 | Transport | Single JSON-RPC-over-WebSocket for all clients | Separate local IPC |
| 7 | Request resolution | Bidirectional, first-responder-wins | Daemon-only streaming |
| 8 | **Extension mechanism (re-decided)** | In-process Go modules behind hook interfaces and a `Host` API; compiled in, config-toggled, boundary enforced by test | Subprocess plugins (supervisor on Windows, second RPC surface, IPC per hook, for authors who do not exist); embedded scripting; WASM |
| 9 | Core vs module | Mechanism in core, policy in modules; no privileged path for built-in tools | Core shipping gating or verification opinions |
| 10 | **Skills (re-decided)** | A module (`skills`), the first proof of the module API | Core exception (reason was plugin fragility, now moot) |
| 11 | **Run report (re-decided)** | Core fills log-derivable fields; `Reporter` modules fill workspace observations; fixed schema | Plugins supplying every field; no schema |
| 12 | Notifications | FCM, metadata-only payloads | Foreground service; poll-on-open |
| 13 | Remote transport | Direct over Tailscale | VPS relay |
| 14 | **Milestone order (re-decided)** | P1 daemon+modules → P2 TUI → P3 memory | TUI after an extension milestone (the reason, an unguarded agent, is gone) |
| 15 | Stop condition | No tool calls **and** no stop-gate veto; progress detector; all gates asked | Model-decides-done; checks in core |
| 16 | Task ledger | Core schema, one `task.update` tool, snapshot events, switchable per model | Module-only tasks (clients need a contract); four-tool shape; delta events |
| 17 | Definition of done | Per-task `done_when` prose plus optional `check`; required under `nabu run` and when a goal is set; evidence set mechanically by core | Free-form plan text; mandatory commands for every task; requiring `done_when` in casual interactive sessions (owner: too naggy) |
| 18 | Judge | Fresh-context model call from the `verify` module; deterministic rules first | Same-context self-evaluation; judge in core |
| 19 | Memory placement | The `memory` module; nothing memory-specific in core or protocol | Memory in `agent`/`session` |
| 20 | Memory storage | Markdown + frontmatter + index, Claude Code-compatible, git-versioned, BM25 recall | Embeddings; knowledge graph; SQLite |
| 21 | Memory curation | Model-proposed at `session_end` / `before_compaction`, bounded, logged as tool calls | Model self-editing an always-in-context block; no curation |
| 22 | Budget | Turn cap per session, pause-not-kill, resume appends | No budget; per-invocation budget |
| 23 | Injected context | Always a `context` event; request assembly is a pure function of the log | Unlogged injection |
| 24 | Prompt layout | Stable prefix; dynamic state in tool results and the Current state block; fixed tool catalog | Rewriting the prefix with live state |
| 25 | Restart | Graceful: interrupted sessions `paused` with a `notice`; `nabu run` auto-resumes once | Hot-reloadable extensions (the thing plugins bought) |
| 26 | Module isolation | `recover()` + per-hook timeout; offending module disabled per session with a `notice` | Trusting modules blindly; process isolation |
| 27 | Session options | `options_change {key, from, to}` for model, compaction, permission mode | A separate event per option (`model_change` alone left the compaction toggle unlogged) |
| 28 | Streaming | Ephemeral `delta` notifications, superseded by the whole `message` event | Logging deltas (bloats the log, complicates replay); no streaming (TUI quality) |
| 29 | Steering | `send_prompt` appends immediately; the loop sees it at the next assembly | A separate queue with its own semantics |
| 30 | Daemon start | CLI auto-starts a detached daemon; `nabu daemon` for foreground | Requiring the owner to start it (delegation would fail silently) |
| 31 | Judge model | Session's provider, fresh context (owner, 2026-09-11) | Defaulting to a hosted model |
| 32 | Claude Code memory import | Opt-in via `memory.import_dirs` (owner, 2026-09-11) | Auto-detect and read by default |

## 22. Owner decisions, resolved 2026-09-11

| Question | Answer |
|---|---|
| Skills become a module | Confirmed |
| Milestone order P1 daemon → P2 TUI → P3 memory | Confirmed |
| Judge model | Session's provider, fresh context |
| `require_done_when` in interactive sessions | **Vetoed.** On under `nabu run` and when a goal is set only |
| Claude Code memory import | Opt-in per workspace |
| Phone tap-to-complete counts as evidence | Confirmed |
| Commit policy | Commit to `main` as work proceeds, `area: summary` style |

## Appendix A — Research summary

What the field converged on in 2025–2026, kept for the record.

**Tasks.** Every serious harness has an explicit ledger: Claude Code's persistent
Tasks (`blockedBy`, `owner`, shared list ids), Codex's whole-list `update_plan`,
Anthropic's `feature_list.json` where the agent may only flip a `passes` field, the
2026 harness blueprint's `tasks.json`. The task object is attention control for the
model and a progress projection for humans. In August 2026 Claude Code and Codex both
turned task tools off by default for frontier models (about 25% fewer tokens); smaller
models still benefit.

**Verification.** Claude Code's Stop hook and `/goal` (a separate small model judges a
written condition after every turn; "not yet" reasons become the next instruction; a
no-progress detector ends the loop); "guardrails in the runtime, not the prompt";
Anthropic's long-running-agent harness (no feature marked passing without an
end-to-end check). Deterministic checks beat judged checks; judged checks beat
self-report.

**Memory.** Three tiers (session, working, long-term). The dominant production shape
for long-term memory is plain files with an index (Claude Code auto memory, the Claude
API memory tool, Letta memory blocks with sleep-time consolidation). Two 2026 papers
give the rules: store decisions, not descriptions; keep the memory control plane out of
the model (in-model self-editing is worst at forgetting; harness-managed memory with
versioning and explicit deletion is best). Targeted 5 K-token recall beats 100 K of
preloaded summary.

### Sources

- Anthropic — Effective harnesses for long-running agents:
  https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents
- Anthropic — Effective context engineering for AI agents:
  https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents
- Claude Code docs — `/goal`: https://code.claude.com/docs/en/goal
- Claude Code docs — memory: https://code.claude.com/docs/en/memory
- Claude Code v2.1.233 — task tools off by default on frontier models:
  https://dev.classmethod.jp/en/articles/20260815-cc-updates-v2-1-233/
- Claude Code Tasks data model: https://claudearchitect.com/docs/claude-code/claude-code-tasks-guide/
- Codex `update_plan` default-off change: https://github.com/openai/codex/issues/42365
- Modern Agent Harness Blueprint 2026: https://gist.github.com/amazingvince/52158d00fb8b3ba1b8476bc62bb562e3
- Remember the Decision, Not the Description: https://arxiv.org/pdf/2605.10870
- Control-Plane Placement Shapes Forgetting: https://arxiv.org/pdf/2606.15903
- Letta — Memory blocks: https://www.letta.com/blog/memory-blocks/
- Harness Engineering for Agentic AI Coding Tools: https://arxiv.org/abs/2602.14690
- Sourcegraph — Context engineering: https://sourcegraph.com/blog/context-engineering
- Ralph Wiggum loop: https://www.codecentric.de/en/knowledge-hub/blog/the-ralph-wiggum-loop-autonomous-code-generation-with-a-fresh-context
