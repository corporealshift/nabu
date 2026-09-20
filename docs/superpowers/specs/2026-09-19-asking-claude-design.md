# Asking Claude — a second opinion nabu can fetch for itself

**Date:** 2026-09-19
**Author:** Kyle (corporealshift), with Claude
**Status:** Approved, not yet planned

## 1. Purpose

Let nabu get a review, or an answer to a hard question, from Claude — without the owner
carrying the message.

Today the loop is: nabu opens a PR, the owner asks Claude to review it, the owner relays
the answer back. That works and has produced real findings, but it needs a person in the
middle for every round trip.

## 2. Why the CLI and not the API

Two routes exist. The API route is cheaper in code: `provider.Resolve` splits a model
string on `/`, so an `anthropic` provider plus `verify.judge_model: "anthropic/…"` would
make Claude the goal judge with **no new code at all** — `verify` already runs a model as
a stop gate.

It was still rejected, for two reasons.

**Credentials.** There is no `ANTHROPIC_API_KEY` on this machine; the `claude` CLI
authenticates from `~/.claude/.credentials.json`, which is the owner's subscription. The
API route means a new key and per-token billing alongside a subscription already paid
for. The CLI route reuses it.

**A reviewer with tools is a different animal.** The judge path hands Claude a transcript.
The CLI path gives Claude the repository: it reads the diff, the spec and the code. Every
useful review in the session that prompted this came from looking at something, not from
reasoning about a transcript.

## 3. Shape

A module at `daemon/modules/claude/`, one line in `daemon/modules/all.go`, offering one
tool:

```
claude.ask {prompt, timeout_seconds?} → Claude's reply
```

It runs `claude -p "<prompt>"` with the session's workspace as the working directory, so
Claude sees the repository nabu is working in.

### One tool, not two

No separate `claude.review` with a canned prompt. A fixed review template is wrong half
the time, and the model can write a better prompt for the situation in front of it than
this module can write in advance.

## 4. Read-only, and why

```
--allowedTools Read Grep Glob
```

A reviewer that can quietly edit the repository removes the only thing that makes a review
worth having: that a second party looked and did not touch it.

**Verified, not assumed.** Asked to create a file under these flags, Claude reported the
write blocked and no file appeared.

The cost is that Claude cannot run the tests. That is acceptable because **nabu can**: it
runs them and puts the output in the prompt. Claude does not need a shell to know a test
failed; it needs the failure text and the code that produced it.

`allowed_tools` in config widens this for anyone who disagrees.

## 5. What it deliberately is not

- **Not a conversation.** One prompt, one reply, no session resumed. Holding a Claude
  session across nabu turns would mean a second conversation living inside a session, and
  nothing in the log model wants that.
- **Not automatic.** It does not implement `BeforeStop`. Every call is one nabu chose to
  make. A gate firing on every stop attempt would spend the owner's quota invisibly.
- **Not a way to delegate work.** It answers; it does not change the workspace.

## 6. How it fails

| Case | Result |
|---|---|
| `claude` not on PATH | the tool is not offered at all, as `vcs` does with `git` |
| Exceeds the timeout | `kind: timeout` |
| Non-zero exit | `kind: exit` with the code |
| Reply larger than the cap | truncated, and the reply says so |

The timeout defaults to **300s**, not the 120s the built-in tools use. A real review takes
minutes, and a limit that cuts it off mid-answer would make the tool useless while
appearing to work.

`guard` rates it **medium**: it reaches the network and spends money.

## 7. What is not solved

**Quota.** Each call spends the owner's Claude Code allowance, from a process nobody is
watching. The timeout bounds one call; nothing bounds how many a run makes. Shipping
without a cap is a deliberate choice to see how it behaves in practice rather than guess a
number now — but it is a real exposure, not an oversight.

## 8. Rejected alternatives

**A skill instead of a tool.** nabu already has `bash`; a skill saying "run `claude -p …`"
would cost no daemon code at all. Rejected because a shell call has no bounded timeout for
this specific thing, no output cap, no structured failure kind, and `guard` cannot tier it
apart from any other command. A tool is also in the model's list always, where a skill is
only known once loaded.

**The anthropic provider.** §2.

**A canned review prompt.** §3.

**Write access for the reviewer.** §4.
