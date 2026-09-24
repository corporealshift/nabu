# Stopping sooner

**Date:** 2026-09-24
**Author:** Kyle (corporealshift), with Claude
**Status:** Approved 2026-09-24. Builds on `2026-09-23-loop-module-design.md`.

## 1. The problem

Session `01M39RT5…` (nabu, 2026-09-24). Kyle asked "Can you create a PR for this work now?"
about Android work that was already on `main`. The premise was wrong, and the model found
that out one minute in (event 153):

> The work is on main, clean, all 8 tasks complete. I need to create a PR.

It treated that as an obstacle rather than something to report. For fifteen minutes it:
- planned to "reset main back, branch from the Android code's root, squash it into one
  commit, and open the PR", in 12 turns, without doing it;
- ran `git log … | grep 2bc77dd` about 15 times;
- created and deleted `android-client-m4` six times;
- ran `git update-ref` on the branch it had checked out, which left 355 staged "changes".

Then its last reply broke down. The thinking repeated one paragraph 88 times, until
llama-server's reasoning budget cut in. The reply repeated it 265 more times (49,903
characters, 7.5 minutes), until the 16,384-token cap stopped it.

The day before, the same kind of request did real damage. In breezeway session
`01M360WGNV…`, after "ok. create the pr", the model ran:

```
git checkout main && git reset --hard HEAD~1 && git checkout -B feat/… && git push -f origin feat/…
```

That discarded the session's uncommitted work. It spent the following turns re-reading
`engine.rs` and saying "the session changes were reverted by the `git reset --hard HEAD~1`
I ran".

What failed, in order:
1. **Nothing told the model to stop** when what it found contradicted the request.
2. **Nothing noticed it had stalled.** The loop module only sees tool calls. It sorted the
   repeated `git log` as *watching* and suggested `wait` three times, which was the wrong
   diagnosis: the model was not waiting for anything.
3. **Nothing asked before history was rewritten.** Under `auto`, guard rates every git
   command medium, so `reset --hard`, `push -f`, `branch -D` and `update-ref` ran silently.
4. **Nothing stopped a reply that had degenerated** until the token cap, and the whole
   runaway went into the log, to be sent back to the model on the next turn.

## 2. Decision

Four changes, each where its kind of change lives.

### 2.1 The prompt: say so when the premise is wrong

One rule in `DefaultSystemPrompt`:

> If what you find contradicts the request — the work is already done, what it names does
> not exist, or doing it means undoing other work — stop and tell the user what you found
> before changing anything.

It is aimed at event 153. On a local model a rule is a hope, not a guarantee, which is
why 2.2 and 2.3 exist.

### 2.2 Loop module: a *stalled* case

A fourth case beside identical change, change not landing and watching.

**Stalled:** the model has taken N consecutive turns (`stalled_after`, default 5) in
which it called tools and every result was one already returned since the person's last
message. Nothing new has entered the context, which is the shared cause the loops proposal
named. A turn with no tool calls neither counts nor resets. A message from the person
resets everything, as for the other cases.

- **The notice** is a suffix `context` event with an info `notice` beside it, like the
  other cases. It carries facts, so no two read alike:
  - how many turns have returned nothing new, and over how long;
  - the calls repeated most in that stretch, and what they keep returning;
  - the sentence the model has opened most of those turns with, quoted back.

  It ends: *if the request does not fit what you have found, say so and stop, or `ask`.*
  It never mentions `wait`.
- **Precedence.** On a turn where stalled fires, the watching hint does not. An
  identical-change notice or refusal wins over a stalled notice on the same turn, since it
  is the more specific fact.
- **Escalation.** The session moves to `blocked` through the existing `Halt` path, with its
  reason and warn notice, on whichever comes first:
  - `stalled_halt_after` (default 2) more stalled turns straight after the notice;
  - a second stalled stretch since the person's last message.

  The second rule is there because a stuck model often gets one new result now and then. In
  `01M39RT5…` the stretches were 5 and 6 turns long, with something new between them, so
  the first rule alone would never have blocked it. The phone and the TUI already show a
  blocked session.

Because it looks at results rather than at what changed, the branch churn in `01M39RT5…`
still counts as stalled: `Deleted branch android-client-m4 (was 2bc77dd)` coming back
again is nothing new.

**Also:** `module.ChangesWorkspace` counts commands that move refs as changes:
`git branch -d/-D/-f/-m/-M/-c/-C`, `git update-ref`, and `git checkout -B` /
`git switch -C`. They change history, which is what that function already claims to
cover. The loop and answer modules both benefit.

### 2.3 Guard: rewriting history is high risk

In `classifyCommand`, a `git` segment is rated `TierHigh` when it can destroy work or
history:

| command | why |
|---|---|
| `reset --hard` | discards uncommitted work |
| `push` with `-f`, `--force`, `--force-with-lease`, or a `+refspec` | rewrites a remote |
| `branch -D`, `branch -f` | drops or moves a branch regardless of merge state |
| `checkout -B`, `switch -C` | resets a branch to a new start |
| `update-ref` | moves any ref with no checks |
| `rebase`, `filter-branch`, `filter-repo` | rewrites commits |
| `clean -f` | deletes untracked files |
| `checkout -- <path>`, `restore` without `--staged` | discards working-tree edits |

Under `auto`, high risk asks, so each of these reaches Kyle. A plain `git push` of a branch
stays medium, because opening a PR needs it.

### 2.4 Agent core: stop a reply that repeats itself

The runner watches the reply and the thinking as they stream, each on its own. When the
same span of at least 40 characters has repeated back-to-back `repeat_limit` times
(default 8), it:
1. cancels the provider call;
2. logs the thinking and the reply cut to where the repetition began plus one copy, so the
   runaway never reaches the log and is never sent back to the model;
3. appends a warn notice: how many times the span repeated, how much was dropped, and the
   span itself, quoted;
4. moves the session to `blocked` with reason "the reply repeated itself". No tool call from
   that reply runs.

It blocks rather than retrying. A model that degenerates like this has usually been circling
for a while already, as it had here. The person decides what happens next.

`repeat_limit` is a provider setting beside `max_tokens`, since how a model degenerates
depends on the model. `0` takes the default and a negative value turns the check off.

This is core, not a module, for the same reason the reply cap is. It is a bound on one
call, with no opinion about the work, and modules have no hook into the stream.

## 3. How it fails

- **The prompt rule is ignored.** Then 2.2 catches the stall, and 2.3 stops the damage.
- **Stalled fires on real work.** It only informs until two more turns have returned nothing
  new, and a word from the person resets it. The replay in §4 found none in sessions
  that were working.
- **A guard ask with nobody watching** is denied at once, as every permission request is
  (`api.Handler.Permission`). The model is told and carries on around it, which is the safe
  outcome for a history rewrite.
- **The repetition check trips on legitimate output**, for example eight identical 40-character
  table rows in a row. The session blocks and Kyle resumes it. A provider can raise
  `repeat_limit` or turn the check off. Tool-call arguments are not watched, so a file being
  written is never cut by this.

## 4. What proves it

- **Replay** (`NABU_LOOP_REPLAY`) over every session log. On 2026-09-24, measured by script
  over 3,218 turns: 28 runs of five or more turns returning nothing new, **all in the five
  sessions already known to loop**. Four spot-checked runs were all genuine:
  - the same read of `main.rs` five times;
  - the same failed edit to `db.rs`;
  - the same `ls migrations/`;
  - the re-read after the `reset --hard`.

  Grouped by the person's messages, those runs give 9 stretches with a stalled notice, and 7
  of them escalate to `blocked`. In `01M39RT5…` the notice lands at event 281 (14:04:19) and
  the block at event 351 (14:06:23). The session actually ran until 14:16, with its
  degenerate last reply still to come. The replay test prints every notice and block for
  review, and asserts none in the other sessions.
- **The watching hint is not given** in `01M39RT5…`.
- **The repetition check** is table-tested on:
  - the real 49,903-character reply, which must trip within its first few kilobytes;
  - the real 16,691 characters of thinking, which must also trip;
  - ordinary output that must not trip: test logs, repeated import lines, a markdown table,
    a long unrepeated reply.
- **Guard**: a table test for every row in §2.3, plus the plain `git push` that stays medium.
- **ChangesWorkspace**: table rows for the new ref-moving commands, and for `git branch`
  alone (a listing), which does not change anything.

## 5. Rejected

- **DRY sampling** on llama-server. It penalises any repeated token sequence, and `edit` needs
  the model to copy `old` text verbatim from context. It would degrade every edit to catch
  a rare failure. 2.4 targets the failure itself.
- **Detecting a stall from repeated text alone.** Across the logs, 14% of turns open with
  a sentence already used four times since the person spoke, mostly "The user wants me
  to…", in sessions that were working. Repeated results, not repeated words, separate a
  stall from progress.
- **A second model call that checks the request against the repo before starting.** It is
  slow on a local model, and it would cost every session to help a few.
- **Asking at the start of every task.** That's noise in the common case.
- **Retrying a degenerate reply.** See 2.4.
- **Collapsing repeats when the request is assembled.** That's a spec §6 change, and 2.4
  keeps the repetition out of the log in the first place.
