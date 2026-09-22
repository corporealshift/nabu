# Archiving sessions

**Date:** 2026-09-22
**Author:** Kyle (corporealshift), with Claude
**Status:** Proposed in the PR for issue 56

## 1. The problem

Sessions only accumulate. Thirty-nine on this machine today, 8 MB of logs, and every one is:

- **listed**, in the TUI picker and the phone's list, newest first, with no way to remove any;
- **loaded at daemon start**: `RecoverInterrupted` walks the whole list;
- **mirrored by the phone**, which syncs and subscribes to every listed session on every
  connection.

Issue 56 asks for two things: archive a session by hand, and have sessions archive
themselves after a few days.

## 2. Decision

**Archiving moves the log** from `sessions/<id>.jsonl` to `sessions/archive/<id>.jsonl`.
Nothing in it is rewritten or deleted. Restoring moves it back.

- An `info` `notice` is appended before it moves ("archived: by request", "archived:
  untouched for 3 days") and another on restore. The log still tells the whole story, and
  the restore notice restarts the idle clock so the sweep does not put it straight back.
- `nabu.session.archive` / `nabu.session.restore` (spec §7.19–7.20), and
  `nabu.session.list {archived: true}` for the archive.
- **Refused while running.** Archiving closes the log under a turn still appending to it.
- **The sweep:** at start and hourly, every session not running whose last event is older
  than `daemon.archive_after_days` (default **3**; 0 turns it off) is archived.
- Clients: `nabu archive|restore <id>`; the TUI's `/archive`, and in the picker `a`
  archives and `tab` shows the archive, where `enter` restores and attaches; on the phone a
  long press archives (after confirming), and Archived in the top bar restores. The phone
  drops from its mirror any session a full listing leaves out, which is how it learns about
  the sweep.

## 3. Rejected

- **Delete.** Not asked for, and not undoable. A session is the record of work done.
- **A new event type, or an `archived` option in the projection.** Both change the event
  schema and the vectors for what is a question of where the log is kept, not of what
  happened in it. And an archived session would still be loaded at start to find out that
  it was archived, which is half the cost this is meant to remove.
- **Hiding per client.** The phone and the TUI would disagree about which sessions exist,
  and the daemon would keep loading and the phone keep syncing all of them.
- **An index file of archived ids.** A second source of truth beside the directory, and one
  that can disagree with it.

## 4. Consequences

- An archived session is not found by any other method, events_after included, until it
  is restored. Clients offer restore rather than a read-only view.
- A client attached to a session when it is archived sees the notice, then its
  subscription ends.
