# Summaries chain: plan

Spec: `docs/specs/2026-10-13-summaries-chain-design.md`.

One branch, `context-cache-specs`, off main, which also carries
`2026-10-13-suffix-stays-put.md`. This plan's steps come first, and it merges as one PR
with a commit per step, so no branch is stacked on an unmerged one.

Each step leaves `gofmt -l .` empty and `go build ./... && go vet ./... && go test ./...`
green. No step changes `protocol/vectors/projection/`, the only vectors the Android app's
tests read, so the app's tests are not part of the gate here.

Times are rendered in UTC to the minute, `2026-10-13 13:19Z`, from the event's own
timestamp or the note's write time, so a rendering never depends on when it runs.

### 1. The person's words in the Current state block

- **Changes:**
  - `protocol/render.go`: `RenderStateBlock(log []Event) string`, which is
    `RenderCurrentState(Project(log))` followed, when the log holds a `user` `message`, by:

    ```
    The person said (<shown> of <total>, oldest first):
    - 2026-10-13 10:50Z: <content>
    - (<n> left out)
    - 2026-10-13 13:19Z: <content>
    ```

    - Every `user` `message`, verbatim; lines after a message's first are indented by
      two spaces.
    - `MaxSaidBytes = 4096`. The first message is always kept. Then the newest are taken,
      newest first, while the section stays within the budget, and rendered in log order.
      `- (<n> left out)` stands where messages were dropped.
  - `protocol/assemble.go:92` and `protocol/vectors.go:105` (`render_state`) call
    `RenderStateBlock` instead of `RenderCurrentState(Project(...))`.
  - `protocol/spec.md` §6.3: the section's normative text, budget and selection rule.
  - Vectors:
    - `rendering/06-state-block-person-said.json`: two messages, both shown;
    - `rendering/07-state-block-person-said-over-budget.json`: a long first message, then
      enough that some are left out; the first and the newest are shown;
    - expected text updated in `rendering/02-state-block-full.json`,
      `assembly/04-summarize-then-state-block.json` and
      `assembly/06-state-block-is-as-of-the-summary.json`, which hold user messages.
- **Proves it:** `go test ./protocol/` runs the new vectors and passes. In
  `assembly/06`, a user message after the summarize is not in the block.
- **Depends on:** nothing.

### 2. The summarizer is given the previous summary

- **Changes:**
  - `daemon/agent/compaction.go`, `summarizeLocked`: find the last `compaction` with
    `mode: summarize` in the log. When there is one, the transcript starts with
    `<earlier-summary>\n<summary>\n</earlier-summary>\n\n` ahead of `<conversation>`.
  - `summarizePrompt`: "An earlier summary, when given, is replaced by yours: carry
    forward everything in it that still holds, and drop what is finished."
- **Proves it:** `daemon/agent/compaction_test.go`:
  - `TestASecondSummaryIsGivenTheFirst`: two summarizes; the fake provider's second
    request holds the first summary's text, ahead of the conversation since it;
  - `TestAFirstSummaryHasNoEarlierOne`: no `<earlier-summary>` on the first.
- **Depends on:** nothing. It is independent of step 1 and ordered after it only so that
  the state block lands first.

### 3. Notes carry their time, and the person wins

- **Changes:**
  - `daemon/modules/notes/notes.go`, `BeforeCompaction`: `Working note "<name>" (written
    <time>): <body>`.
  - `daemon/agent/compaction.go`, `summarizePrompt`: "Working notes among the facts to
    preserve are the agent's own, written at the time shown. Keep them verbatim. Where a
    message from the person later than a note contradicts it, say the note is out of
    date: the person's message wins. The same holds for anything else the agent wrote."
- **Proves it:** `TestCompactionIsToldToPreserveTheNotes` asserts the write time is in
  the preserve string; a `compaction_test.go` assertion that the prompt states the
  precedence rule.
- **Depends on:** nothing.

### 4. Replay compaction 3 of `01M4TE3Z`

- **Changes:** none committed. A throwaway test, deleted afterwards, builds the
  summarize request for compaction 3's range of `~/.rho/sessions/01M4TE3Z35KB2ZCJC5EXNHRNTC.jsonl`
  the way step 2 does. It preserves the note `run-claude-exit1-diagnosis` as written at
  12:33Z (from its `notes.write` call in the log), in step 3's form, and sends the
  request to Strata at `localhost:8080`.
- **Proves it:** the summary says Kyle chose parking and that it is still to be built.
  The summary and its token count go in the PR description. If it does not say so, the
  steps above are not done: report it, don't merge.
- **Depends on:** steps 1 to 3, and a quiet daemon, since one summary call on Strata
  takes about 8 minutes.

## Not doing

- Reminding the agent to review its notes when the person writes (spec, Rejected).
- Changing how often or at what size sessions summarize.
- Anything in `2026-10-13-suffix-stays-put-design.md`: its own plan.
