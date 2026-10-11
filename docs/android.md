# The Android client

The client lives in `clients/android` and is written in Kotlin and Compose. It shows:
- sessions, and each one's transcript with markdown, the model's thinking and tasks;
- permission prompts and questions, which you answer from the phone;
- runs and goals.

It also has an outbox. A prompt written with no signal is held there, and sent when there
is one.

You build and install it from source. There is no release.

```bash
clients/android/gradlew.sh :app:assembleDebug
```

## Reaching the daemon

The daemon binds to loopback. To reach it from the phone, bind it to an address on a
private network such as Tailscale, and set a token:

```json
{ "daemon": { "bind": "0.0.0.0:8737", "token": "a-long-random-string" } }
```

The daemon refuses a connection from anywhere but loopback unless it carries the token.
It runs shell commands in your repositories, so don't expose the port to the internet.

## Notifications

The daemon tells the phone when something needs you:
- a question is waiting;
- a run or a goal finished, failed or blocked;
- a session stopped on an error;
- a turn longer than a minute finished.

The sessions inside a run or goal never notify on their own. Only the run or goal does. A
notification carries only identifiers and states, and the phone writes the words from
its own copy of the sessions.

Notifications go through Firebase Cloud Messaging, which needs a Firebase project of your
own:

1. In the [Firebase console](https://console.firebase.google.com), create a project, and
   add an Android app with the package name `com.nabu.client`. Download its
   `google-services.json` to `clients/android/app/`. Git ignores that file.
2. In **Project settings → Service accounts**, generate a new private key. Save it as
   `~/.nabu/fcm-service-account.json`, and point the config at it:

   ```json
   { "notify": { "service_account": "fcm-service-account.json" } }
   ```

3. Rebuild and install the app, and restart the daemon. The app asks permission to show
   notifications, and registers itself each time it connects.

`notify.labels` sets which labels notify. The default is `run:done`, `run:failed`,
`goal:done` and `goal:blocked`. `notify.done_after_seconds` sets the shortest turn whose
end notifies, 60 by default. Without `google-services.json` the app still builds and
runs, but never gets notifications.
