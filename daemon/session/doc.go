// Package session owns the append-only event log: one JSONL file per session under
// ~/.nabu/sessions/, the session index, cursors, and persistence. The daemon is the
// sole writer; every append goes through this package, which stamps id, parent_id and
// timestamp. Reading is always "events after a cursor".
package session
