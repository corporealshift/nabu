# Proposal: page the session catch-up

**Status:** proposed, not scheduled. No decision taken.
**Raised:** 2026-09-16, out of the websocket read-limit fix (PR #24).
**Affects:** `nabu.session.events_after`, every client, the Android milestone.

## The problem

`nabu.session.events_after` returns **every event since the cursor in a single
message**. There is no limit and no paging. A client attaching to a session for the
first time passes no cursor at all, so it asks for the entire log.

That message has to fit inside the websocket read limit. PR #24 raised that limit from
32 KiB to 64 MiB on both sides, which fixed the immediate disconnects, but it moved the
ceiling rather than removing it. The catch-up is still one message, and one message
still has to fit.

## When it bites

Measured against real logs on this machine:

| Measure | Value |
|---|---|
| Largest real session today | 234 KB over 57 events |
| Average event | about 4 KB |
| Largest single tool result | 32 KB, the configured `max_output` cap |
| Current ceiling | 64 MiB |

So the ceiling is reached at roughly **16,000 events** of ordinary work, or about
**2,000 events** if most of them are output-heavy tool calls. Today's longest session is
57 events, so this is not close. It is also not far: a long autonomous run generates
events quickly, and sessions are designed to be resumed rather than thrown away.

Two things make it arrive sooner than the raw numbers suggest.

**A phone is the worst case.** The Android client mirrors the log locally, so its first
attach to an existing session fetches all of it, over mobile data, into memory on a
device with far less of it than a desktop. It is the client most likely to meet the
ceiling and least able to absorb it.

**Raising the limit again is not free.** The limit exists so a buggy or hostile peer
cannot exhaust memory. Every increase trades that protection for headroom, and the
headroom is temporary.

## The protocol may already allow the fix

This is the part worth checking before designing anything.

`events_after` returns `{events, synced}`, and the spec says:

> `synced` is `true` when the client's `last_event_id` equals the log's last id after
> applying the returned events. A client rendering a session with `synced: false` MUST
> show it as partially synced.

A response carrying **a prefix of the pending events with `synced: false`** is therefore
already well-formed and already meaningful: it says exactly "here is some of it, you are
not caught up". Clients are already required to render that state honestly.

If that reading holds, capping the response needs **no change to the wire format, the
JSON schema, or the existing conformance vectors**. What it needs is a daemon-side cap
and clients that call again until `synced` is true.

## A related gap, found while checking the above

The Go TUI **discards `synced`**. In `clients/go-tui/tui.go` the catch-up reads
`events, _, err := c.EventsAfter(...)`, throwing the flag away, so a partially synced
session renders as though it were complete.

The spec says a client in that state MUST show it as partially synced, so this is a
conformance bug today, independent of this proposal. It does no harm yet only because
the daemon never returns `synced: false` except for an unknown cursor. It becomes
visible the moment anything caps the response, which is what Option A does.

Worth fixing on its own; it is a precondition for Option A either way.

## Options

**A. Cap the response, let `synced` carry the rest.** The daemon returns at most N
events or M bytes, whichever comes first, with `synced: false`. Clients loop on the new
cursor until synced. No wire change; the spec gains a sentence documenting the cap.

**B. Explicit paging.** Add a `limit` parameter and a `next_cursor` to the response.
Clearer to read, but it is a real protocol change: spec, schema and a new conformance
vector, and every client updated together.

**C. Stream the catch-up as notifications.** The daemon pushes the backlog as ordinary
event notifications after subscribing, so there is no large response at all. Elegant,
but it blurs the line between replay and live events, and the client can no longer tell
when catch-up finished.

**D. Do nothing until it hurts.** Raise the limit again when someone hits it. Cheapest
today, and the failure mode is a dropped connection with an opaque error, which is
exactly what PR #24 was cleaning up.

## Recommendation

**Option A**, when it is scheduled.

It is the smallest change that removes the ceiling rather than moving it. It needs no
new wire fields, so no client breaks by not knowing about it: a client that ignores
`synced` still works, it just shows an incomplete transcript, which is what `synced`
already obliges it to disclose.

It also makes work the Android milestone needs anyway do double duty. Spec 15 already
requires a partially synced session to render visibly truncated rather than silently
short, so the UI for "you are behind" is being built regardless. Option A makes that
state routine and frequently exercised instead of rare and therefore untested.

Suggested cap: **500 events or 4 MiB**, whichever comes first. Both well inside the
transport limit, both large enough that the common case is still a single round trip.

## What would change

- `daemon/api`: cap what `events_after` returns and set `synced` accordingly.
- `protocol/spec.md` §7.5 and §4: document the cap and that a client must loop.
- `clients/goclient`: fetch until `synced` is true.
- `clients/go-tui`, `cmd/nabu`: no change beyond the client.
- `clients/android`: build the loop in from the start rather than retrofitting.
- New conformance vector for a capped response, even though the shape is unchanged,
  because the behaviour is now specified.

## Cost

Small. A day, most of it in making sure the client loop cannot spin: a cap that returns
zero events while reporting `synced: false` would loop forever, so the daemon must
always make progress or say why it cannot.

## Trigger

There is no need to act now. Worth doing before either of:

- The Android client goes past reading its own mirror, since it is the client that
  meets this first.
- Any real session passes roughly 2,000 events, which is worth checking occasionally
  rather than assuming.

## Prior art in this repo

The websocket client was rebuilt once already because two goroutines shared a socket,
and the read limit was raised because two defaults collided at the same number. Both
were latent for as long as nothing exercised them. This is the same shape: correct
until the numbers grow, and then a disconnect with an unhelpful message.
