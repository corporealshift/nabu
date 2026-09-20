# nabu P3 — Memory: Store and Recall Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> This plan specifies contracts and behaviours, not code. Write the failing test first
> from the stated behaviour, then the implementation. If a requirement is ambiguous or
> contradicts the real code, STOP and report rather than guessing.

**Goal:** Give nabu a memory the model can read and write, so a session on a repository starts already knowing what earlier sessions learned.

**Architecture:** The `memory` module: markdown files under the nabu root, global and per workspace, with a one-line-per-memory index injected as a prefix context block. Recall is BM25 over the corpus, which needs no model and no embeddings. Nothing in the agent, the session or the protocol knows memory exists.

**Tech Stack:** Go 1.26, standard library only. Git is invoked as a subprocess. No new dependencies.

---

## Decisions settled by this plan

### The file format, taken from what already exists

Spec 11.2 says the format is identical to Claude Code auto memory. The real files on
this machine look like this, and the implementation must read them unchanged:

```
---
name: llm-prompts-need-data-not-rules
description: "When an LLM's output is vague, Kyle wants more data given to it, not more restrictions."
metadata:
  node_type: memory
  type: feedback
  modified: 2026-09-14T13:47:36.986Z
---

<the fact>

**Why:** <why it matters>
**How to apply:** <what to do differently>
```

Two things follow from the real files rather than from the spec's summary. `type` sits
**under a `metadata` block**, not at the top level. And `description` may be quoted.
A parser that expects flat top-level keys will read every existing memory as typeless,
so this is worth a test of its own against a fixture copied from the real shape.

Unknown frontmatter keys are preserved on read and written back on save. nabu is not
the only writer of these files.

### The index

`MEMORY.md` is one line per memory, in the shape the real one uses:

```
- [Title](file.md) — one-line hook
```

The title is the memory's `description` trimmed to its first clause; the hook is what
makes a reader open the file. Capped at **200 lines or 25 KB**, whichever comes first.

### What happens at the cap, before consolidation exists

Consolidation is the next plan. Until it lands, a save that would exceed the cap
**still writes the file and still adds the line**, and the module appends a `notice`
saying the index is over its cap and needs consolidating.

Refusing the save was the alternative and is worse: the model would lose a fact it had
decided was worth keeping, and the failure would surface as a tool error in the middle
of unrelated work. An over-long index costs context; a dropped memory costs the thing
memory exists for. The notice makes the debt visible.

### BM25 parameters and tokenisation

`k1 = 1.2`, `b = 0.75`, the standard defaults. The corpus is dozens of short documents,
not a web index, and there is nothing to tune against yet; picking the textbook values
and saying so beats inventing numbers.

Tokenisation is lowercase, split on anything that is not a letter or digit, with no
stemming and no stop-word list. A memory corpus is small enough that a stop word costs
nothing, and stemming would need a dependency or a hand-rolled approximation.

Name, description and body are all searched. The name and description are weighted
higher than the body, because a memory named for its subject should win a query about
that subject even when the body never repeats the phrase.

### Recall scope

`memory.recall {query, scope?}` where `scope` is `global`, `workspace`, or `all`.
**The default is `all`**, searching both and ranking them together. A caller asking
about a subject does not care which store holds the answer, and the result names the
scope of each hit so it is never ambiguous.

Top five, returned in full. Five short files is a few hundred lines at most, which is
affordable once, on demand, in a way that injecting every memory would not be.

### Names, filenames and collisions

A name is kebab-case and becomes `<name>.md`. Anything outside `[a-z0-9-]` is replaced
with a hyphen and runs are collapsed, so a name can never escape its directory.

**A save to an existing name overwrites that file and keeps its index line**, updating
`modified`. Spec 11.4 requires that same-subject files are updated rather than
duplicated, and making the name the identity is what enforces it. The old body is not
merged: the model was asked for the current state of the fact.

### Imported Claude Code memories are read-only

`memory.import_dirs` lists directories read at `Init`. Their memories are searchable
and loadable, and appear in the index marked as imported.

**They are never written.** A save naming an imported memory writes a new file in
nabu's own store and the nabu copy shadows the import from then on. Deleting one
removes nabu's copy and the import reappears. Refusing the save would leave the model
unable to correct anything it imported, and writing into Claude Code's directory would
be nabu editing another tool's state.

### Git

The memory directory is a git repository. `Init` creates one if the directory is not
already inside a work tree.

A commit happens **once per session that wrote**, at `SessionEnd`, not per save: one
commit per fact would bury the history that makes the repository useful. The message
names the session and what changed, for example
`memory: 2 saved, 1 forgotten (session 01ARZ…)`.

**A missing git is not an error.** If git is unavailable or the directory cannot be a
repository, memory works exactly as before and the module logs once at `Init`. Memory
is the product; versioning is a convenience on top of it.

### What the injected instructions say

The prefix block carries the index and a short statement of when to save. The wording
governs the model's behaviour, so it is fixed here rather than left to the implementer:

> Save a memory when the user corrects you, confirms an approach, or tells you
> something about this project you could not have read from the code or the git
> history. Save a pointer to anything outside the repository you had to be told about.
> Do not save architecture, file layout, or anything the repository already records:
> that goes stale and the code does not.

### What memory is not

Spec 11.5 bounds this and the implementation must not exceed it. No embeddings, no
automatic recall on every request (that is listed as later and off until measured), and
no memory of conversation transcripts. Memory holds decisions, not descriptions.

---

## File structure

| Path | Responsibility |
|---|---|
| `daemon/modules/memory/memory.go` | **New.** The module: `Init`, hooks, config, the store roots, the injected block. |
| `daemon/modules/memory/store.go` | **New.** Reading, writing, deleting and listing memory files, the frontmatter parser and writer, name-to-filename, imports. |
| `daemon/modules/memory/index.go` | **New.** Building and writing `MEMORY.md`, and the cap. |
| `daemon/modules/memory/bm25.go` | **New.** Tokenising, scoring, ranking. Pure and directly testable. |
| `daemon/modules/memory/tools.go` | **New.** `memory.recall`, `memory.save`, `memory.forget`. |
| `daemon/modules/memory/git.go` | **New.** Repository init and the per-session commit. |
| `daemon/modules/memory/*_test.go` | **New.** One test file per unit above. |
| `daemon/modules/all.go` | **Modify.** Register the module. |

---

## Task 1: The store and the frontmatter parser

**Files:** Create `store.go`, `store_test.go`.

**Purpose:** Read and write memory files in the format that already exists on disk.

**Reads:** `daemon/modules/skills/skills.go` for how that module parses frontmatter and
walks directories. Memory's parser is a superset: it must handle the nested `metadata`
block and preserve unknown keys.

**Contract**

A `Memory` value carries its name, description, type, modified time, body, the scope it
came from, whether it was imported, and the keys the parser did not recognise. Loading
a directory returns every valid memory in it. Saving writes one file. Forgetting
deletes one.

**Behaviours that must hold:**
- [ ] A file in the real format parses: name, description and type all populated
- [ ] `type` is read from under `metadata`, where the real files put it
- [ ] A quoted description loses its quotes
- [ ] Unknown frontmatter keys survive a load-then-save round trip
- [ ] A file with no frontmatter is skipped, and does not stop the others loading
- [ ] A file with no name is skipped
- [ ] A name is turned into a safe filename: `../escape` cannot write outside the store
- [ ] Saving an existing name overwrites it and updates `modified`
- [ ] Forgetting removes the file; forgetting an unknown name is an error naming it
- [ ] A missing directory loads as empty, not as an error

**Verify:** `go test ./daemon/modules/memory/`

**Done when:** The real files in `~/.claude/projects/.../memory/` would parse, proven by
a fixture copied from their shape.

**Commit:** `memory: store and frontmatter`

---

## Task 2: The index

**Files:** Create `index.go`, `index_test.go`.

**Purpose:** The one-line-per-memory list that goes in front of the model.

**Contract**

Building an index takes the memories of a scope and produces `MEMORY.md` content, one
line per memory in the documented shape. Writing it replaces the file. A cap of 200
lines or 25 KB applies.

**Behaviours that must hold:**
- [ ] One line per memory, each naming its file
- [ ] The line carries enough to decide whether to open the file
- [ ] An empty store produces an empty index, not a file with a header and nothing else
- [ ] At the cap, the index is still written whole and a notice is produced
- [ ] The notice says the index needs consolidating and names the count
- [ ] Rebuilding after a forget removes exactly that line
- [ ] The index is deterministic: same memories, same bytes

**Verify:** `go test ./daemon/modules/memory/`

**Done when:** An index round-trips and the cap is visible rather than silent.

**Commit:** `memory: the index and its cap`

---

## Task 3: BM25

**Files:** Create `bm25.go`, `bm25_test.go`.

**Purpose:** Find the memories a query is about, with no model and no embeddings.

**Contract**

Ranking takes a query and a corpus and returns matches ordered by score. `k1 = 1.2`,
`b = 0.75`. Name and description are weighted above body.

**Behaviours that must hold:**
- [ ] A query matching a memory's name ranks it first
- [ ] A query matching only the body still matches, below an equal name match
- [ ] A term in every document contributes nothing to ranking
- [ ] A rare term outweighs a common one
- [ ] A long document is not favoured merely for being long
- [ ] Matching is case-insensitive
- [ ] Punctuation does not prevent a match: `docker-compose` matches `docker compose`
- [ ] An empty query returns nothing rather than everything
- [ ] A query matching nothing returns an empty result, not an error
- [ ] Ranking is stable: equal scores keep a deterministic order

**Verify:** `go test ./daemon/modules/memory/`

**Done when:** A query about a subject finds the memory named for it.

**Commit:** `memory: bm25 recall`

---

## Task 4: The module, injection and imports

**Files:** Create `memory.go`, `memory_test.go`. Modify `daemon/modules/all.go`.

**Purpose:** Wire the store into a module and put the index in front of the model.

**Reads:** `daemon/modules/skills/skills.go` — the same three hooks, the same shape.

**Contract**

`Init` resolves the global and workspace roots under the nabu root, loads both stores
plus any import directories, and reads config for `import_dirs` and `enabled`.
`SessionStart` returns the index and the save instructions as one prefix block.
`AfterCompaction` returns the same.

**Behaviours that must hold:**
- [ ] `SessionStart` returns a prefix block containing both indexes
- [ ] The block contains the save instructions verbatim as specified above
- [ ] With no memories at all, no block is returned rather than an empty one
- [ ] `AfterCompaction` returns the same content as `SessionStart`
- [ ] The workspace store is keyed by the session's workspace key, so two repositories do not share memory
- [ ] Imported memories appear in the index marked as imported
- [ ] An import directory that does not exist is skipped without error
- [ ] A nabu memory shadows an imported one of the same name
- [ ] The module satisfies `SessionStarter`, `ToolProvider` and `CompactionHook`
- [ ] It is registered in `all.go` and the import-boundary test still passes

**Verify:** `go build ./... && go vet ./... && go test ./...`

**Done when:** A session starts with its memories in front of the model.

**Commit:** `memory: module, injection and imports`

---

## Task 5: The tools

**Files:** Create `tools.go`, `tools_test.go`.

**Purpose:** Let the model read and write memory deliberately.

**Reads:** `daemon/modules/skills/skills.go` for how a module declares a tool and its
JSON schema.

**Contract**

`memory.recall {query, scope?}` returns the top five matches in full, each naming its
scope. `memory.save {scope, name, type, description, body}` writes one. `memory.forget
{name}` deletes one.

**Behaviours that must hold:**
- [ ] `recall` with a query returns matching memories in full
- [ ] `recall` defaults to searching both scopes
- [ ] `recall` with `scope: workspace` does not return global memories
- [ ] `recall` returns at most five
- [ ] `recall` finding nothing says so rather than erroring
- [ ] `save` writes a file that `recall` then finds
- [ ] `save` with an invalid type is an error naming the valid ones
- [ ] `save` with an empty name or body is an error
- [ ] `save` naming an imported memory writes to nabu's store, leaving the import untouched
- [ ] `forget` removes a memory and `recall` no longer finds it
- [ ] `forget` on an unknown name is an error naming it
- [ ] Every tool declares a schema that is valid JSON
- [ ] A save or forget marks the session as having written, for the commit at session end

**Verify:** `go test ./daemon/modules/memory/`

**Done when:** The model can save a fact and a later recall finds it.

**Commit:** `memory: recall, save and forget tools`

---

## Task 6: Git versioning

**Files:** Create `git.go`, `git_test.go`. Modify `memory.go`.

**Purpose:** Make memory changes reviewable and reversible.

**Reads:** `daemon/modules/report/report.go` — how that module runs git and what it does
when git is missing or the directory is not a repository.

**Contract**

`Init` makes the memory directory a repository if it is not already inside one.
`SessionEnd` commits, but only for a session that wrote.

**Behaviours that must hold:**
- [ ] `Init` creates a repository in a fresh memory directory
- [ ] `Init` leaves an existing repository alone
- [ ] A session that wrote produces exactly one commit at `SessionEnd`
- [ ] A session that wrote nothing produces no commit
- [ ] The message names the session and what changed
- [ ] With git unavailable, memory still saves, recalls and forgets, and the module logs once
- [ ] With git unavailable, `SessionEnd` does not error
- [ ] The module satisfies `SessionEnder`

**Verify:** `go build ./... && go vet ./... && go test ./...`

**Done when:** A session that saved a memory leaves one commit behind, and a machine
without git is unaffected.

**Commit:** `memory: git versioning`

---

## Verifying

The project gate, from the repo root:

```
go build ./... && go vet ./... && go test ./...
```

CI runs that plus `gofmt -l .` on ubuntu-latest and windows-latest.

Beyond the gate, this is only done when driven against a live daemon and a real model:
save a memory in one session, start a second on the same workspace, and confirm the
index arrives with the first session's fact in it. That is the whole point of the
milestone, and no unit test proves it.

## Not in this phase

The **curator**: automatic writes at `session_end` and `before_compaction`, the
`<workspace>-in-progress` memory, and consolidation near the cap. That is the second
P3 plan and it is where the model-driven judgment lives.

Also out: automatic recall at `before_request` (spec 11.3 lists it as later and off
until measured), and embeddings of any kind.
