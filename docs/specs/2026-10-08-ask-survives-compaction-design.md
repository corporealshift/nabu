# Claude's answers survive a summary

**Date:** 2026-10-08
**Status:** Approved by Kyle in conversation
**Amends:** the `claude` module (`daemon/modules/claude`), and
`2026-10-07-ask-claude-when-stuck-design.md`, whose "it stays there like any tool
result" holds only until the next summary.

## Problem

Session `01M4ETGBNTTHTH98WX9WDEG9N1` (liftoff, run `mission-control-settings-wf4n`,
Qwen3.6-35B-A3B) was asked to fix a failing `verify.sh`. For two hours it rewrote the
test setup: a TestActivity, then a test manifest, then the production manifest. At turn
100 the automatic ask fired, and Claude's answer was exactly right. It said to stop
changing the setup, revert the manifest, and fix each failing test for its own cause:
DataStore file names, a Robolectric window too short for the screen, `"RLR"` drawn as
one node per chip, a masked token field, and the label "Daemon host".

The model followed part of it over the next 40 minutes. Then a summarize compaction ran.
Compaction always summarises up to the newest event, so after it the answer existed
only as the summariser's paraphrase, written by the same small model. That paraphrase
kept "don't touch the setup" and the production change Claude had asked for, but
dropped the per-test causes and the exact calls. Claude's step 8 had said to check the
section heads' order with `onNodeWithText(head).getUnclippedBoundsInRoot().top`. With
only the summary to go on, the model went looking for `boundsInRoot`. It spent the next
half hour searching the web and unzipping Gradle caches for a Compose position API
that does not exist under that name. In the end it dropped the ordering check, and the
check passed without it.

Asking Claude worked. The answer did not survive the session's own housekeeping: the
facts most worth keeping word for word, a method name and a list of per-test causes,
are exactly what a paraphrase loses.

## Decision

The `claude` module implements `module.CompactionHook`. After every summarize
compaction it puts the stretch's Claude answers back, word for word, as one `prefix`
context block.

### What is put back

- **Every successful `claude.ask` result since a person last spoke**, in log order,
  oldest first. "Since a person last spoke" is the same stretch as the automatic ask
  uses (`stretchOf`). Both sources count: the module's automatic asks
  (`source: module:claude`) and the model's own calls. An answer is an answer, and a
  narrow early one may hold a fact a later one does not repeat.
- **Not failed asks** (a result with `status: error`): a timeout or CLI error is not
  direction.
- **Not answers from before the last user message.** The person speaking ends the
  stretch, and their words replace what came before, as they do for the automatic
  ask's count.
- **Nothing when the stretch has no answers.** Most compactions add no block.

### The block

```
## Claude's answers in this task

Claude was asked for help during this task. Its answers are kept here word for word
through summarising. Some steps may already be done: check the files before redoing
one.

### Answer 1, after turn <n>

<answer>

### Answer 2, after turn <n>

<answer>
```

`<n>` is the stretch's assistant-message count at the result, so the model can tell how
old each answer is.

**Cap: 12 KB for the whole block.** Space goes to the newest answer first, so it is
never cut unless it alone is over the cap. Older answers fill what is left, newest to
oldest. An answer that does not fit whole is cut with the module's `cut`. Answers left
with no room are dropped, and a line in their place says how many were.

### Where it lives

Only in `daemon/modules/claude`: a new `AfterCompaction`, and a `BeforeCompaction`
that returns nothing. Nothing changes in `daemon/agent`, `daemon/session` or the
protocol. Re-adding retired prefix blocks is what `AfterCompaction` exists for, and the
clock, skills and memory modules already do it.

`prefix` is the right slot. A prefix block lasts from when it is appended to the next
summarize compaction, when this hook runs again and re-adds it. Between summaries it
stays fixed, as the request prefix must.

### How it fails

- **The log cannot be read:** no block. The session is exactly where it is today.
- **A result's content is not text the module can read:** that answer is skipped, and
  the others are still put back.
- **The hook times out or errors:** the registry already disables the module for that
  session and logs a notice, as for any hook. The answers are still in the log and the
  transcript.

## Rejected

- **Passing the answers to `BeforeCompaction` as facts to preserve verbatim.** The same
  small model writes the summary. Paraphrasing what it was given is exactly what went
  wrong, and a 4 KB answer in a "preserve these facts" list invites it again.
- **A `suffix` block.** It reaches one request, then it is gone.
- **Leaving `claude.ask` calls and results unsummarised in core.** That puts an opinion
  about one tool into `daemon/agent`. Mechanism in core, policy in modules.
- **Asking Claude again after every compaction.** It is slow, it costs money, and the
  answer already exists.
- **Latest answer only.** Simpler, and earlier answers are more likely to be stale.
  Kyle chose every answer in the stretch: a later answer is written without the
  earlier one in its question, so it may not repeat what the earlier one found.
- **Automatic asks only.** The model's own asks are rarer, but no less Claude's
  direction.

## Follow-up, not in this change

The same session had `git checkout -- <file>` refused twice: the guard rates it as
needing approval, and an unattended session has nobody to give it. The model then
reverted a file by rewriting it by hand. Whether reverting a file to HEAD inside a run's
own worktree should need approval is a separate guard question.

## What proves it works

- `daemon/modules/claude`, table-driven, over constructed logs:
  - one answer in the stretch: `AfterCompaction` returns one `prefix` block carrying it
    word for word, with its turn;
  - two answers: both, oldest first;
  - an answer before the last user message is not included; nor is a failed ask;
  - no answers: no block;
  - over the cap: the newest is whole, the older one is cut, and with no room left a
    third is dropped and counted; the block never passes 12 KB;
  - the model's own `claude.ask` and the module's automatic one are both included.
- The full gate.
- Live: the next session that summarises after an ask carries the block in the first
  request after the summary (it shows as a `context` event from `module:claude` right
  after the `compaction`).
