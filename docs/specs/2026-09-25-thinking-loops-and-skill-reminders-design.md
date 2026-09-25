# Repeating thinking, and reminding the model of a skill

**Date:** 2026-09-25
**Status:** Approved by Kyle in conversation
**Changes:** §2.4 of [stopping sooner](2026-09-24-stopping-sooner-design.md), for thinking only

Both come from `01M3B65R…`, a breezeway Android session on the local Qwen model.

## 1. Thinking that repeats is trimmed, not stopped

### What happened

The stopping-sooner design watched the thinking and the reply alike, and blocked the
session when either repeated one passage eight times. In `01M3B65R…` it fired twice, both
times in thinking, and blocked the session twice, the second time straight after Kyle's
"continue here". Both were real loops: one paragraph, verbatim, eight times.

The stop cut in about a thousand tokens into the thinking. llama-server runs with
`--reasoning-budget 4096 --reasoning-budget-message "… let's answer now."`, so it would
have closed that thinking itself and made the model answer. The fixture from `01M39RT5…`
ends with exactly that message.

### Decision

The runner still watches both streams. When the **reply** repeats, nothing changes: the
call is cancelled, the reply is cut to one copy, and the session blocks, as §2.4 says.

When the **thinking** repeats, the call goes on. When the turn ends, the thinking is
logged with its repeated run collapsed to one copy and a marker, and anything written
after the run is kept, including the server's budget message. An info notice says how
many copies were dropped and quotes the passage. The turn's tool calls run as usual.

### Why this is safe

Thinking is never sent back to the model: request assembly leaves `thinking` events
out. So a loop in thinking costs time, not context, and cannot feed itself on a later
turn. Where a server sets no reasoning budget, the reply cap (`max_tokens`) still bounds
the call.

### Rejected

- **Retry the turn when thinking loops.** A retry costs a whole prompt pass, and the
  server's budget already gives the model a way out of the loop.
- **Raise the limit for thinking only.** This delays the same block without changing the
  outcome.

## 2. A skill is named when the model touches its files

### What happened

`android-dev` was in the Skills index, and the index says to check it before starting a
task. The model never called `skill.load` in 147 tool calls, while it wrote an Android
app from scratch without compiling it. Across every session on record, skills were loaded
three times. The index is a prefix block, and this model does not act on one. It does act
on suffix blocks: it answered the loop module's notices in the same session.

### Decision

A skill may declare the files it covers, in a nabu-only frontmatter key:

```yaml
paths: **/*.gradle.kts, **/AndroidManifest.xml, **/gradlew*
```

The value is a comma-separated list of globs matched against workspace-relative paths.
`**` matches any number of directories, `*` and `?` stay within one, and matching
ignores case. Claude Code ignores the key.

After each tool call, the skills module looks at the paths it touched: `path` and
`pattern` arguments, and path-like words in a `bash` command. If one matches a skill
that has not been loaded in this session and has not been named yet, the next request
gets one suffix block, source `module:skills`:

> You are working on `android/settings.gradle.kts`, which the `android-dev` skill covers:
> "<description>". Call `skill.load` with `android-dev` and follow it before going further.

Each skill is named at most once per session. Whether a skill was loaded or named is read
from the log: `skill.load` calls, and the module's own earlier blocks. A daemon restart
therefore does not repeat a reminder.

### Why a reminder and not a gate

A gate that refused the first write until the skill was loaded would be stronger. It
would also stop legitimate work whenever a glob is broader than the skill. The reminder
is the smaller step. If the model ignores that too, a gate is the next thing to try.

### Rejected

- **Match skills against the prompt's words.** This is fuzzy, and it fires on
  conversation about a subject rather than work on it.
- **Inline the body of every matching skill.** This costs the whole body on every request
  until the next compaction, whether or not the work needs it.
