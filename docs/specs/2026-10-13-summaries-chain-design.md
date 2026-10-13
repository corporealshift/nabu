# Summaries chain, and the person's words survive them

**Date:** 2026-10-13
**Status:** Approved by Kyle in conversation
**Amends:** the summarize call (`daemon/agent/compaction.go`); `protocol/spec.md` §6.3,
the Current state block; the notes module's preserve strings
(`docs/specs/2026-09-18-workspace-notes-design.md` §6).

## Problem

In session `01M4TE3Z` on 2026-10-13, Kyle answered "let's go with your recommendation"
(park a gate that Claude refused because of a rate limit). The work he approved was
never built, and the session reported it as "deliberately out of this PR". Three causes,
largest first:

1. **A summary never sees the summary before it.** The summarizer is given the events
   since the last `summarize` and nothing else (`summarizeLocked`,
   `renderForSummary(log[start:])`). Summary 2 recorded "Kyle chose plain parking".
   Summary 3 never saw it, and Kyle's message came before its range. So its only
   evidence was the agent's own paraphrases, a memory save, and one stale note. It said
   the request was "relayed inside the agent's own `claude.ask` call": it had never seen
   the message itself. Any session that summarizes twice keeps only what was said again
   inside the latest range.
2. **Notes reach the summarizer as undated facts.** The note `run-claude-exit1-diagnosis`
   was written at 12:33, before Kyle's decision at 13:19, and said parking was "Kyle's
   decision, not mine". It went to the summarizer as "The summary MUST preserve these
   facts verbatim", with no time and no rule for a later message that disagrees.
   Summary 3 sided with it: "Do not implement the behavioural half".
3. **The agent never rewrote the note**, and the notes block was the last thing it read
   on every request, after Kyle's message. `2026-10-13-suffix-stays-put-design.md` puts
   the note at the point it was written.

## Decision

- **The summarizer is given the previous summary.** The transcript starts with
  `<earlier-summary>` holding the last `summarize` compaction's summary, then the
  conversation since. The summary prompt says the new summary replaces the earlier one,
  so it carries forward everything in it that still holds and drops what is finished.
  This is the daemon's summarize call; the protocol does not change.
- **The person's words are carried without the summarizer.** The Current state block
  (§6.3) gains a section rendered from the log up to the compaction, as tasks are:

  ```
  The person said (<shown> of <total>, newest last):
  - <timestamp>: <message>
  ```

  - Every `user` `message` event in the log, verbatim, oldest first.
  - At most 4 KB. The first message is always kept, since in a run it is the brief;
    then the newest that fit. A count of those left out is given instead of them. The
    first message alone may exceed the budget, like the newest note in the notes block.
  - Omitted when the log has no user message.
  - Normative text in §6.3 and a rendering vector. The event schema does not change: the
    block is text, rendered from events that already exist.
- **Notes carry their time, and a later message from the person wins.**
  - The notes module's preserve strings become `Working note "<name>" (written
    <timestamp>): <body>`.
  - The summary prompt says: preserved working notes are the agent's own, kept verbatim;
    where a message from the person later than a note contradicts it, the summary says
    the note is out of date, and the person's message wins. The same holds for anything
    the agent wrote.

## Rejected

- **Summarize the whole history every time.** It defeats compaction, and a long session's
  history does not fit the window.
- **Stop preserving notes.** Notes exist to survive compaction; what was missing was a
  time and a rule.
- **Tell the summarizer that the person's messages win, and nothing else.** It cannot
  honour a message it was never shown, which is what happened in summary 3.
- **Remind the agent to review its notes whenever the person writes.** A new opinion in
  the notes module, for a problem these two changes may already fix. Revisit if a stale
  note wins again.

## How it fails

- The summarize request grows by the previous summary: 8 to 13K tokens in `01M4TE3Z`.
  It fits in 92K, since tool results reach the summarizer cut to 1,200 bytes.
- Summaries could grow with each compaction, because each carries the last forward. The
  prompt tells it to drop what is finished; summary sizes are measured over the next few
  long sessions.
- The person's words take up to about 4 KB of the system message after each summarize.
  The block stays the same until the next one, so the prefix cache holds.

## What proves it works

- `daemon/agent`: the summarize request holds the previous summary, ahead of the
  conversation since; a first summarize has none.
- `protocol`: a rendering vector for the Current state block with the person's messages,
  including one where the budget leaves messages out and the first is kept.
- `daemon/modules/notes`: preserve strings carry the write time.
- Replay: compaction 3 of `01M4TE3Z` run again from its log with the new input, on Strata.
  The summary says Kyle chose parking and that it is to be built.
