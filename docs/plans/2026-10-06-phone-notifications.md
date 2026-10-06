# Phone notifications: plan

Spec: `docs/specs/2026-10-06-phone-notifications-design.md`. One branch, `notify`, with one
commit per step. Each step leaves the gate green.

## 1. Devices and the protocol methods

- **Changes:**
  - `daemon/notify/devices.go`: a device list in `<root>/devices.json`;
  - `daemon/api`: `nabu.device.register` and `nabu.device.unregister`;
  - `protocol/errors.go` and `protocol/schema/jsonrpc.json`: the method list;
  - `spec.md` §7.
- **Proves it:** a devices round-trip test, a handler test, and the protocol method-list
  test. Then the gate.

## 2. The FCM sender

- **Changes:** `daemon/notify/fcm.go`: the service-account key, the RS256 JWT, the token
  exchange and cache, the send, and stale-token reporting.
- **Proves it:** tests against httptest token and FCM servers, with a generated RSA key.

## 3. The notifier, hooked up

- **Changes:**
  - `daemon/notify/notifier.go`: the rule table, the goroutine and the buffer;
  - `daemon/session`: the store's append observer;
  - `daemon/api`: the request open and close hooks;
  - `daemon/config`: the `notify` block;
  - `daemon/daemon.go`: wiring it together.
- **Proves it:** rule-table tests, an end-to-end test from a real store append to a fake
  sender, and the no-text check.

## 4. Android

- **Changes:**
  - Gradle: firebase-messaging, and the Google Services plugin when the JSON exists;
    `.gitignore`;
  - the messaging service, the channels, the wording, foreground suppression,
    registration on connect, the permission, and opening from a tap.
- **Proves it:** unit tests; `gradlew.sh :app:testDebugUnitTest :app:assembleDebug`.

## 5. Docs

- **Changes:** the README setup section, the architecture milestone, and closing the
  proposal PR as superseded, which is Kyle's call.
