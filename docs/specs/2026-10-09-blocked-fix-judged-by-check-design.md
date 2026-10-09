# A blocked fix is judged by the check

**Date:** 2026-10-09
**Status:** Approved by Kyle in conversation

## Problem

The runner throws away a session that ends blocked: it resets the worktree to where the
session started, and the step is tried again. For a plan or work session that is right:
the session did not finish its job, and nothing after it would notice.

A fix session is different. The check runs verify.sh straight after it, and that is
what the fix is judged on. Its own stop gate is a second opinion.

In the equipment run on liftoff, a fix session did as the owner asked:
1. It committed the button-width fix.
2. It committed `verify-revision.md`, asking for six Compose checks to come out of
   verify.sh, because the brief says Compose UI tests are not required.

Its stop gate then refused every stop over a failing check it could not clear (a
separate bug, in the verify module). The stall rule ended the session blocked after
about a hundred turns. The runner reset to the session's start, which lost both
commits, and started the fix over. The revision request never reached Claude.

## Decision

A fix or ci-fix session that ends blocked is judged on what it committed, as a session
that stopped is:
- What it left uncommitted is reset away. The check runs on the worktree, so leaving it
  would judge files the pull request never gets.
- The session is stopped, and the run goes on as from a stop:
  - verify.sh changed: the result is discarded, as before;
  - a revision request: revise;
  - otherwise: check.

Every other step still discards a blocked session.

## Rejected

- **Honouring only a committed revision request.** A blocked fix that committed a real
  fix loses it just the same, and the check would have passed it.
- **Keeping what was left uncommitted.** The check would pass on files that are never
  committed, and the PR would lack them.
- **Treating every blocked session this way.** After a plan, tasks or work session,
  nothing checks the result before the next step builds on it.

## What proves it works

`clients/runs`:
- A fix session that commits a revision request and ends blocked: no reset to its
  start, Claude revises, the run finishes, and the session is stopped.
- A fix session that commits a fix, leaves a file uncommitted and ends blocked: the
  reset goes back only to its last commit, and the run finishes.
- A blocked work session is still reset to its start and retried.
- The full gate.
