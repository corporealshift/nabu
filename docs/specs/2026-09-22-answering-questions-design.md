# Answering a question without starting work

**Date:** 2026-09-22
**Author:** Kyle (corporealshift), with Claude
**Status:** Proposed in the PR for issue 76

## 1. The problem, from the logs

Two exchanges from real sessions, both after work had been under way:

| The person asked | What happened |
|---|---|
| "what's the status here?" | ran the tests, said "3 failures left. Let me fix them.", and edited `engine.rs` three times |
| "why did you stop here? I am curious if something is causing the stopping…" | wrote an 11 KB plan file; `verify` then vetoed the stop ("uncommitted changes — commit them"), so it committed the plan, to `main` |

#85 added a line to the system prompt. It did not hold, for two reasons:

- **The model reads it once, at the top.** On a long session it is 100K tokens behind the
  question. What the model reads last is the transcript of the work it was doing.
- **The stop gate pushes back.** `verify` refuses a stop while tasks are open, checks have
  failed, or the tree is dirty. So even a model that answers and stops is told "these tasks
  are not finished" and sent back to them. On a question turn that isn't verification, it's
  an instruction to resume work.

## 2. Decision

A question turn is one where the person's latest message **asks and asks for nothing
else**, and no goal is active. On such a turn:

1. **The `answer` module puts a reminder at the end of the request**, as a suffix `context`
   event, on every request of the turn: answer, then stop; read and run what you need; do
   not edit, commit or resume earlier work.
2. **It holds workspace changes for approval.** `edit`, `write`, `git commit`, and plainly
   mutating `bash` (git add/commit/push/checkout/…, rm, mv, redirects, `sed -i`) return
   `Ask`, with a summary that says why: *"you asked a question, and answering it wants to
   change the workspace: edit engine.rs"*. Reading and running things to find the answer
   stays free, including the tests.
3. **`verify` treats an answered question as a finished turn.** If the turn asked a
   question and nothing changed the workspace since, the stop is allowed. Once something
   did change (the person approved an edit), every check applies again.

Question-turn detection lives in `daemon/module` (`AsksOnly`, `QuestionTurn`,
`ChangedSinceUser`, `ChangesWorkspace`) because two modules need the same answer, and
modules may import only the module contract. The core (`daemon/agent`) is unchanged.

## 3. The classifier, and why it leans towards "no"

`AsksOnly` is a heuristic. It says yes only when some sentence is a question (ends in `?`,
or opens with an interrogative) **and** no sentence is a request. A sentence is a request
if it opens with a polite form ("can you", "could you", "please", "I'd like you to", "let's",
"we should", …) or with an imperative verb ("fix", "add", "run", "commit", "ask", …).

The errors are asymmetric, and it is tuned for that:

- **A request taken for a question** costs one approval prompt per change.
- **A question taken for a request** is what happened before this existed.

So "can you get some ci set up? rust fmt, clippy, tests, etc?" is a request, and so is
"what's going on here? ask Claude to help". A question with no `?` that opens on a
statement ("Why did you stop here") is not detected. That keeps it conservative.

## 4. Rejected

- **Only strengthen the prompt.** Already tried in #85; the log above is after it.
- **Deny instead of ask.** A request misread as a question would silently do nothing. Ask
  keeps the person in the loop at the cost of a tap.
- **Classify in the core, and pass it in `StopInfo`.** An opinion about what a message
  means is policy, and policy lives in modules (invariant 4).
- **Ask a model whether the message is a question.** A second model call on every turn,
  on a local model that already takes seconds, to decide something a few rules decide as
  well for the messages in the logs.
- **Gate all of `bash` on a question turn.** "What's the status?" legitimately runs the
  tests. Only plainly mutating commands are held.

## 5. Off switch

`{"modules": {"answer": {"enabled": false}}}` turns off the reminder and the gate. The
`verify` exemption stays, because it depends only on whether the turn changed anything.
Under permission mode `bypass`, `Ask` is approved automatically, as everywhere else.
