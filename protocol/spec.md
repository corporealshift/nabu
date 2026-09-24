# nabu protocol — normative specification

**Protocol version:** 1.0
**Status:** P0, authoritative. The Go `protocol` package and the Kotlin client both
implement this document and both run the vectors under `vectors/`.

Words in **bold capitals** (MUST, MUST NOT, SHOULD) carry their RFC 2119 meaning.

## 1. Transport

JSON-RPC 2.0 over WebSocket. The daemon binds loopback plus the Tailscale interface.
Connections from non-loopback addresses MUST present a bearer token
(`Authorization: Bearer <token>`) during the WebSocket upgrade; a missing or wrong
token is refused with `nabu_unauthorized`.

Every message is one JSON-RPC object per WebSocket text frame. Batches are not
supported.

The daemon answers a connection's calls in the order they arrive, with one exception:
`nabu.session.interrupt` and `nabu.session.stop` are answered at once, because each
exists to end whatever is ahead of it. A slow call does not stop the daemon reading the
connection, so pings and replies to the daemon's own requests (§7.18) keep flowing
while one runs.

### 1.1 Handshake

The first request on a connection MUST be `nabu.hello`. Any other method before a
successful hello is refused with `nabu_protocol_mismatch`.

```jsonc
// client → daemon
{"jsonrpc":"2.0","id":1,"method":"nabu.hello","params":{
  "client":"go-tui","client_version":"0.1.0","protocol_version":"1.0"}}
// daemon → client
{"jsonrpc":"2.0","id":1,"result":{
  "daemon_version":"0.1.0","protocol_version":"1.0","capabilities":["delta","tasks","goal"]}}
```

Versions are `MAJOR.MINOR`. A client whose MAJOR differs from the daemon's is refused
with `nabu_protocol_mismatch`. A differing MINOR is accepted; the client MUST ignore
fields and capabilities it does not know.

## 2. Identifiers and time

Session ids and event ids are **ULIDs** (26 characters, Crockford base32, per the ULID
spec): 48 bits of millisecond time followed by 80 random bits. Timestamps are RFC 3339
with millisecond precision in UTC.

Ids sort by creation order as strings, and a writer MUST guarantee that: two ULIDs
generated in the same millisecond order by their random bits, which is not creation
order. A writer issuing a sequence (a session's events, a daemon's session ids) MUST
ensure each new id sorts strictly after the previous one, generating at the next
millisecond if necessary. Readers may rely on this ordering within one log; they MUST
NOT rely on it across logs written by different daemons.

## 3. The session event log

A session is an append-only sequence of events. Each event is:

| Field | Type | Rules |
|---|---|---|
| `id` | string | ULID. Unique within the daemon. |
| `parent_id` | string \| null | The id of the preceding event in this session; `null` only for the first event, which MUST be of type `session`. |
| `timestamp` | string | RFC 3339 UTC. Informational: ordering is by position in the log, never by timestamp. |
| `type` | string | One of the fifteen types in §3.1. |
| `data` | object | Type-specific payload (§3.1). Unknown extra fields MUST be preserved by readers and MUST NOT cause rejection. |

**The daemon is the sole writer.** Clients never append. A client-originated change
(a prompt, a task edit, an option change) becomes an event only when the daemon
accepts the corresponding method call.

**Position is order.** Readers MUST render events in log order. Timestamps MAY be
non-monotonic (clock adjustments) and MUST NOT be used to reorder.

**The log is never rewritten.** Compaction, interruption, and restart append; they do
not modify or remove.

### 3.1 Event types

Every `source` field, where present, is one of `daemon`, `model`, `client`, or
`module:<name>`.

#### `session` — first event of every log

```jsonc
{"workspace":"C:/Users/kyle/proj","workspace_key":"proj-3f9a1c","context_window":256000,
 "options":{"model":"qwen3.6-35b-a3b","compaction_enabled":true,"permission_mode":"ask"}}
```

`permission_mode` ∈ `ask | auto | bypass`.

`context_window` is the model's context size in tokens, as configured when the session
was created. It is recorded so a client can say how full the context is: the daemon
knows the size and the log carries the usage, but without this a client holds only the
numerator. Zero or absent means the size was not configured, and a client should then
say nothing rather than guess.

#### `message`

```jsonc
{"role":"user","content":"Add cursor validation."}
{"role":"assistant","content":"Done. All tests pass.",
 "usage":{"input_tokens":8123,"output_tokens":212,"cached_tokens":7900},
 "interrupted":false}
```

`role` ∈ `user | assistant`. `usage` and `interrupted` appear only on assistant
messages; `usage` SHOULD be present when the provider reported it; `interrupted` is
`true` when `nabu.session.interrupt` cut the response short.

#### `thinking`

```jsonc
{"content":"17 times 23. 17*20 is 340, plus 17*3 is 51, so 391.","source":"model"}
```

The model's reasoning for the turn that follows it, when the provider reports any.
It is appended **before** the `message` it produced, so a reader that stops at the
message has already seen the thinking behind it.

Thinking is for the reader, never for the model: a `thinking` event MUST NOT appear in
a request (§6.1), and MUST NOT affect the projection (§5). A provider that reports no
reasoning produces no event — an empty `thinking` event is never written.

#### `tool_call`

```jsonc
{"call_id":"call_01","tool":"bash","arguments":{"command":"go test ./..."},"source":"model"}
```

`call_id` correlates with the `tool_result`. `source` is `model` or `module:<name>`.

#### `tool_result`

```jsonc
{"call_id":"call_01","tool":"bash","content":"ok  \t./protocol\t0.4s","status":"ok"}
{"call_id":"call_02","tool":"bash","content":"exit status 1","status":"error","kind":"exit","exit_code":1}
```

`status` ∈ `ok | error`. A denied gate produces `status: error` with the denial
reason as `content`.

`kind` and `exit_code` are optional and describe **how** a call failed, so a
reader does not have to parse `content` to find out. Both MUST be absent when
`status` is `ok`.

| `kind` | meaning |
|---|---|
| `exit` | the process ran and returned non-zero |
| `timeout` | the call outlived its deadline |
| `denied` | a gate or the user refused it |
| `invalid_args` | the arguments were unusable |
| `not_found` | the tool, file or program is not there |
| `io` | the filesystem or the operating system refused |

`kind` MAY be absent on a failure: absent means **unclassified**, never "fine" —
`status` is what says whether the call succeeded. A reader that knows only
`status` and `content` is still correct, because `content` is unchanged by the
presence of either field.

`exit_code` is present only where the tool ran a process. Zero is a valid exit
code, so its absence and a value of `0` are different facts.

#### `options_change`

```jsonc
{"key":"permission_mode","from":"ask","to":"auto","source":"client"}
```

`key` ∈ `model | compaction_enabled | permission_mode`.

#### `state_change`

```jsonc
{"from":"idle","to":"running","reason":"prompt"}
```

States ∈ `idle | running | blocked | paused | completed | error`. `reason` is free
text (`prompt`, `turn_complete`, `stop_gate`, `budget`, `interrupted`,
`daemon_restart`, `stopped`, an error string). The first `state_change` in a log MUST
be `from: null, to: idle` or is implied: a log with no `state_change` is `idle`.

#### `compaction`

```jsonc
{"mode":"clear_results","range_start":"01J...A","range_end":"01J...K"}
{"mode":"summarize","range_start":"01J...A","range_end":"01J...Q","summary":"..."}
```

`mode` ∈ `clear_results | summarize`. `summary` is REQUIRED for `summarize`. The range
is inclusive and refers to event ids in this log. Events in the range remain in the
log and MUST still be rendered by clients; only request assembly (§6) treats them
differently.

#### `context`

```jsonc
{"source":"module:skills","slot":"prefix","content":"# Skills\n- verifying-work: ..."}
```

`slot` ∈ `prefix | suffix`. A `prefix` block is part of the request prefix from the
point it is appended until the next `summarize` compaction. A `suffix` block applies
to exactly one request: the next one assembled after it.

#### `tasks` — full snapshot

```jsonc
{"revision":4,"source":"model","tasks":[
  {"id":"t1","title":"Add cursor validation","status":"done",
   "done_when":"go test ./protocol/ passes","check":"go test ./protocol/",
   "blocked_by":[],"note":"","evidence":"01J...X"}]}
```

`status` ∈ `pending | in_progress | blocked | done | failed | cancelled`. `revision`
increments by one per snapshot. `evidence`, when present, is the id of a `check` event
with `status: pass` and a matching `task_id`; it is set by the daemon, never by the
writer of the snapshot. `blocked_by` MAY be empty and MUST be present.

#### `goal`

```jsonc
{"condition":"all tests in ./protocol pass and git status is clean","state":"set","source":"client"}
{"condition":"...","state":"unmet","reason":"git status shows 2 modified files","source":"module:verify"}
```

`state` ∈ `set | met | unmet | impossible | cleared`. The current goal is the last
`goal` event; a goal whose last state is `met`, `impossible`, or `cleared` is not
active.

#### `check`

```jsonc
{"name":"task:t1","kind":"command","task_id":"t1","status":"pass",
 "summary":"exit 0","output":"ok  \t./protocol\t0.4s"}
```

`kind` ∈ `command | judge | module`. `status` ∈ `pass | fail | error`. `output` MAY
be truncated by the writer; `summary` MUST be short.

#### `stop_veto`

```jsonc
{"module":"verify","reason":"2 tasks are still open: t3 (in_progress), t4 (pending)"}
```

Outstanding vetoes are the `stop_veto` events appended after the most recent
assistant `message`. They are rendered into the next request per §6.4.

#### `budget`

```jsonc
{"max_turns":60,"max_tokens":0,"max_usd":0,"source":"client"}
```

`0` means unlimited. The active budget is the last `budget` event.

#### `notice`

```jsonc
{"source":"daemon","level":"warn","message":"session interrupted by daemon restart"}
```

`level` ∈ `info | warn | error`.

#### `report` — at every terminal or paused transition

```jsonc
{"exit_status":"completed",
 "goal":{"condition":"...","state":"met","reason":"..."},
 "tasks":{"total":6,"done":6,"open":[]},
 "checks":[{"name":"verify.command","status":"pass","summary":"ok  ./... 2.1s"}],
 "files_touched":["protocol/cursor.go"],"commits":["a1b2c3d"],"tree_dirty":false}
```

`exit_status` ∈ `completed | blocked | paused | error` and equals the `to` of the
`state_change` appended immediately before it. `goal` is omitted when no goal was ever
set. Readers MUST tolerate `files_touched`, `commits`, and `tree_dirty` being absent
(the `report` module was disabled).

### 3.2 Validation

An event is invalid, and a reader MUST reject the log, if: `id` is not a ULID;
`parent_id` does not equal the previous event's `id` (or is non-null on the first
event); `type` is unknown; or `data` violates the type's schema in
`schema/event.json`. Readers MUST NOT reject an event for unknown extra `data`
fields.

## 4. Cursors and sync

A cursor is `{"session_id": string, "last_event_id": string | null}`.

`events_after(log, cursor)`:

- `last_event_id: null` → every event, in order.
- `last_event_id` = an id in the log → every event after it, in order (possibly none).
- `last_event_id` not in the log → error `nabu_cursor_unknown`. The client MUST reset
  its cursor to `null` and refetch. (Returning an empty set here was considered and
  rejected: a client with a corrupt cursor would silently stop syncing forever.)

`synced` is `true` when the client's `last_event_id` equals the log's last id after
applying the returned events. A client rendering a session with `synced: false`
MUST show it as partially synced.

## 5. Session state projection

A reader derives the session's current state by a single left-to-right pass over the
log. The projection is normative; both implementations MUST produce the same result
for the same log (vectors under `vectors/projection/`).

| Field | Derived from |
|---|---|
| `state` | `to` of the last `state_change`; `idle` if none |
| `options` | `session.options`, then each `options_change` applied in order |
| `goal` | the last `goal` event, or none |
| `tasks` | the `tasks` array of the last `tasks` event, or empty |
| `budget` | the last `budget` event, or unlimited |
| `turns` | count of assistant `message` events |
| `usage` | sum of assistant `message.usage` fields |
| `last_event_id` | id of the last event |
| `compacted_through` | `range_end` of the last `summarize` compaction, or none |

`thinking` events are ignored by the projection entirely: they carry no state,
and a log that differs only in its thinking projects identically.

## 6. Request assembly

The model request is a pure function of the log and the static system prompt. This
section is normative for the daemon and is vector-tested as a list of segments
(`vectors/assembly/`); clients do not assemble requests.

Order:

1. Static system prompt (not part of the log).
2. `context` blocks with `slot: prefix`, in log order, appended after the last
   `summarize` compaction (or since the start if none). A `summarize` compaction ends
   the life of earlier prefix blocks; modules re-inject what should persist.
3. If a `summarize` compaction exists: its `summary`, followed by the **Current state
   block** (§6.3).
4. `message`, `tool_call`, and `tool_result` events after the last `summarize`
   compaction, in log order, with §6.2 applied.
5. `context` blocks with `slot: suffix` appended after the last assistant `message`.
6. Outstanding `stop_veto`s rendered per §6.4, as one user-role message.

Events of type `options_change`, `state_change`, `tasks`, `goal`, `check`, `budget`,
`notice`, `report`, and `thinking` are never sent to the model directly; their effect
reaches the model through tool results (tasks) and the Current state block. Thinking
has no effect to reach it: replaying a model's own reasoning back to it is not
something every provider accepts, and nothing here depends on it.

### 6.2 Tool-result clearing

For each `compaction` with `mode: clear_results`, every `tool_result` whose id lies in
its range is replaced in the request by the stub `[result cleared: <tool>, <n> bytes]`.
The `tool_call` stays. Ranges from multiple clearings accumulate.

### 6.3 Current state block (normative text)

```
## Current state
Goal: <condition> (<state>)        ← or:  Goal: none   (no goal event, or last state is cleared)
Tasks (<done>/<total> done):        ← omitted entirely when there are no tasks
- [x] <id> <title>                  ← done
- [>] <id> <title>                  ← in_progress
- [ ] <id> <title>                  ← pending
- [!] <id> <title> — blocked: <note>
- [-] <id> <title> — <failed|cancelled>
Budget: turn <turns> of <max_turns> ← or:  Budget: turn <turns> (unlimited)
```

Lines are joined with `\n`, no trailing newline.

### 6.4 Veto message (normative text)

```
Before stopping, the following must be addressed:
- (<module>) <reason>
- (<module>) <reason>
```

Rendered as a single `user` message. It is never appended to the log as a `message`;
it exists only in the assembled request.

## 7. JSON-RPC methods

All client → daemon methods take `params` as an object. `session_id` is required
wherever shown.

### 7.1 `nabu.hello` — §1.1.

### 7.2 `nabu.session.list {archived?}` → `{sessions: [SessionSummary]}`

`SessionSummary = {session_id, workspace, workspace_key, state, event_count,
created_at, updated_at, goal?: {condition, state}, tasks?: {total, done}, last_prompt?,
archived?}`.

`last_prompt` is the content of the session's most recent `user` `message`, trimmed and
cut to at most 200 characters; absent when nothing has been asked. Sessions in one
workspace are otherwise hard to tell apart in a list.

Lists the sessions in use. With `archived: true` it lists the archive instead (§7.19),
and each summary carries `archived: true`. A client that mirrors sessions SHOULD drop
any it holds that a full listing leaves out: they were archived.

### 7.3 `nabu.session.create {workspace, options?}` → `{session_id, event}`

`options` defaults: model from config, `compaction_enabled: true`,
`permission_mode: "ask"`. The returned `event` is the `session` event.

### 7.4 `nabu.session.send_prompt {session_id, content, client_id?}` → `{event_id}`

Appends a `user` `message` immediately, in every state except `completed`, `error`,
and `paused` (those return `nabu_invalid_transition`; use `resume` for `paused`). If
the session is `idle` or `blocked`, the loop starts. If it is `running`, the message is
picked up at the next request assembly.

While a summary is being written (§7.15, or the automatic pass in §6), the message is
appended once the summary lands rather than at once, and the call returns then. A
message appended in between would follow the summarised range and precede the summary,
so request assembly would keep it out of every later request.

`client_id` is optional and makes the call **idempotent**, which is what a client with
an offline outbox (§5) needs: a prompt retried after a dropped connection must not be
appended twice. A `client_id` this session has already seen returns the `event_id` of
the message it produced the first time, in any state, without appending anything and
without starting the loop. The id is recorded on the `message` event, so the log is its
own deduplication table and a retry is still recognised after a daemon restart.

Omitting `client_id` deduplicates nothing: a person typing the same thing twice means
it twice.

### 7.5 `nabu.session.events_after {session_id, last_event_id}` → `{events, synced}` — §4.

### 7.6 `nabu.session.subscribe {session_id}` → `{subscribed: true}`

Thereafter the daemon sends notifications (no `id`):

- `nabu.session.event {session_id, event}` for every appended event.
- `nabu.session.delta {session_id, turn_id, text}` while an assistant message
  streams. Deltas are ephemeral: never logged, never replayed. The final `message`
  event carries the full text and supersedes every delta of that `turn_id`.
- `nabu.session.thinking {session_id, turn_id, text}` while the model reasons,
  for providers that report it. Ephemeral in the same way, and superseded by the
  `thinking` event (§3.1), which is what persists.

`nabu.session.unsubscribe {session_id}` → `{subscribed: false}`.

One client never holds up another. A connection that stops reading — a phone frozen in
the background keeps its socket open but takes nothing — is **closed** once it falls
too far behind or a write to it stalls, rather than delaying what the daemon sends to
anyone else. Nothing is lost by this: a client reconnects and resumes from its cursor
with §7.5.

### 7.7 `nabu.session.interrupt {session_id}` → `{}`

Cancels the in-flight turn. The daemon appends the partial assistant `message` with
`interrupted: true`, then `state_change → idle (interrupted)`. No-op when not
`running`.

### 7.8 `nabu.session.stop {session_id}` → `{}`

Ends the session: `state_change → completed (stopped)` plus a `report`. Allowed from
any non-terminal state.

### 7.9 `nabu.session.resume {session_id, budget?}` → `{}`

Only from `paused`; otherwise `nabu_invalid_transition`. Appends a `budget` event if
one is given, then `state_change → running (resumed)`.

### 7.10 `nabu.session.state {session_id}` → the §5 projection.

### 7.11 `nabu.session.set_goal {session_id, condition}` → `{event_id}`

Appends `goal {state: set, source: client}`. If the session is `idle`, starts the loop
with the condition as the directive. Replaces any active goal.

### 7.12 `nabu.session.clear_goal {session_id}` → `{event_id}`

Appends `goal {state: cleared}`; `nabu_invalid_transition` if no goal is active.

### 7.13 `nabu.session.set_option {session_id, key, value}` → `{event_id}`

Appends `options_change`. `key` ∈ `model | compaction_enabled | permission_mode`.

### 7.14 `nabu.session.update_tasks {session_id, tasks}` → `{event_id}`

Appends a `tasks` snapshot with `source: client`. Same shape as the tool's argument.

### 7.15 `nabu.session.compact {session_id}` → `{event_id, mode}`

Summarises the session's history now, rather than waiting for it to cross the automatic
threshold. Appends a `compaction` event and returns its id.

`mode` is `summarize` normally. It is `clear_results` when the summariser itself failed:
the daemon falls back to stubbing old tool results rather than leaving the context
untouched, and says which happened instead of reporting the one that was asked for.

**Refused while the session is `running`**, with `nabu_invalid_transition`. Compaction
rewrites what the next request is assembled from, and doing that under a turn already in
flight would change the ground beneath it. Interrupt first (§7.7), then compact. A
`completed` or `error` session is refused for the same reason it cannot be prompted.

**Refused when there is too little history to summarise**, with `nabu_invalid_params`.
The automatic pass treats that as "not yet" and says nothing, which is right for something
that runs on its own; a client that asked deserves an answer rather than a success that
did nothing.

**Allowed when `compaction_enabled` is false.** That option turns off the *automatic*
pass. Asking explicitly is the owner overriding their own default.

This is a model call over the whole history, so it takes minutes on a local model.
Clients should expect it to be slow and must not time the call out on the assumption
that it is quick. The daemon appends an `info` `notice` when it starts, so every client
can show that one is under way.

It is not tied to the connection that asked for it: a client that drops meanwhile loses
the reply, not the summary. `nabu.session.interrupt` and `nabu.session.stop` end it; the
call then fails with `nabu_invalid_transition` and the history is unchanged, rather than
falling back to `clear_results`.

### 7.16 `nabu.workspace.browse {path?}` → `{path, parent, entries}`

Lists the directories a client may start a session in. `path` absent returns the
configured roots; otherwise it returns the directories inside `path`.

```jsonc
{"path":"C:/Users/kyle/projects","parent":"C:/Users/kyle",
 "entries":[{"name":"nabu","path":"C:/Users/kyle/projects/nabu","is_repo":true}]}
```

`entries` holds directories only, sorted case-insensitively by name. Dotted directories
and build output (`node_modules`, `build`, `target`, `vendor`, `dist`, …) are omitted.
`is_repo` reports a `.git` entry of either kind, so a worktree counts.

`parent` is `null` at a root, so a client knows where climbing stops rather than
learning it from a refusal one level later.

A path outside the configured roots is `nabu_invalid_params`. The roots bound **listing**,
not access: an authenticated client may already create a session at any path with §7.3.
They exist so that a client cannot enumerate the machine for the asking.

Roots come from `daemon.browse_roots`, defaulting to the user's home directory.

### 7.17 `nabu.workspace.create_directory {parent, name}` → `{path, parent, entries}`

Creates one directory inside `parent` and returns **the new directory's listing**, in the
same shape as §7.16, so a client can move into what it just made rather than asking again.

`parent` must be inside the configured roots, checked exactly as §7.16 checks it. `name`
is **one directory name, never a path**: separators, `.`, `..`, a leading dot, a leading
or trailing space, the characters `: * ? " < > |`, and anything over 64 characters are all
`nabu_invalid_params`. A name that already exists is `nabu_invalid_params` too — a client
listing the directory has already been shown what is there.

This is the only method by which a client causes the daemon to write to its filesystem. It
is not a new level of access: §7.3 already lets a client start a session at any path, whose
agent can create directories. It is listed separately because the daemon acts here on a
client's word rather than a model's.

### 7.18 Daemon → client requests

Sent as JSON-RPC requests (with `id`) to every subscriber of the session. The first
response wins; later responders receive `nabu_already_resolved`.

A request is also sent to a connection that **subscribes while it is still open**, oldest
first, as part of §7.6. A phone that wakes from the background and resubscribes is shown
the question it slept through; a client may be sent a request it already holds, and
recognises it by `request_id`.

With nobody subscribed, `nabu.rpc.ui.ask` **waits** for someone to subscribe, up to the
timeout below. `nabu.rpc.permission.request` does not: the gated call is refused at once.

`nabu.rpc.permission.request {session_id, request_id, tool, summary, risk}` →
`{verdict: "approve" | "deny", reason?}`. `risk` ∈ `low | medium | high`.

`nabu.rpc.ui.ask {session_id, request_id, question, choices?}` → `{answer}`.

Requests time out after a configurable interval (default 10 minutes) with the
session moving to `blocked`; a later answer is still accepted and resumes it.

### 7.19 `nabu.session.archive {session_id}` → `{}`

Puts a session away: it leaves the list, is not loaded when the daemon starts, and is
no longer mirrored. Its log is kept whole, and an `info` `notice` saying why is appended
before it moves. Every subscription to it ends.

**Refused while the session is `running`**, with `nabu_invalid_transition`: archiving
closes the log under a turn still writing to it. Archiving an archived session does
nothing.

The daemon also archives, with the same notice, any session not `running` whose last
event is older than `daemon.archive_after_days` (default 3; 0 turns this off).

An archived session is not found by any other method until it is restored.

### 7.20 `nabu.session.restore {session_id}` → `{}`

Brings an archived session back, appending an `info` `notice`, which also restarts its
idle clock. An unknown id is `nabu_session_not_found`; restoring a session that is not
archived does nothing.

### 7.21 `nabu.session.stats {session_id}` → `SessionStats`

How much work a session was, derived from its log alone:

```jsonc
{"session_id": "…", "turns": 41, "prompts": 6,
 "tokens": {"input": 1840000, "output": 21000, "cached": 0,
            "peak_context": 118000, "context_window": 256000},
 "per_turn": [{"at": "…", "input": 12000, "output": 300}],
 "tools": [{"tool": "read", "calls": 60, "errors": 2}],
 "compactions": {"summarize": 1, "clear_results": 2},
 "vetoes": 3, "interruptions": 1,
 "started_at": "…", "last_event_at": "…", "working_seconds": 5400,
 "rereads": [{"path": "engine.rs", "reads": 7}]}
```

`turns` counts assistant messages; `prompts` user messages. `tokens.input` is every
request's input summed, so it is what was processed: each request carries the
conversation again. `peak_context` is the largest single request. `per_turn` lists turns
that reported usage, in order. `tools` is ordered by calls. `working_seconds` is time spent
`running`, counting no more than 15 minutes for any silence (a daemon stopped
with the session still marked running is not work). `rereads` lists files read three times or more, most first; it is a signal worth
a look, not a verdict. Arrays are always present, possibly empty.

Not normative in the way §5 is: clients ask for it rather than computing it.

### 7.22 `nabu.usage {days?}` → `{days: [{date, turns, input, output}]}`

Turns and tokens per calendar day in the daemon's time zone, across every session,
archived ones included (§7.19), oldest day first. Every day in the range is present, a quiet
one as zeros. `days` defaults to 14 and is capped at 90.

## 8. Error codes

| Code | Name | Meaning |
|---|---|---|
| -32700 | `parse_error` | Invalid JSON |
| -32600 | `invalid_request` | Not a valid JSON-RPC object |
| -32601 | `method_not_found` | |
| -32602 | `invalid_params` | |
| -32603 | `internal_error` | |
| -32001 | `nabu_session_not_found` | |
| -32002 | `nabu_invalid_transition` | Method not allowed in the session's current state |
| -32003 | `nabu_permission_denied` | Caller may not perform this action |
| -32004 | `nabu_already_resolved` | Another client answered first |
| -32005 | `nabu_cursor_unknown` | `last_event_id` not in the log |
| -32006 | `nabu_protocol_mismatch` | Hello missing or incompatible MAJOR |
| -32007 | `nabu_unauthorized` | Missing or wrong bearer token |
| -32008 | `nabu_workspace_untrusted` | Workspace requires trust before use |

Error objects carry `{code, message, data?}`; `data.name` is the name above.

## 9. Conformance vectors

Each file under `vectors/<family>/` is one case:

```jsonc
{
  "name": "cursor after last event returns nothing",
  "description": "...",
  "input": [ /* events, in log order */ ],
  "operation": {"op": "events_after", "last_event_id": "01J...K"},
  "expect": { /* op-specific, see below */ }
}
```

Operations and their `expect` shapes:

| `op` | `operation` fields | `expect` |
|---|---|---|
| `validate` | — | `{"valid": true}` or `{"valid": false, "error": "<substring of the error>"}` |
| `events_after` | `last_event_id` | `{"event_ids": [...], "synced": bool}` or `{"error": "nabu_cursor_unknown"}` |
| `project` | — | a JSON object; every field present MUST equal the projection's field (fields absent from `expect` are not checked) |
| `render_state` | — | `{"text": "..."}` — exact match of §6.3 |
| `render_vetoes` | — | `{"text": "..."}` — exact match of §6.4, or `{"text": ""}` when none outstanding |
| `assemble` | — | `{"segments": [{"kind": "prefix_context" \| "summary" \| "state" \| "message" \| "tool_call" \| "tool_result" \| "tool_result_cleared" \| "suffix_context" \| "vetoes", "id"?: "<event id>"}]}` |

An implementation MUST run every vector file and fail on any mismatch. Vector data
contains no language-specific constructs.
