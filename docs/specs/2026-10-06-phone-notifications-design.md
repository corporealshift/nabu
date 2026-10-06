# Phone notifications

**Date:** 2026-10-06
**Status:** Approved by Kyle in conversation
**Issue:** 108, "notify when the agent is done. notify if it needs an answer to a
question. notify if the agent stops early"
**Builds on:** architecture design §16 (Firebase Cloud Messaging; payloads carry
identifiers and status only). **Supersedes** the proposal in PR 113
(`docs/proposals/2026-09-23-phone-notifications.md`, never merged). That proposal
predates runs and goals, and would have buzzed for every one of a run's step sessions.

## Problem

Kyle drives nabu from his phone. Runs and goals now work for hours without him, and they
stop for him: a goal that blocks, a run that fails, a question waiting for an answer. The
phone hears about none of it unless the app is open. Android freezes the app, and its
socket, soon after it goes to the background.

## Decision

### What notifies

The rule throughout: **a session with a parent never notifies.** A run's steps and a
goal's runs belong to their root, and the root says what happened. The one exception is a
question, which waits for a person wherever it is asked.

| kind | fires when | the phone shows |
|---|---|---|
| `question` | the daemon opens a daemon-to-client request (`nabu.rpc.ui.ask`, or a permission request sent to subscribers) in any session | "<project> is asking you something", high priority |
| `resolved` | that request is answered, or times out | the question's notification goes away |
| `label` | a session with no parent gains a label listed in `notify.labels`, by default `run:done`, `run:failed`, `goal:done` and `goal:blocked` | "Goal blocked: <title>" |
| `stopped` | a session with no parent goes from `running` to `blocked`, `error` or `paused` | "<project> stopped: error" |
| `done` | a session with no parent goes from `running` to `idle` with reason `turn_complete`, or to `completed`, after a turn of at least `notify.done_after_seconds` (default 60) | "<project> finished: 5/6 tasks" |

The answers to the three questions PR 113 asked:
- "Stops early" means `blocked`, `error` or `paused`.
- `done` is filtered by how long the turn ran. Runs and goals report through their labels
  whatever their length.
- Tapping a notification opens the session. Answering from the notification is later work.

The daemon gives labels no meaning. Which labels notify is a list in config, which is
policy, and the notifier matches strings.

### Payload

Data-only FCM messages, so the phone chooses the words, and §16 holds by construction:

```jsonc
{"kind": "label", "session_id": "01M4…", "label": "goal:blocked",
 "state": "idle", "tasks_done": "2", "tasks_total": "5", "request_id": ""}
```

Every value is an identifier, a state, a label or a count. The phone writes the title
from its own mirror: the session's project and its title (last prompt, or the first line
of its description).

### Daemon

- **`daemon/notify`** is mechanism:
  - **`Devices`**, the registered phones, kept in `<root>/devices.json`.
  - **`FCM`**, a sender for the FCM HTTP v1 API. It authenticates with a service-account
    key: a JWT signed with RS256 using the standard library, exchanged for an access token
    that is cached until it expires. A token FCM reports as `UNREGISTERED` or
    `INVALID_ARGUMENT` is dropped from the devices.
  - **`Notifier`**, which takes every appended event and every request opening and
    closing, applies the table above, and sends. It runs on a goroutine of its own behind
    a buffered channel. Session appends never wait on it: when the buffer is full, the
    event is dropped and logged.
- **Hooks:**
  - The session store takes one observer, called after every successful append, in every
    session, whether or not a client is attached.
  - `Handler.ask` reports a request opening and closing.
- **Protocol:**
  - `nabu.device.register {token, name}` → `{}` upserts a device.
  - `nabu.device.unregister {token}` → `{}` removes one.

  Both go in `spec.md` §7 and the JSON-RPC schema. Neither adds an event, so there is no
  new conformance vector: the vectors cover the log, and the method list is checked
  against the schema by `protocol` tests.
- **Config:**

  ```jsonc
  {"notify": {"service_account": "fcm-service-account.json",
              "labels": ["run:done", "run:failed", "goal:done", "goal:blocked"],
              "done_after_seconds": 60}}
  ```

  A relative path resolves against the nabu root. With no `service_account`, nothing is
  sent; devices still register, so setting the key later needs nothing from the phone.
- Every send and every failure goes to `daemon.log`. A push is not a logged event: it is
  derived entirely from the log, as the fan-out to clients is, and an event per push would
  put the notifier's own traffic into every transcript.

### Android

- **Firebase:**
  - The app depends on `firebase-messaging`.
  - The Google Services plugin is applied only when `app/google-services.json` exists.
    The file is per developer and git-ignored. Without it the app builds and runs, and
    simply never registers.
- **Delivery:**
  - A `FirebaseMessagingService` builds the notification from the payload and the Room
    mirror.
  - Two channels, so each can be silenced on its own in Android settings: **Questions**
    (high importance), and **Runs and sessions** (default importance).
  - A notification for the session that is on screen, with the app in the foreground, is
    dropped. A `resolved` cancels its question.
  - Tapping a notification opens the app on that session.
- **Registration:** on every connect, and when the token rotates, the app sends
  `nabu.device.register` with its token and the phone's model name.
- **Permission:** `POST_NOTIFICATIONS` is asked for once, on Android 13 and later.

### Setup only Kyle can do

1. Create a Firebase project and add an Android app with package `com.nabu.client`.
   Download its `google-services.json` to `clients/android/app/`.
2. In the project settings, under service accounts, generate a private key. Save it as
   `~/.nabu/fcm-service-account.json`, and set `notify.service_account` in
   `~/.nabu/config.json`.
3. Rebuild and install the app, and restart the daemon.

The README gets these steps.

## How it fails

| What | Then |
|---|---|
| No service-account key, or it does not parse | Logged once at start. Nothing is sent, and nothing else changes. |
| FCM or the token endpoint is unreachable | The send is logged and dropped. It is not retried: a late "it finished" is worth little, and the app shows the truth when opened. |
| A device's token is stale | FCM says `UNREGISTERED`, and the device is removed |
| The phone has no `google-services.json` build | It never registers, and the daemon sends to nobody |
| The notifier falls behind | Its buffer fills, and further events are dropped and logged. Appends never block. |

## Rejected

- **Per-session `done` for every session**, as PR 113 had it. A run has a dozen step
  sessions.
- **The runner sending notifications.** It would need the FCM credentials too, and would
  cover only runs and goals, not questions or interactive sessions.
- **The daemon matching `run:` and `goal:` itself.** That is policy, so it is config.
- **Self-hosted push (ntfy, UnifiedPush)**, a foreground service, or polling. §16 chose
  FCM, and Kyle confirmed it.
- **Answering from the notification**, for now.

## What proves it works

- `notify` tests:
  - the rule table against event sequences: a parent never notifies, a short turn does
    not, each stop state does, a listed label does, an unlisted label does not;
  - the FCM sender against a fake token endpoint and a fake FCM, checking the JWT's
    claims and signature, the cached token, the message shape, and that a stale token is
    removed;
  - no payload value appears in the text of any message in the test log.
- The API: register and unregister round-trip through the handler, and a question opening
  and closing reaches the notifier.
- Android unit tests: the wording from a payload and a mirror row, channel choice, and
  dropping the session on screen.
- Live, once Kyle has done the setup: with the app in the background, a question, a
  finished goal and a stopped session each show up on the phone, and the question goes
  away when it is answered in the TUI.
