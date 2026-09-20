# nabu P3 — Memory: The Curator Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> This plan specifies contracts and behaviours, not code. Write the failing test first
> from the stated behaviour, then the implementation. If a requirement is ambiguous or
> contradicts the real code, STOP and report rather than guessing.

**Goal:** Let memory fill itself, so a session on a repository starts knowing what the last one learned without anyone having remembered to write it down.

**Architecture:** A curator pass inside the existing `memory` module. At session end and before a compaction it asks the configured model, in a fresh context, whether anything since the last pass is worth keeping. It writes through the host's tool API, so every write is a logged, gateable, reversible `tool_call`.

**Tech Stack:** Go 1.26, standard library only. No new dependencies.

---

## What already exists

The store, the index, BM25 recall and the `memory.recall` / `memory.save` /
`memory.forget` tools are built and merged. This plan adds only the automatic half.
Read `daemon/modules/memory/` before starting; in particular `Module.Tools`,
`Store.Save`, `Store.Forget`, `BuildIndex` and `Module.SessionEnd`.

`daemon/modules/verify/verify.go` already makes a fresh-context model call through
`host.Model().Complete` with a system prompt and one user message. The curator is the
same shape. Read it rather than inventing a parallel pattern.

---

## Decisions settled by this plan

### The curator writes through the tool API, never the store directly

Spec 11.4 requires it and the reason is worth stating: a curator that writes files
directly is an invisible actor. Going through `host.Tools().Call` makes every write a
`tool_call` event with `source: module:memory`, so it appears in the transcript, passes
the same gate a model call passes, and can be undone from the log.

### When a pass happens, and when it is skipped

At `SessionEnd` and at `BeforeCompaction`. A pass costs a model call, so it is skipped
when there is nothing to look at: no user message and no tool call since the last pass.
A session that only asked a question and got an answer writes no memories.

At `SessionEnd` the curator runs **before** the git commit, so its writes land in that
session's commit rather than trailing into the next one.

### What the model sees

Events since the last pass, as a compact transcript: user messages, assistant messages,
tool calls by name with short arguments, and any notice, stop_veto or goal event. Tool
**results** are capped hard: they are the largest thing in a log and rarely the thing
worth remembering.

The window is bounded at **200 events or 24 KB**, whichever comes first, keeping the
most recent. A pass that cannot see everything is worth more than one that is too
expensive to make.

It is also shown the names and descriptions of the memories that already exist, so it
updates a memory rather than adding a second one about the same subject. Spec 11.4
requires that, and the name being the identity is what enforces it.

### What comes back

A JSON array of at most **three** objects, each with `scope`, `name`, `type`,
`description` and `body`. Three is the spec's cap. More than three and the pass is
truncated to the first three rather than rejected: the model found too much, which is
not a reason to keep none of it.

Anything that is not parseable JSON is discarded and logged once. The curator never
retries: a second call doubles the cost of a pass that has already failed once.

### The in-progress memory

At `SessionEnd`, if open tasks remain, the curator writes a `project` memory named
`<workspace-key>-in-progress` recording where work stopped and what is outstanding.
That is the mechanism by which a *new* session on the same repository picks up where
the last one left off.

**When a session ends with no open tasks, that memory is forgotten if it exists.** A
stale in-progress memory is worse than none: it tells the next session to resume work
that is already finished.

### Consolidation

When the index is over its cap, the pass first asks the model to consolidate: merge
memories that say the same thing, and drop ones that no longer hold. At most **three**
merges and three deletions per pass, so a bad judgment cannot empty the store in one
go, and git keeps whatever it does remove.

Consolidation sees names and descriptions only. Bodies are fetched only for the
memories it proposes merging, which keeps the common case cheap.

### Failure is never the session's problem

No model configured, a failed call, an unparseable reply, a refused tool gate: each is
logged and the pass ends. None of them fails the session, blocks a stop, or produces an
error the human has to clear. Memory filling itself is a convenience on top of a
session, not a step in it.

### Configuration

Under `modules.memory`: `curator` (default **on**), `curator_model` (default: the
session's model), `curator_max_per_pass` (default 3). Turning the curator off leaves
the manual `memory.save` tool working exactly as now.

---

## File structure

| Path | Responsibility |
|---|---|
| `daemon/modules/memory/curator.go` | **New.** The pass: deciding it is worth making, building the window, the prompt, parsing the reply, applying writes through the tool API. |
| `daemon/modules/memory/progress.go` | **New.** The `<workspace>-in-progress` memory: writing it when tasks are open, forgetting it when they are not. |
| `daemon/modules/memory/consolidate.go` | **New.** The over-cap pass: proposing merges and deletions, and applying them. |
| `daemon/modules/memory/memory.go` | **Modify.** Config for the curator, the cursor per session, and calling the pass from `SessionEnd` and `BeforeCompaction`. |
| `daemon/modules/memory/*_test.go` | **New.** One test file per unit above. |

---

## Task 1: The window, and deciding a pass is worth making

**Files:** Create `curator.go`, `curator_test.go`. Modify `memory.go`.

**Purpose:** Work out what the curator would look at, and whether to look at all.

**Contract**

Given a session and the id of the last event a pass saw, the module produces the
transcript window for this pass and reports whether a pass is warranted. The cursor
advances only when a pass actually runs.

**Behaviours that must hold:**
- [ ] With no user message and no tool call since the cursor, no pass is warranted
- [ ] A user message since the cursor warrants a pass
- [ ] A tool call since the cursor warrants a pass
- [ ] With no cursor, which is where a daemon restart lands, the whole log is the window
- [ ] The window holds only events after the cursor
- [ ] The window is capped at 200 events and 24 KB, keeping the most recent
- [ ] A tool result is included but truncated, and the truncation is visible in the text
- [ ] The window renders user, assistant, tool_call, notice, stop_veto and goal events
- [ ] Context events are excluded: they are what the module itself injected
- [ ] Two passes over the same session do not see the same events twice

**Verify:** `go test ./daemon/modules/memory/`

**Done when:** A session that only asked a question produces no pass, and a session that did work produces a window holding that work.

**Commit:** `memory: the curator window`

---

## Task 2: The prompt and the reply

**Files:** Modify `curator.go`, `curator_test.go`.

**Purpose:** Ask the question, and understand the answer.

**Reads:** `daemon/modules/verify/verify.go` for how a module makes a fresh-context
model call and what it does when the model is absent or the reply is unusable.

**Contract**

The prompt carries the save criteria, the existing memory names and descriptions, and
the window. The reply is a JSON array of proposed memories, parsed into the same shape
`memory.save` takes.

**Behaviours that must hold:**
- [ ] The prompt contains the save instructions verbatim, the same wording the session prefix uses
- [ ] The prompt lists existing memory names so the model updates rather than duplicates
- [ ] The prompt carries the window
- [ ] A well-formed reply parses into proposals with scope, name, type, description and body
- [ ] A reply wrapped in prose or a code fence still parses
- [ ] A reply that is not JSON yields no proposals and no error
- [ ] A proposal with an unknown type is dropped, not corrected
- [ ] A proposal with an empty name or body is dropped
- [ ] More than three proposals are truncated to three
- [ ] With no model available the pass produces nothing and does not error

**Verify:** `go test ./daemon/modules/memory/`

**Done when:** A scripted model reply becomes a list of proposals, and every malformed shape is dropped rather than written.

**Commit:** `memory: the curator prompt and reply`

---

## Task 3: Applying the pass

**Files:** Modify `curator.go`, `curator_test.go`, `memory.go`.

**Purpose:** Write what the pass decided, visibly, and run it at the right moments.

**Contract**

Each proposal is written by calling the `memory.save` tool through the host's tool API.
The pass runs from `SessionEnd`, before the git commit, and from `BeforeCompaction`.

**Behaviours that must hold:**
- [ ] Each accepted proposal results in one `memory.save` tool call through the host
- [ ] The store is never written directly by the curator
- [ ] A tool call that fails is logged and the remaining proposals still run
- [ ] `SessionEnd` runs the pass before committing, so the writes are in that commit
- [ ] `BeforeCompaction` runs the pass and returns whatever it already returned
- [ ] The cursor advances past the window after a pass, so the next pass sees new events only
- [ ] With the curator disabled in config, no pass runs and no model call is made
- [ ] A session whose workspace has no memories still works: the first pass creates them

**Verify:** `go build ./... && go vet ./... && go test ./...`

**Done when:** A session that learned something ends with that memory on disk and a `tool_call` in its log saying so.

**Commit:** `memory: apply the curator pass`

---

## Task 4: The in-progress memory

**Files:** Create `progress.go`, `progress_test.go`.

**Purpose:** Let a new session pick up where the last one stopped.

**Contract**

At session end, open tasks produce a `project` memory named
`<workspace-key>-in-progress`. No open tasks removes it if it exists.

**Behaviours that must hold:**
- [ ] Open tasks at session end write the in-progress memory
- [ ] Its name is the workspace key followed by `-in-progress`
- [ ] Its body names the outstanding tasks and their status
- [ ] A blocked task's note reaches the body, because that is why it stopped
- [ ] No open tasks and an existing in-progress memory forgets it
- [ ] No open tasks and no such memory does nothing
- [ ] It is written through the tool API like every other curator write
- [ ] It is workspace-scoped, never global
- [ ] Two repositories do not share one, because the key differs

**Verify:** `go test ./daemon/modules/memory/`

**Done when:** A session that stops with work outstanding leaves a memory saying so, and a session that finishes clears it.

**Commit:** `memory: the in-progress memory`

---

## Task 5: Consolidation near the cap

**Files:** Create `consolidate.go`, `consolidate_test.go`.

**Purpose:** Keep the index inside its cap without losing what it knows.

**Contract**

When `BuildIndex` reports the index over its cap, the pass asks the model to propose
merges and deletions, and applies at most three of each through the tool API.

**Behaviours that must hold:**
- [ ] Under the cap, no consolidation is attempted and no model call is made
- [ ] Over the cap, the model is asked with names and descriptions only
- [ ] A merge writes the surviving memory and forgets the ones it absorbed
- [ ] A merge names a target that must already exist, or it is dropped
- [ ] At most three merges and three deletions are applied per pass
- [ ] A deletion of a name that does not exist is dropped, not an error
- [ ] Consolidation cannot remove every memory: an empty result is refused
- [ ] Imported memories are never merged or deleted, because nabu does not own them
- [ ] Every change goes through the tool API, so git records the pass

**Verify:** `go build ./... && go vet ./... && go test ./...`

**Done when:** An over-cap index comes back under it with nothing lost that mattered, and the whole pass is visible in git.

**Commit:** `memory: consolidation near the cap`

---

## Verifying

The project gate, from the repo root:

```
go build ./... && go vet ./... && go test ./...
```

CI runs that plus `gofmt -l .` on ubuntu-latest and windows-latest.

Beyond the gate, this is only done when driven against a live daemon and a real model:
work on a repository until something is learned, end the session, and confirm a new
session on that repository starts with the fact in front of it that nobody typed. That
is the whole point of the milestone and no unit test proves it.

## Risks worth naming

The curator writes to memory without a human in the loop, and a wrong memory is in
front of every future session on that repository. Three things bound that: every write
is a visible `tool_call`, git keeps the history, and `memory.forget` removes one. The
cap of three per pass means a bad pass is small.

## Not in this phase

Automatic recall at `before_request`. Spec 11.3 lists it as later and off until
measured, and nothing here changes that.
