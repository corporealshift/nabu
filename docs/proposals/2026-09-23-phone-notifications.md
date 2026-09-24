# Proposal: notifying the phone

**Status:** proposed, for review. Nothing here is built.
**Raised:** 2026-09-23, issue 108: "notify when the agent is done. notify if it needs an
answer to a question. notify if the agent stops early."
**Settled already:** the architecture design (§16) chose Firebase Cloud Messaging, and
ruled that a payload carries identifiers and status only, never transcript content. This
proposal builds on that decision and does not reopen it.

## 1. Where things stand

Nothing notifies today. `daemon/notify` is a package comment and nothing else, P5 in the
milestone table is empty, and the Android app has no Firebase dependency. A phone learns
anything only while the app is open, and Android freezes the app, and its socket, soon
after it goes to the background.

The session logs on the desktop show what that costs (45 sessions):

| what happened | count |
|---|---|
| `ask` failed with "no client is attached" | 20 of 26 |
| turn ended `idle`, `turn_complete` | 75 |
| turn ended `idle`, `interrupted` | 8 |
| turn ended `error` (provider error) | 4 |
| turn ended `paused` (daemon restart) | 1 |
| turn ended `blocked` (no progress against vetoes) | 1 |

Of the 83 turns that ended `idle`, 44 took under a minute and 20 took over five.

Issue 109 (fixed separately) changes the first row: a question now **waits** for a client
to attach, and is sent to the phone when it subscribes. So a question no longer fails
because the phone is asleep, but the phone still has to be woken to see it. Waking it is
this proposal.

## 2. What notifies

Three kinds, one per phrase in the issue:

| kind | fires when | notification |
|---|---|---|
| `question` | the daemon opens a request that needs a person: `nabu.rpc.ui.ask`, or `nabu.rpc.permission.request` while someone is subscribed | "breezeway is asking you something", high priority |
| `done` | a session goes `running → idle` with reason `turn_complete`, or `→ completed` | "breezeway finished: 5/6 tasks" |
| `stopped` | a session goes `running →` `blocked`, `error` or `paused` | "breezeway stopped: error" |

When a question is answered, from any client, a `resolved` message follows, and the phone
removes the notification. A question buzzing on the phone after it was answered at the desk
is a lie.

An interrupt (`idle`, `interrupted`) notifies nothing: whoever interrupted is already
looking.

**Decision needed (Q1):** is `stopped` what the issue means by "stops early"? The other
reading is "the agent said it was done with tasks still open". The stop gate already turns
that into `blocked` when `verify` objects, so this proposal catches it, but a turn that ends
`idle` because the model gave up and nothing vetoed looks exactly like a finished one in
the log.

## 3. Not buzzing while you watch

75 turn endings in these logs, most of them while Kyle was at the desk. A phone that buzzes
for each is a phone that gets muted. Two filters:

- **On the phone:** a push for a session the app is showing in the foreground is dropped.
  The app already shows it.
- **On the daemon:** `done` is sent only for a turn that ran at least `done_after` (default
  60 s). Under a minute, you were probably waiting for it. `question` and `stopped` are
  always sent: both mean the agent is not working until someone acts.

**Decision needed (Q2):** is a duration threshold the right filter for `done`? The
alternative is "no client other than the phone has sent anything to the session in the
last N minutes": closer to "you walked away", but it needs the daemon to remember which
connection is which device, which it does not do today.

## 4. How

### Daemon

`daemon/notify` is mechanism: a device list and an FCM sender. It holds no opinion about
which events matter.

- **Devices.** New methods `nabu.device.register {token, name}` → `{}` and
  `nabu.device.unregister {token}` → `{}`, stored in `~/.nabu/devices.json`. The app
  registers on every connect and on every token rotation. A token FCM reports as
  `UNREGISTERED` is removed.
- **Sending.** The FCM HTTP v1 API, authenticated with a service-account key at a path
  named in config. The OAuth token is a JWT signed with RS256, which the standard library
  can do, so no Google SDK is pulled into the daemon.
- **Triggers.** Every question already goes through `Handler.ask` in the API layer, which
  can hand it to `notify` directly. State changes need a subscription of their own to each
  open session's event stream in the store, the way the fan-out subscribes, because the
  fan-out exists only while a client is attached, and the point is to notify when none is.
  Sends are asynchronous and never block the loop; a failed send is logged to `daemon.log`
  and not retried.

Config:

```jsonc
{"notify": {"service_account": "~/.nabu/fcm-service-account.json",
            "kinds": ["question", "done", "stopped"],
            "done_after_seconds": 60}}
```

No `service_account`, no notifications, and nothing else changes.

### Payload

Data-only messages, so the app decides what to show, and §16's constraint holds by
construction:

```jsonc
{"kind": "done", "session_id": "01M38…", "state": "idle",
 "tasks_done": "5", "tasks_total": "6", "request_id": ""}
```

The words on the notification come from the phone's own Room mirror: the workspace name is
already there. Transcript text never goes through Google.

### Android

- `firebase-messaging` and the Google Services Gradle plugin. `google-services.json` is
  per-developer and stays out of git; the build works without it and simply cannot notify.
- A `FirebaseMessagingService` that shows the notification, drops it if that session is on
  screen, and cancels it on `resolved`.
- Two channels, so each can be silenced on its own in Android settings: **Questions** (high
  importance) and **Runs** (`done` and `stopped`, default importance).
- `POST_NOTIFICATIONS`, asked for the first time the app connects, since Android 13 needs
  it at runtime.
- Tapping opens the session. For a question, the app reconnects and subscribes, and issue
  109's replay puts the question on screen.

### Protocol

`nabu.device.register` and `nabu.device.unregister` go in `spec.md` §7 with a schema and a
conformance vector each, as CLAUDE.md requires. No event changes.

## 5. Rejected

- **A foreground service holding the socket.** Rejected in §16: it dies when the app is
  force-stopped, costs battery all day, and pins a notification of its own.
- **Polling from WorkManager.** Fifteen minutes at best. Useless for a question.
- **Self-hosted push (ntfy, UnifiedPush).** No Google project, but §16 decided on FCM and
  nothing here changes that. If setting up Firebase turns out to be the blocker, this is the
  fallback to write up as a new spec, not something to slip in here.
- **Answering from the notification.** Choices as action buttons, or an inline reply, are
  where this should end up, but answering means connecting from a background worker, and
  the tap-to-open path covers the need first. **Decision needed (Q3):** in the first version,
  or after?
- **A `notice` event per push.** CLAUDE.md asks for side effects to be logged events. A push
  is derived entirely from the log, like the fan-out to clients, which is not logged either;
  writing an event per push would put the notifier's own traffic into every transcript.
  Sends are logged to `daemon.log`.
- **Putting this in a module.** What counts as worth a notification is policy, but the
  trigger that matters most, a request waiting on a person, lives in the API layer, which
  modules cannot see. A module would need a new hook just to learn what `Handler.ask`
  already knows.

## 6. What it needs from Kyle

1. **Q1–Q3** above.
2. **A Firebase project**, with Cloud Messaging enabled, a service-account key for the
   desktop, and `google-services.json` for the app. Code can't do this part.

## 7. Proving it

- Daemon: a fake FCM endpoint receives exactly one `done` for a turn over the threshold,
  none for one under it, one `question` per open request and one `resolved` when it is
  answered, and a `stopped` for each of `blocked`, `error` and `paused`. No payload
  contains event text: the test checks every string value against the session log.
- Android: unit tests for foreground suppression and channel choice. Then on the phone,
  with the app backgrounded, a real question, a real finished turn, and a real error each
  show up, and the question disappears when it is answered in the TUI.
