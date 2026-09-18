# Workspace notes — working state that outlives a session and not much longer

**Date:** 2026-09-18
**Author:** Kyle (corporealshift), with Claude
**Status:** Approved, not yet planned

## 1. Purpose

A place for what the agent works out while doing a job: the dead ends, the reason X
has to happen before Y, how far through a refactor it got. Prose, not checkboxes.

This **reverses an exclusion**. The 2026-09-17 spec listed nabu's scratchpad suggestion
and deliberately left it out, on the grounds that the task list and skills already
covered it. They do not. The task list holds *what to do*, each item with a `done_when`
that makes it checkable. Nothing holds *what was learned doing it*, and a summarize
compaction is where that knowledge goes to die: everything the model figured out is
squeezed into one summary written in a single pass under pressure.

Per `CLAUDE.md`, a decision that no longer holds is revisited in a new dated spec
rather than argued with in place. This is that spec.

## 2. Why it is not memory, and not the task list

| | holds | curated by | lives |
|---|---|---|---|
| task list | what to do, each with a `done_when` | model, per step | the session |
| **notes** | **what was worked out doing it** | **model, per note, rewritten whole** | **the workspace, ~14 days** |
| memory | facts the owner stated that the code does not record | curator + explicit save | forever, git-versioned |

`memory.save`'s own instructions say to save what the owner told you and not what the
repository records. Working state is neither. It is also the wrong shape for memory:
memory is meant to be true indefinitely, and "I am halfway through the SimpleFIN sync"
is false within a week.

## 3. Scope: the repository, and why nothing new is invented

`daemon/workspace` already computes a stable identity per spec §11.2: a slug of the
repository directory name plus a short hash of the git common dir, so it is **the same
key across worktrees and subdirectories of one repository**, and a path hash outside
git. `memory` already scopes by it (`~/.nabu/memory/ws/<key>/`).

Notes use the same key. No project concept is invented, because the one that would be
needed already exists.

### The unit of work problem, and how naming solves it

The motivating case is not "a repository" but "a multi-session task inside a repository"
— a feature effort that may run for weeks. There is nothing in the system that names
one, and inventing an identity for it would mean asking the model to decide when an
effort begins and ends, which it would get wrong.

So **notes are named by the model**, many per workspace:

```
notes.write("simplefin-sync-refactor", "...")
notes.write("android-copy-gotchas", "...")
```

An effort gets its own note inside the repository container. While the work continues
the model keeps rewriting that note, which refreshes its clock, so it survives as long
as the work does. When the effort finishes nobody touches it and it ages out. One rule
covers both a long campaign and a scribble, and neither needs an identity the system
cannot supply.

## 4. Expiry

A note expires **14 days after it was last written**, swept at session start.

Rejected: **age since last *touched***, where any session reading the notes resets the
clock. A long effort would never lose its notes mid-flight, but notes for finished work
would stay alive indefinitely as long as the owner kept working in that repository,
which is most of the time. The point is that dead notes go away by themselves.

Rejected: **no clock, the model prunes**. Nothing useful would ever be lost to a sweep,
but it depends on the model reliably tidying up, and the 35B model this runs on will
not.

Fourteen rather than the seven "a few days" suggests, because a multi-session task can
run longer than a week. It is one config key (`modules.notes.expire_days`).

## 5. How notes reach the model

A **suffix** `context` block on every request, holding every live note newest first,
each with its age.

Suffix and not prefix, because the prefix must not change between compactions
(invariant 6) and notes change constantly. A `context` event and not a notification,
because request = f(log) (invariant 3): everything model-visible is in the log first.

Assembly includes only suffix contexts *after the last assistant message*
(`protocol/assemble.go:111`), so a block re-emitted each turn replaces the previous one
rather than accumulating. This is the mechanism `watch` already relies on.

**There is no `notes.read`.** They are already in front of the model; a read tool would
be a way to spend a turn on something it can already see.

### Budget

Notes are rendered in full up to a byte cap (default 4 KiB). Past the cap, the
remaining notes are listed by name and age only, so the model knows they exist and can
choose to rewrite one down to size. Silently dropping them would make a note vanish
with no way to tell.

## 6. Surviving compaction

`BeforeCompaction` returns each note as a preserve string, so a summarize is required
to keep them rather than deciding for itself. This is what the hook is for; today only
memory's curator uses it.

The notes themselves are on disk, so a compaction cannot actually destroy one. The
preserve strings are about the *summary* staying coherent with what the notes say.

## 7. Sharing with the owner

Notes are plain markdown files at `~/.nabu/notes/ws/<key>/<slug>.md`, with frontmatter
carrying the name and the last-written time. Readable, greppable and diffable with no
extra machinery, which is most of what "share them with me" needs.

Plus `nabu notes`, which prints the current workspace's notes, and `nabu notes --all`
for every workspace.

### Non-goals

- **Not git-versioned.** Memory is, because memory is meant to last and a bad curator
  pass should be recoverable. A note that expires in two weeks does not earn a commit
  per edit.
- **No pushing.** The agent does not interrupt the owner with a note. They are there to
  be read.
- Not synced anywhere, not shared between machines.

## 8. Tools

| tool | effect |
|---|---|
| `notes.write` | create or replace a note by name |
| `notes.delete` | remove one |

`notes.write` replaces wholesale, the same semantics `task.update` already uses for the
task list. Replacement forces the model to curate rather than accrete, and bounds the
cost by construction.

## 9. What this does not fix

A note is only as good as the model's discipline in rewriting it. A 35B will let notes
go stale. The age shown beside each note is there so staleness is visible rather than
invisible; it is not a claim that staleness is solved.
