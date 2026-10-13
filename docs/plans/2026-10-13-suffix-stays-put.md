# Suffix blocks stay put: plan

Spec: `docs/specs/2026-10-13-suffix-stays-put-design.md`.

Same branch as `2026-10-13-summaries-chain.md` (`context-cache-specs`), after its steps,
in the same PR with a commit per step. Both plans touch `protocol/assemble.go` and the
notes module. Doing them in one branch, in order, means neither is rebased onto the
other.

Each step leaves `gofmt -l .` empty and `go build ./... && go vet ./... && go test ./...`
green. No step changes `protocol/vectors/projection/`.

### 1. Notes render when they were written, not how old they are

- **Changes:**
  - `daemon/modules/notes/notes.go`, `Render(live, maxBytes)`: no `now`. Each entry is
    `### <name> (written 2026-10-13 12:33Z)`, and each line of the not-shown list is
    `- <name> (written ...)`, in UTC to the minute.
  - `humanAge` stays: `rho notes` (`cmd/rho/commands.go:683`) still shows ages to the
    person.
- **Proves it:** a notes test renders the same notes at two different clocks and gets
  identical text; the existing rendering tests are updated to the new form.
- **Depends on:** nothing. On its own it changes only the block's wording, which is still
  sent on every request.

### 2. Suffix blocks stay at their place in the log, and modules send only what is new

One step, because the halves are unsafe apart. The assembly change alone would add a
notes block to every request. The notes change alone would show notes on one request
and then hide them.

- **Changes:**
  - `protocol/assemble.go`: suffix `context` events after the last `summarize` are
    `SegSuffixContext` segments in log order, among messages and tool events. The
    "after the last assistant message" loop goes. Vetoes are unchanged.
  - `daemon/agent/request.go`: a `SegSuffixContext` flushes any pending assistant message
    and becomes a `user` message. Suffix segments next to each other are joined into one
    message with a blank line. Vetoes stay one trailing `user` message.
  - `protocol/spec.md`: §4 `context` (lifetime: until the next `summarize`, at its
    position) and §6.1 (item 5 folded into item 4).
  - `daemon/module/module.go`: the `RequestHook` comment says a suffix block stays in the
    conversation until the next summarize, so return one only when there is something
    new.
  - `daemon/modules/notes/notes.go`, `BeforeRequest`: render the live notes. Read the
    session's log for the last `module:notes` suffix block since the last `summarize`.
    Send nothing when its content equals the rendering; otherwise send it. With no live
    notes and a block already in view, send `## Working notes\nNone left.` once, so the
    model doesn't keep reading a deleted note as live.
  - `daemon/modules/answer/answer.go`, `BeforeRequest`: send the reminder only when the
    log holds no `module:answer` suffix block since the turn's user message.
  - Vectors:
    - `assembly/02-prefix-and-suffix-contexts.json`: the suffix block appears at its log
      position;
    - `assembly/08-suffix-stays-put.json`: a suffix block, a tool round, a second suffix
      block. The segments are the earlier request's segments, then the new ones.
- **Proves it:**
  - `go test ./protocol/` passes the vectors above.
  - `daemon/agent/request_test.go`, `TestEachRequestStartsWithThePreviousOne`: build a
    request, append a tool round and a suffix block, build again. The first request's
    messages are a prefix of the second's, message for message.
  - `daemon/modules/notes`:
    - `TestUnchangedNotesSendNothing`;
    - `TestAWriteADeleteAndAnExpiryEachSendOneBlock`;
    - `TestNotesAreSentAgainAfterASummary`.
  - `daemon/modules/answer`: `TestTheReminderIsSentOncePerQuestionTurn`.
  - `TestANoteSurvivesIntoTheNextRequest` still passes, now because the block stays in
    the conversation.
- **Depends on:** step 1, so that an unchanged set of notes renders the same text.

### 3. Measure it live

- **Changes:** none committed.
- **How:**
  - Check which build the live daemon runs, since an install from this branch replaces
    it. Then build this branch and install it with nothing running: stop the runner,
    stop the daemon, swap `~/go/bin/rho.exe`, and start both.
  - Run one interactive session on Strata in this repo that writes a note early and works
    for at least 40 requests.
  - Compute the tokens read again per call from its log (input minus cached, against
    the previous call), as for `01M4TE3Z`.
- **Proves it:** the median tokens read again per call is under 2,000, from 8,548. The
  figure goes in the PR description. If it isn't, report what the cache did instead,
  and don't merge.
- **Depends on:** steps 1 and 2, the summaries-chain plan being done, and a quiet
  daemon.

## Not doing

- Vetoes stay trailing (spec, Decision).
- No change to skills, loop or watch, which already send only on events.
- No Strata configuration change.
