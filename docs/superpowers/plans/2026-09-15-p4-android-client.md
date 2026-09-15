# nabu P4 — Android Client Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> This plan specifies contracts and behaviours, not code. Write the failing test first
> from the stated behaviour, then the implementation. If a requirement is ambiguous or
> contradicts the real code, STOP and report rather than guessing.

**Goal:** Make the phone a real client: read a run on the sofa, answer a permission prompt, queue a prompt on the underground and have it send itself when signal returns.

**Architecture:** Kotlin, Compose and Room, speaking the same JSON-RPC protocol over a Tailscale connection. A repository owns the socket and writes everything into Room; the UI only ever reads Room. Offline rendering is not a mode, it is what the app always does.

**Tech Stack:** Kotlin 2.1, Compose, Room, OkHttp WebSocket, kotlinx.serialization. Toolchain at `~/android-toolchain` (JDK 21, SDK 35, Gradle 8.11.1), the same one the owner's other Android projects use.

---

## The toolchain, because it is not where anyone would look

Builds run through a wrapper that pins the toolchain, matching `mealemon/gradlew.sh`:

```
JAVA_HOME=C:/Users/corpo/android-toolchain/jdk
ANDROID_HOME=C:/Users/corpo/android-toolchain/sdk
C:/Users/corpo/android-toolchain/gradle/bin/gradle
```

`local.properties` carries `sdk.dir` and is not committed. The proven versions from
`mealemon` are Android Gradle Plugin 8.7.3, Kotlin 2.1.0, KSP 2.1.0-1.0.29, compileSdk
35, minSdk 26. Use them rather than newer ones: this machine is known to build these.

---

## Decisions settled by this plan

### The UI never reads the socket

The repository owns the connection and writes every event into Room. Screens observe
Room. This is the decision the whole app hangs on: offline rendering needs no separate
path, a dropped connection changes nothing about what renders, and the mirror is the
only source of truth the UI has.

### A partially synced session says so, loudly

Spec 4 requires it and spec 15 names it a designed requirement rather than a discovered
one. `events_after` returns `synced`. When it is false, or when the local cursor is not
the log's last event, the transcript renders a **banner at the point of truncation**,
not a footnote: earlier events exist that this device has not fetched.

A session that has never synced shows as unsynced rather than empty. An empty
transcript and a truncated one must never look alike.

### Permission prompts need more friction than a keystroke

Spec 15 requires it. On the TUI a prompt is one key. On a phone, in a pocket, a
mis-tap approves a command against the owner's repositories.

**Low and medium risk:** a button, but never within the default tap target of where the
composer's send button sits, so a double tap cannot approve anything.
**High risk:** the approve control is disabled until the summary has been scrolled to
its end, and approving takes a deliberate long press. Denying is always one tap.

The asymmetry is the point: the safe answer is always easier than the dangerous one.

### The outbox is a table, not a retry queue in memory

A composed prompt is written to Room first and sent second, with a client-generated
ULID as its `client_id`. The daemon deduplicates on that id, so a send that is retried
after a dropped connection cannot produce the prompt twice.

An item stays in the outbox until the daemon returns an `event_id` for it. Pending
items are visible in the composer, because a prompt the user believes was sent and was
not is the failure that matters.

### Tasks are tapped, not typed

A task card taps to done, sent as `update_tasks` with `source: client`. The optimistic
update is written to Room immediately and reconciled when the resulting event arrives,
because a tap that does nothing visible for two seconds reads as broken.

### Connection settings live on the device

Host, port and bearer token are entered once and kept in DataStore. The daemon refuses
non-loopback connections without a token, so the token is not optional here and the
setup screen says so rather than failing later with a connection error.

---

## File structure

| Path | Responsibility |
|---|---|
| `clients/android/settings.gradle.kts`, `build.gradle.kts`, `gradlew.sh` | **New.** Project, pinned plugin versions, toolchain wrapper. |
| `clients/android/app/build.gradle.kts` | **New.** Module config, dependencies. |
| `.../protocol/Events.kt` | **New.** The wire types: event, the data payloads, JSON-RPC envelopes. |
| `.../protocol/Projection.kt` | **New.** The left-to-right projection of spec 5, ported. |
| `.../net/DaemonClient.kt` | **New.** The socket: connect, handshake, call, subscribe, notifications. |
| `.../data/MirrorDb.kt`, `Entities.kt`, `Daos.kt` | **New.** Room: sessions, events, outbox. |
| `.../data/SessionRepository.kt` | **New.** Owns the client, writes Room, exposes flows. |
| `.../ui/*` | **New.** Session list, transcript, task card, composer, permission sheet, setup. |
| `.../app/src/test/**` | **New.** Unit tests for the projection, the outbox and the sync state. |

---

## Task 1: The project builds

**Files:** Create the Gradle files, the manifest, a placeholder activity.

**Purpose:** Prove the toolchain before writing anything that depends on it.

**Behaviours that must hold:**
- [ ] `gradlew.sh :app:assembleDebug` produces an APK
- [ ] The wrapper pins the toolchain rather than relying on PATH
- [ ] `local.properties` is gitignored and the build fails helpfully without it
- [ ] `gradlew.sh :app:testDebugUnitTest` runs and passes with zero tests
- [ ] Plugin and SDK versions match the ones proven on this machine

**Verify:** `cd clients/android && ./gradlew.sh :app:assembleDebug :app:testDebugUnitTest`

**Done when:** An APK exists and the test task runs.

**Commit:** `android: project skeleton`

---

## Task 2: The protocol types and the projection

**Files:** Create `protocol/Events.kt`, `protocol/Projection.kt` and their tests.

**Purpose:** Understand a log without a daemon. This is the part that must agree with
Go exactly, so it is built and tested first, offline.

**Reads:** `protocol/spec.md` §5 for the projection, `protocol/types.go` for the
payload shapes, and `protocol/vectors/projection/` for cases to port.

**Contract**

Events decode from the daemon's JSON. A projection folds a list of events into the
session's current state: state, options, tasks, goal, budget, usage.

**Behaviours that must hold:**
- [ ] An event decodes with its typed payload for message, tool_call, tool_result, state_change, task, goal, budget, notice, stop_veto, report, compaction, context
- [ ] An unknown event type decodes without throwing, keeping its raw data
- [ ] An unknown field inside a known payload is ignored rather than fatal
- [ ] The projection matches the vectors under `protocol/vectors/projection/`
- [ ] A log with no state_change projects as idle
- [ ] The last state_change wins
- [ ] The last budget event wins
- [ ] Tasks reflect the most recent task event
- [ ] Assistant usage accumulates across messages

**Verify:** `./gradlew.sh :app:testDebugUnitTest`

**Done when:** The Go conformance vectors for projection pass against the Kotlin port.

**Commit:** `android: protocol types and projection`

---

## Task 3: Room mirrors the log

**Files:** Create `data/Entities.kt`, `data/Daos.kt`, `data/MirrorDb.kt` and tests.

**Purpose:** The local truth the UI reads.

**Contract**

Three tables: sessions (id, workspace, key, state, cursor, synced), events (id,
session id, ordinal, type, raw json), outbox (client id, session id, content, created,
sent). Events are inserted idempotently by id.

**Behaviours that must hold:**
- [ ] An event inserted twice appears once
- [ ] Events for a session read back in log order
- [ ] A session row records the last event id seen
- [ ] A session row records whether it is synced
- [ ] Deleting a session deletes its events
- [ ] The outbox holds items independently of events
- [ ] Queries expose flows, so a write updates a screen with no refresh

**Verify:** `./gradlew.sh :app:testDebugUnitTest`

**Done when:** A log can be written, re-written and read back in order.

**Commit:** `android: the room mirror`

---

## Task 4: The daemon client

**Files:** Create `net/DaemonClient.kt` and tests.

**Purpose:** Speak the protocol.

**Reads:** `clients/goclient/client.go`. In particular, one coroutine owns the socket:
the Go client had to be rebuilt for exactly this reason, and repeating the mistake on a
phone would be worse, not better.

**Contract**

Connect with a bearer token, complete `nabu.hello`, make calls that suspend until their
response, and deliver notifications to a flow. Reconnect with backoff.

**Behaviours that must hold:**
- [ ] The handshake sends the client name, version and protocol version
- [ ] A refused handshake surfaces as a typed error, not a hang
- [ ] A call suspends until its own response and ignores others
- [ ] Notifications reach the flow while a call is in flight
- [ ] Concurrent calls from different coroutines do not interleave on the wire
- [ ] A dropped connection retries with backoff and does not lose queued sends
- [ ] The token is sent as an Authorization header
- [ ] Cancelling the scope closes the socket

**Verify:** `./gradlew.sh :app:testDebugUnitTest` against a local fake server

**Done when:** A fake daemon can be driven through a handshake, a call and a
notification.

**Commit:** `android: the daemon client`

---

## Task 5: The repository, and honest sync

**Files:** Create `data/SessionRepository.kt` and tests.

**Purpose:** Join the socket to the mirror, and be honest about what is missing.

**Contract**

On connect: list sessions, then for each subscribe and fetch `events_after` the local
cursor, writing events and the `synced` flag into Room. The UI observes Room only.

**Behaviours that must hold:**
- [ ] Subscribing happens before fetching, so an event during catch-up is not lost
- [ ] Events land in Room and update the cursor
- [ ] `synced: false` is recorded and survives going offline
- [ ] An unknown cursor re-fetches the whole log rather than failing
- [ ] A session never seen before is recorded as unsynced, not empty
- [ ] Nothing in the UI path touches the socket

**Verify:** `./gradlew.sh :app:testDebugUnitTest`

**Done when:** A session mirrors, and a partial mirror is marked as partial.

**Commit:** `android: the repository and sync state`

---

## Task 6: Session list and transcript

**Files:** Create the Compose screens and their tests.

**Purpose:** Read a run.

**Behaviours that must hold:**
- [ ] The session list shows every mirrored session with workspace and state
- [ ] It renders from Room with no connection at all
- [ ] The transcript renders messages, tool calls, results, notices and vetoes
- [ ] A partially synced transcript shows a banner at the truncation point
- [ ] A never-synced session is visibly unsynced rather than blank
- [ ] The newest events are at the bottom and it opens there
- [ ] A long tool result is collapsed with a way to expand it

**Verify:** `./gradlew.sh :app:testDebugUnitTest` plus a run on the device

**Done when:** A run can be read on the phone, in aeroplane mode, without lying about
what it has.

**Commit:** `android: session list and transcript`

---

## Task 7: The outbox and composer

**Files:** Create the composer, the outbox worker and tests.

**Behaviours that must hold:**
- [ ] A composed prompt is written to the outbox before any send is attempted
- [ ] Each item carries a client-generated ULID as its client_id
- [ ] Sending while offline leaves the item pending and says so
- [ ] Connectivity returning sends pending items oldest first
- [ ] A retried item does not produce a second message, because the id repeats
- [ ] An item is cleared only when the daemon returns an event_id
- [ ] Pending items are visible in the composer

**Verify:** `./gradlew.sh :app:testDebugUnitTest`

**Done when:** A prompt written in aeroplane mode arrives exactly once on landing.

**Commit:** `android: the outbox`

---

## Task 8: Task card and permission prompts

**Files:** Create the task card and the permission sheet, and tests.

**Behaviours that must hold:**
- [ ] The task card lists tasks with status, and a tap marks one done
- [ ] The update is sent as `update_tasks` with `source: client`
- [ ] The tap updates the card immediately and reconciles on the event
- [ ] A permission request raises a sheet naming the tool and the risk
- [ ] Deny is always a single tap
- [ ] A high-risk approve requires the summary to be scrolled to its end
- [ ] A high-risk approve requires a long press, not a tap
- [ ] No approve control sits where the send button was a moment earlier
- [ ] An unanswered prompt survives backgrounding the app

**Verify:** `./gradlew.sh :app:testDebugUnitTest` plus a run on the device

**Done when:** A dangerous command cannot be approved by a mis-tap.

**Commit:** `android: task card and permission prompts`

---

## Verifying

```
cd clients/android && ./gradlew.sh :app:assembleDebug :app:testDebugUnitTest
```

Unit tests run on the JVM and need no device. CI does not build this: the Android SDK
is not on the runners, and adding it is its own decision.

Beyond the gate, this is only done on the real phone, against the real daemon over
Tailscale: read a live run, answer a permission prompt, queue a prompt in aeroplane
mode and watch it send itself. The owner's device is the only place that proves it, and
the memory about that device applies.

## Not in this phase

FCM notifications are P5. Nothing here should assume them, though the outbox is the
thing P5's "you have pending items" notification will read.
