# A gate per workspace

**Date:** 2026-09-25
**Status:** Approved by Kyle in conversation

## Problem

In breezeway session `01M3B65R…`, the model wrote an Android app and opened two pull
requests without compiling it once. A project gate (`verify.command`) makes every stop
that changed something run a command first, and it is the mechanical answer to that. But
it cannot be set for breezeway alone:

- `verify.command` is module config, which the daemon reads once, globally. Set there, it
  would run breezeway's Gradle build in nabu's own sessions too.
- The architecture spec gives each workspace an overlay at `<workspace>/.nabu/config.json`,
  and `config.ApplyOverlay` implements merging one. Nothing calls it: overlays are never
  read.

## Decision

The verify module takes a `commands` object that maps a workspace path to its gate:

```json
{ "modules": { "verify": {
    "command": "",
    "commands": {
      "C:/Users/corpo/Documents/projects/breezeway":
        "SQLX_OFFLINE=true cargo check --workspace --all-targets && if [ -d android ]; then cd android && ./gradlew.sh :app:assembleDebug :app:testDebugUnitTest; fi"
    },
    "command_timeout": 900
} } }
```

A workspace's entry replaces `command` in that workspace, and an empty string turns the
gate off there. Paths are compared absolute, cleaned, with forward slashes and ignoring
case.

## Rejected

- **Wire up the overlays.** Module config is read once at daemon start, and each module
  keeps one configuration. Per-workspace module config means building module
  configuration per session, which is a core change far larger than one per-project
  setting needs. If a second setting turns out to be per-project, that is when to do it.
- **Key by workspace key** (`breezeway-bdbc0f9b`). The key is stable, but a person cannot
  read or write it without looking it up. A path can be.
