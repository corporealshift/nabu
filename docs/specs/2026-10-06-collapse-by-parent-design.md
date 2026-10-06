# Collapsing sessions by parent

**Date:** 2026-10-06
**Status:** Approved by Kyle in conversation
**Issue:** 135, "the session list should be collapsible by parent instead of always showing
all children sessions. archiving a parent should archive all the children too"
**Amends:** `protocol/spec.md` §7.19 and §7.20, and the idle sweep.

## Problem

A run has a home and a session for each step. A goal (`2026-10-06-goals-design.md`)
has runs under it, each with its steps under it. Both the TUI and the phone list every
one of these sessions, so a single goal fills the screen. Putting a run away means
archiving each session by hand, and the idle sweep takes the steps one by one, whenever
each has sat long enough.

## Decision

### Archiving takes the whole family (daemon)

- **`nabu.session.archive`** archives the session and every session descended from it.
  - If any of them is `running`, nothing is archived. The call fails with
    `nabu_invalid_transition` and names the running session.
  - Each descendant gets the usual `info` notice: `archived with <parent id>: <why>`.
- **`nabu.session.restore`** brings back the session and every descendant archived
  with it. A descendant that had been archived on its own earlier stays archived: its
  last notice does not name the parent.
- **The idle sweep** looks only at sessions with no listed parent. Their children go with
  them. A family is idle when its newest event, across every member, is older than
  `archive_after_days`, and none of its members is running.

This is the daemon's job rather than each client's. The phone, the TUI and the sweep
would each need it, and a client that forgot would leave orphans in the list.

### Collapsing in the clients

- A session with listed children is **collapsed by default**. Its row shows how many
  sessions are under it. Its status, such as `run: work` or `goal: runs, round 2`,
  already says what is happening.
- **Android:** a chevron on the card expands or collapses it. **TUI picker:** `→` expands
  the selected session and `←` collapses it. On a child, `←` goes to its parent.
- Each level expands on its own. Expanding a goal shows its runs, still collapsed; a run
  shows its steps when it is expanded too.
- What is expanded is kept for as long as the list is, on that device. It is not saved
  anywhere and not shared.

## Rejected

- **Cascading in each client.** It is three places to get it right, and the sweep is not
  a client.
- **Expanding active homes automatically.** The list would move under your thumb as runs
  change step.
- **Saving what is expanded on the daemon.** It is a view preference, and each device has
  its own.

## What proves it works

- Daemon:
  - archiving a home archives its children and grandchildren, each with its notice;
  - a running grandchild refuses the whole archive and is named;
  - restore brings back only the descendants archived with the parent;
  - the sweep archives a family only when every member is idle.
- TUI: tests that the picker hides children until `→`, shows the count, and that `←`
  collapses.
- Android: unit tests for the collapsed list order and counts, then a look on the phone.
