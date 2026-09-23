# Proposal: breaking repetition loops

**Status:** proposed, for review. Nothing here is built.
**Raised:** 2026-09-22, by Kyle: "thinking, tool call, then thinking almost the same thing again."
**Evidence:** every session log on the desktop: 39 sessions, about 1,900 model turns. Scripts in §6.

## 1. What the loops look like

Three sessions hold nearly all of it, all breezeway, all long. The worst stretch, session
`01M30HBK…` events 1739–1865, is the same call 32 times in 13 minutes:

```
THINK   "Kyle is right - I've been stuck in a loop for many iterations now. Let me stop this
         pattern completely and fix all compilation errors at once by rewriting ALL server files"
SAY     "You're right. I've been stuck in a loop for many iterations now. Let me stop this ..."
CALL    write crates/breezeway-server/src/error.rs   (1,259 bytes, identical every time)
RESULT  ok  'wrote 1259 bytes to crates/breezeway-server/src/error.rs'
        ... ×32
```

The model **knows** it is looping. It says so in almost every turn, and then does the same
thing. Knowing is not the missing piece.

Across the sessions the loops come in five shapes:

| shape | example | what it returns each time |
|---|---|---|
| **the same write** | `error.rs` ×32, `lib.rs` ×15, `mod.rs` ×5 | `wrote N bytes`: identical |
| **the same failing build** | `cargo build … \| grep error` ×4–7 with edits between | the same error |
| **the same failed edit** | an `edit` whose `old` text was not there, ×4 | `old text not found` |
| **the same read** | `routes/auth.rs` ×8, `domain.rs` read 42× in one session | the same content |
| **the same plan** | `task.update` with the same list ×3 | the same list, **with new task ids** |

What they share: **the call returns exactly what it returned last time.** Nothing new enters
the context, so the next turn is built from the same material as the last, and the model
produces the same thing. 253 tool calls in these logs returned the same result as a call a
few turns earlier.

## 2. What breaks a loop, when one breaks

**New information, not insight.** The 32-write loop ended when the model finally made no tool
call. The verify module vetoed the stop ("these tasks are not finished"), the model ran
`cargo build`, and the build output was *different*: a real error in another file. It fixed it
within two turns:

```
1883 CALL   cargo build --package breezeway-server
1884 RESULT error[E0433]: cannot find module or crate `breezeway_server`
1889 THINK  "The write didn't take effect because I was stuck in a loop writing error.rs
            instead of auth.rs. Let me fix all the files properly now."
1891 CALL   sed -i 's/use breezeway_server::/use crate::/g' …      ← progress
```

So the lever is to **put something new in front of the model** as soon as a call repeats
with the same result. Asking it to stop does nothing: it has been saying so itself for 12 turns.

## 3. What makes loops likely

**How full the context is.** Repeated call-and-result, per 100 turns, by how full the
context was at the time:

| context used | turns | repeats | per 100 turns |
|---|---|---|---|
| 0–29% | 518 | 10 | ~2 |
| 30–49% | 371 | 26 | ~7 |
| 50–59% | 311 | 50 | 16 |
| 60–69% | 372 | 90 | **24** |
| 70–79% | 304 | 67 | 22 |
| 80–89% | 52 | 10 | 19 |

Roughly ten times the rate past half full. **Confounded:** the long sessions are also the
hard ones (a build that will not compile), so some of this is the task, not the context.
Still, it says where to look.

**The model's own words, copied forward.** Nabu sends the model's visible text back in the
history and drops its reasoning. "You're right — I've been stuck in a loop…" was first
written **with no user message and no veto before it** (event 1029, after three identical
`task.update`s). From then on it opens nearly every turn. Of 183 thoughts saying "the user is
right" or "Kyle is right":

- 155 came straight after an ordinary tool result: nothing but the model's own echo;
- 22 after a stop veto;
- 4 after an injected context block;
- **2** after something Kyle actually said.

The repeated preamble is an attractor: each copy makes the next more likely.

**Harness text reads as the person.** Vetoes and suffix context blocks go to the model as a
`user` message (`daemon/agent/request.go:77`). "Before stopping, the following must be
addressed…" arrives as if Kyle said it, and the model answers "Kyle is right". This is a
minor contributor (22 of 183), but it makes the apology frame worse, and it is cheap to fix.

**Sampling has no repetition guard.** llama-server's current settings, read from `/slots`:

```
temperature 0.6  top_k 20  top_p 0.95  min_p 0
presence_penalty 0.0  frequency_penalty 0.0  repeat_penalty 1.0  dry_multiplier 0.0
```

That is exactly Qwen's "thinking mode, precise coding" preset. The model card's other
thinking preset, for general tasks, uses `presence_penalty=1.5`, and it says to raise it
"between 0 and 2 to reduce endless repetitions". Nabu sends no sampling parameters of its
own, so it has no way to change this for one request.

**Tools that say nothing new.** Three of the loop shapes are a tool returning the same
uninformative line:

- `write` says `wrote 1259 bytes` whether or not the file already had those bytes;
- `edit` says `old text not found`, with no hint of what *is* there;
- `task.update` with ids left out gives every task a **new id** (t129, t138, t147 for the same
  list), so resending an unchanged plan churns it, and the model sees "the same response"
  and resends it again.

**Working memory in the 70–85% band.** Between `ClearAt` (0.70) and `SummarizeAt` (0.85),
clear-results runs every turn and stubs tool output older than four turns. Session
`01M2Y8K0…` sat at 77–79% for a long stretch, and the model retried one failing `edit` four
times. What the clears were stubbing, one per turn: the CI failure log, then **the result of
the previous identical edit**, `old text not found` (events 2338 and 2343). The record that
this exact edit had already failed was the thing being removed, so the model tried it again.
One session, so worth measuring rather than acting on. It also argues for B1: a loop notice
carries that record forward in a form clearing does not remove.

## 4. Options

Grouped by where they live. **Mechanism** means `daemon/agent` or the tools; **policy** means
a module. **Spec** means a protocol change: spec text, schema and vectors together.

### A. Tools that tell the model something new (mechanism, no spec change)

1. **`write` and `edit` say when nothing changed**:
   `unchanged: error.rs already has exactly this content (1,259 bytes)`. In the 32-write loop
   this is new information on the second call, not the thirty-third.
2. **A missed `edit` shows the nearest match**: the closest region by line similarity, with
   line numbers, so the next attempt has something to aim at instead of retrying.
3. **`task.update` keeps ids by title** when a task comes back without one and its title
   matches an existing task. Say so in the result: "no change: the plan is the same as
   revision 21".

Cheap, local, and it removes three of the five shapes at the source.

### B. A `loop` module: notice, then intervene (policy, no spec change)

It watches for **the same call with the same result** within the last few calls. That's the
strongest signal in the logs, and nearly free to compute. Near-duplicate reasoning between
turns (word-shingle overlap ≥ 0.5) is a second signal, but noisier.

It escalates:

1. **Name the repetition**, as a suffix context event (so request = f(log) holds):
   for example *"`write error.rs` has run 4 times with the same content and the same result.
   The file has not changed since the first. The last build failed with `cannot find module or
   crate breezeway_server` in `routes/auth.rs`. Doing the same thing again will not change it."*
   **It must carry facts, and change every time.** A fixed nudge is one more thing to copy.
   The facts come from the log: the repeated call, the count, and the latest *differing*
   result (usually the last build or test output).
2. **Refuse the next identical call** with a ToolGate `Deny`: "this exact call ran 4 times
   with the same result; take a different step". A denial is new information. In the
   32-write loop it would have landed on write 5.
3. **Suggest a way out:** run the check (`verify.command`), read the file that actually
   failed, or `claude.ask` (already a tool) for a second opinion.
4. **Give up visibly:** after a few rounds, move the session to `blocked` with a notice saying
   why. The phone and the TUI already show that, so Kyle finds out in minutes, not after
   an evening of writes.

### C. Say which messages are the harness (spec: §6.4 veto text)

Prefix the trailing harness message: *"[nabu, automated — not the person]"*. It's a one-line
change to normative text, plus the vectors for it. Small effect on its own (22 of 183), but it
stops the harness from feeding the apology frame.

### D. Sampling that pushes back when looping (mechanism + config; spec if per-request)

1. **Let a provider's config pass sampling parameters through**
   (`presence_penalty`, `temperature`, `dry_multiplier`, …). No spec change. Kyle could then
   run the general preset (`presence_penalty` 1.5) and compare.
2. **Raise them only while looping:** the loop module asks for a higher
   `presence_penalty` / temperature for the next request only. For request = f(log), the
   override has to be logged (a new `options_change` key, or a field on the context event).
   That makes it a spec change.

Caution: a presence penalty applies to every token, and code repeats identifiers legitimately.
Qwen's coding preset sets it to 0 for a reason. That's why the targeted version (only while a
loop is detected) is the more interesting one.

### E. Take the loop out of the context (spec: new compaction mode)

The repeated calls and "You're right…" preambles are the attractor. A `collapse` compaction
would replace a detected loop range with one line: *"[12 turns: `write error.rs` repeated with
identical content; no effect]"*. That removes the thing being copied. It's the strongest
structural fix and the most expensive (spec §6 assembly, schema, new vectors), so do it only
if A and B are not enough.

### F. Context housekeeping (measure first)

- **Summarise earlier when a loop is detected:** a fresh context drops the attractor
  wholesale. It's a lever the loop module could pull through the existing `nabu.session.compact`.
- **Revisit the 70–85% band:** four turns of results is little for a build-fix cycle.
  Measure before changing.
- **`preserve_thinking`:** Qwen recommends it for agents, and nabu drops reasoning. It could
  help (the model sees it already concluded the same thing) or hurt (more to copy). It's an
  experiment for the bench, not a default.

## 5. Recommendation

**Build A and B1–B2 first.** They need no protocol change, they attack the shared cause (no
new information), and the logs let us check them directly. Replaying the three loop sessions'
tool calls through the new `write` and the loop detector shows where each loop would have
been broken.

Then **D1** (a config pass-through), so Kyle can try the general preset without code changes,
and **C**, because it's one line.

Hold **E** and **F** until A and B have been running for a week, then re-run the tally in
§3 on the new logs.

## 6. Measuring it

- The **stats** from #97 already count re-reads. Add "repeated call and result" and "near-identical
  thoughts" per session, so every session reports its own loop count.
- The **bench** (`bench/`) runs tasks end to end. Add the same count to its report, and run it
  with each option on and off.
- The **scripts** used for this proposal, in `docs/proposals/loops/`, kept for re-running on
  new logs:
  - `repeats.py <session>`: every stretch of the same call with the same result;
  - `by_fill.py`: the fill-level tally in §3;
  - `answering.py`: the "who was the model answering" tally;
  - `show.py <session> <from> <to>`: print a stretch of a log, as in §1 and §2.

## 7. Not proposed

- **A fixed "you are looping, stop" message.** The model already says that to itself every turn.
- **Killing the session on the first repeat.** Some repeats are legitimate: re-running a build
  after an edit is how a fix is checked. The trigger is the same call *and* the same result.
- **A global repetition penalty as the new default** without measuring it on code: see D.

## Sources

- Qwen3.6-35B-A3B model card, Best Practices:
  <https://huggingface.co/Qwen/Qwen3.6-35B-A3B> (sampling presets, `presence_penalty` for
  endless repetitions, `preserve_thinking` for agents).
- Session logs under `~/.nabu/sessions/`, 2026-09-18 to 2026-09-22.
