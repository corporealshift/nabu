# Five gaps — transcript copy, structured tool errors, vcs tools, cross-workspace reads, change notices

**Date:** 2026-09-17
**Author:** Kyle (corporealshift), with Claude
**Status:** Approved, not yet planned

## 1. Purpose

Five independent pieces of work, gathered here because they were decided together.
Four of them come from nabu's own answer when asked what it was missing (session
`01M2HFSR6RFH18BXFJ5R3FS8W3`, 2026-09-17 03:17). The fifth — copying text out of the
Android transcript — comes from the owner.

Each piece ships on its own branch and its own PR. Nothing here is a package deal:
any one of them can land, or not land, without the others. The one ordering
constraint is that **B precedes C**, for the reason given in §4.

nabu's fifth suggestion, a scratchpad for working state distinct from `memory.save`,
is **deliberately excluded**. The task list and skills already cover it, and adding a
third place to put state would make "where does this belong" a question with no good
answer.

## 2. A — Selecting and copying transcript text (Android)

### The problem

There is no way to get text out of the Android client. `clients/android` contains no
`SelectionContainer` and no clipboard code at all. A reply containing a command, a
path, or a diff can be read and not used.

### Two features, not one

**Copy affordances.** A copy control on every fenced code block, and a long-press on
any transcript line that copies that whole message.

**Free selection.** The transcript is wrapped in a `SelectionContainer` so arbitrary
spans can be drag-selected across blocks.

Both are wanted. The affordance is the common case — one gesture, whole block, no
fiddly handles on a phone. Free selection is the escape hatch for the half-sentence
the affordance cannot anticipate.

### What gets copied: source, not render

Copying yields the **original markdown**, not the rendered text. Pasting a table into
a terminal should produce the pipes; pasting a code block should produce exactly what
was in the fence, with no bullet glyphs or column padding that the renderer invented.

This means each `Line` variant in `clients/android/.../ui/Transcript.kt` must be able
to hand back its source text. Every variant already carries it.

### Why this is tractable

`MarkdownText` renders each `Block` as its own `Text` composable inside a `Column`.
Compose's `SelectionContainer` selects across sibling `Text` composables, so free
selection is a wrapper rather than a rewrite of the renderer.

The work that is not free: the gesture conflict between long-press-to-select,
long-press-to-copy, `LazyColumn` scrolling, and the existing `combinedClickable` in
`ui/Permission.kt`. Selection handles and a long-press action want the same gesture,
and the resolution has to be deliberate rather than whichever composable wins.

### Non-goals

- No rich-text or HTML clipboard flavour. Plain text only.
- No "copy conversation" or export-the-session feature. Per block and per message.
- No change to the wire protocol. This piece is client-only.

## 3. B — Structured tool errors

### The problem

`tool_result` today is `{call_id, tool, content, status}` with `status ∈ ok | error`.
Everything about *how* a tool failed is prose inside `content`: `daemon/tools/bash.go`
appends `[exit status 1]` or `[timed out after 2m0s]` and returns a parallel Go error
that the model never sees. A model that wants to branch on "did this time out or exit
non-zero" is reading English.

### The change

Two optional fields on `tool_result`:

- `exit_code` — the process exit status, where the tool ran a process.
- `kind` ∈ `exit | timeout | denied | invalid_args | not_found | io`.

`content` keeps the human-readable output byte for byte. Nothing that reads `content`
today changes behaviour, and both new fields are absent on success.

Per `CLAUDE.md`, changing an event means changing `protocol/spec.md`, the JSON schema
in `protocol/schema/event.json`, and a conformance vector **together**.

### Rejected alternatives

**A nested `error` object** (`{"error": {"kind": ..., "exit_code": ...}}`). Tidier in
isolation, but every existing reader of `status` would have to learn a second place to
look for failure. Flat optional fields extend what is there.

**Replacing `status` with `kind`.** `status` is load-bearing in clients and in the
conformance vectors. A new required field is a breaking change for a gain that two
optional fields deliver without one.

## 4. C — `git` and `gh` tools

### The problem

nabu's words: it is parsing `git diff` and `git log` output as text, and calls that
fragile. Every structured fact it wants — which files changed, what the hunk headers
say, which commit touched a line — arrives as prose it has to re-derive.

### Where it lives

A new module at `daemon/modules/vcs/`, one line in `daemon/modules/all.go`.

**Not in `Builtins`.** `CLAUDE.md` puts mechanism in core and policy in modules, and a
curated set of git subcommands is an opinion about workflow. Being a module also means
config can turn it off, which matters for a workspace that is not a git repository and
for benchmark runs that should not see it.

### Shape

`git` covers the read verbs — status, diff, log, blame, show — returning structured
results rather than text. `gh` covers pr and issue and run, view and list.

Write verbs (commit, `pr create`) pass through the existing guard gate like any other
side effect. They are not privileged because there are no privileged tools
(invariant 5).

### Why B precedes C

If these tools land before the `kind` field exists, they will emit the old
stringly-typed failures and have to be retrofitted the moment B lands. Built after B,
they are born reporting `kind: not_found` when `gh` is not installed and
`kind: exit` when a subcommand fails.

### Non-goals

- Not a general git porcelain. A curated set of verbs, not a passthrough.
- No credential handling. `gh` uses the ambient login or it fails.

## 5. D — Cross-workspace reading

### The problem

A session is locked to one workspace. nabu cannot read nabu's own source while working
in `farthing`, so cross-project reference work is impossible.

### The change

`config.Config` already carries `TrustedWorkspaces`. Extend that into a list of
readable roots. `read`, `glob`, and `grep` gain an optional `workspace` argument, which
must name a configured root; absent, they behave exactly as today.

### The boundary that does not move

**Writes never cross.** `write`, `edit`, and `bash` stay confined to the session
workspace. The guardrail that blocked `rm ~/nabu-permission-test-does-not-exist` in the
owner's permission test keeps its exact current behaviour for everything that mutates.

Read and write are separated here deliberately: reading another repository is a
convenience with a bounded blast radius, and writing to one is a foot-gun with an
unbounded one.

### Non-goals

- No arbitrary filesystem access. Configured roots only, named, never a path escape.
- No cross-workspace sessions. One session, one workspace, extra reads.

## 6. E — Change notifications

### The problem

The owner drives the workspace concurrently from other terminals. nabu has no way to
know whether a file it read three turns ago is still what is on disk, so it is
guessing, or re-reading defensively, or wrong.

### The constraint that shapes the design

Invariant 3: **request = f(log)**. Two daemons with the same modules and the same log
build the same request byte for byte. A watcher that whispers to the model out of band
breaks that, and `CLAUDE.md` is explicit that every model-visible injection is a
`context` event.

So the watcher appends a `context` event listing the paths that changed since the last
turn. Replay stays deterministic because the event is *in* the log. The
non-determinism lives in when the daemon noticed a change, not in what the model saw —
the same category as when a tool result arrived.

### Shape

A module at `daemon/modules/watch/`. It coalesces changes **between** turns rather
than interrupting one: a turn that is mid-flight runs to completion, and the changes
it did not know about appear in the next request.

### Non-goals

- No interrupting a running turn.
- No file contents in the event. Paths and the kind of change; the model re-reads what
  it cares about.
- Not a sync mechanism. It reports what moved; it does not reconcile anything.

## 7. What lands, in what order

| | piece | branch | depends on |
|---|---|---|---|
| A | transcript copy | `android-copy` | — |
| B | structured tool errors | `tool-error-kind` | — |
| C | `git` and `gh` tools | `vcs-tools` | B |
| D | cross-workspace reads | `cross-workspace-read` | — |
| E | change notices | `watch-module` | — |

Every piece carries its own tests and passes the full gate
(`go build ./... && go vet ./... && go test ./...`, plus `gradlew.sh` for A) before its
PR opens.
