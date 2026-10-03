# nabu github, and reviewing pull requests

**Date:** 2026-09-28
**Status:** Approved by Kyle in conversation
**Part 1 of 3.** This spec sets up the watcher that parts 2 and 3 rely on
(`2026-09-28-github-comments-design.md`, `2026-09-28-github-issues-design.md`). Each part
is its own pull request, and they land in this order.

## Problem

Kyle wants nabu to work from GitHub without being asked each time: review pull requests,
address comments left on them, and work issues. Breezeway comes first. Nothing in nabu
can do that today:

- Every session starts because a client asked for one. Nothing notices an outside event
  and starts work by itself, and `Host` has no way for a module to create a session.
- The `gh` tool (`daemon/modules/vcs/gh.go`) only reads. It cannot comment, review, push,
  or open a pull request.
- `guard` treats reaching the network as high risk, so it asks even in `auto` mode.
  In a session nobody is watching, nobody will answer.

## Decision

A new long-running client, `nabu github`, polls GitHub with `gh` and does its work by
starting ordinary daemon sessions over the existing protocol. It posts to GitHub itself,
and only after a session has finished. **Neither the daemon, the protocol nor any module
changes.**

This part builds the watcher and one job: reviewing pull requests.

### Where it lives

- `clients/github/` is the watcher: config, state, polling, the job queue and the
  worktrees. It imports `clients/goclient` and `protocol`, and never the daemon.
- `cmd/nabu` gets a `github` subcommand:
  `nabu github [--once] [--dry-run] [--root DIR]`.
  - With no flags it runs until it is interrupted.
  - `--once` polls once, runs whatever jobs that finds, waits for them to finish, and
    exits. That is for testing and for Task Scheduler.
  - `--dry-run` runs the sessions for real, then prints what it would have posted
    instead of posting it. It keeps its state in a separate file, so a dry run never
    marks anything as handled for a real run.

### Config

`<root>/github/config.json`. It belongs to the watcher and not the daemon, so the daemon's
config is unchanged.

```json
{
  "repos": [
    {"name": "corporealshift/breezeway",
     "clone": "C:/Users/corpo/Documents/projects/breezeway"}
  ],
  "label": "nabu",
  "poll": "2m",
  "quiet": "5m",
  "max_jobs": 1,
  "review": {"enabled": true, "pushes": true, "max_turns": 100}
}
```

- `clone` is Kyle's existing checkout. The watcher only runs `git fetch` and
  `git worktree` in it. It never checks anything out there and never changes the files.
- `max_jobs` is how many sessions run at once, across all repositories. The default is 1,
  because sessions share one local model.
- `review.pushes`: when true, each new head of a PR gets its own review; when false, only
  the first head a PR has is reviewed.

### State

`<root>/github/state.json`. For each repository it records:

- the head SHA of each PR that has been reviewed;
- each job still running: its kind, the item, the head SHA, the session ID and the
  worktree path;
- each job that failed, with what it failed on (a head SHA, or an `updated_at` time).

The state is written before a session is created and after the result is posted. A crash
between those two leaves a job marked as running with a session ID. On restart the
watcher reattaches to it through `nabu.session.state`.

### The poll

Every `poll`, for each repository:

1. `gh pr list --state open --json number,headRefOid,headRefName,isDraft,isCrossRepository,updatedAt,labels,baseRefName`.
2. Turn the list plus the state into jobs. This is a pure function: snapshot + state +
   now → jobs. It is where the rules live, and most of the tests go here.
3. Add the new jobs to a queue, and start them while fewer than `max_jobs` are running.
4. Check every running job with `nabu.session.state`.

If `gh` or the daemon cannot be reached, that poll is skipped and nothing in the state
changes. The next poll tries again.

**The watcher never subscribes to its sessions.** Under §7.18 that means a permission
request is refused at once rather than left waiting, so a session that tries to push or
to reach the network finds out immediately. If Kyle attaches from the TUI, he is a
subscriber and gets any question first. The watcher never answers one.

### Reviewing a pull request

**Which PRs.** Open, not a draft, not from a fork, and with a head SHA the watcher has not
reviewed. The head must also have been the head for at least `quiet`, so a burst of
pushes gets one review. Every such PR in a configured repository is reviewed, label or
no label. That includes PRs nabu opened itself.

**Worktree.** The watcher runs `git fetch origin pull/<n>/head <base>` in the clone, then
`git worktree add --detach <root>/github/worktrees/<repo>/review-<n>-<sha7> <sha>`.

**Session.**
- `nabu.session.create` with the worktree as the workspace,
  `options.permission_mode: "auto"` and `budget.max_turns: review.max_turns`. That is 100
  by default (amended 2026-10-03: the first default, 30, cut real reviews off before
  they finished).
- No goal. A review changes nothing, so the stop gate has nothing to object to.
- The prompt gives the PR's title, body, base and head. It says the diff is
  `git diff origin/<base>...<sha>` and tells the model not to change any files. If nabu
  reviewed this PR before, it also includes that review's summary and says what has
  changed since it (`git diff <old sha>...<sha>`), so the model does not repeat itself.
- The prompt tells the model not to use `ask`, and to put anything it could not settle
  into the summary as a question for the author. In the first live runs, a review model
  asked "does this workspace have any go.mod or test files?", which it could have checked
  itself. Nobody was subscribed, so the question waited out its 10-minute timeout before
  the model carried on without an answer.
- The prompt says the final message must end with a fenced `json` block:

```json
{"summary": "…", "comments": [{"path": "src/x.rs", "line": 42, "body": "…"}]}
```

**When it is done.** The session is done when its state is `idle`. The watcher calls
`nabu.session.stop`, which writes the `report`, and reads the last assistant message with
`events_after`. Then:

- Parse the JSON block. Every comment whose `path` and `line` fall on the right side of a
  hunk in the reviewed diff becomes a line comment. Every other comment is added to the
  review body as `path:line — body`. GitHub rejects the entire review if one line comment
  points outside the diff, and this keeps one bad line from losing all the others. The
  diff is `git diff origin/<base>...<sha>`, taken in the worktree before it is removed,
  and not `gh pr diff`. `gh pr diff` shows the PR's current head, which may not be the
  SHA that was reviewed.
- If there is no JSON block, or it does not parse, the whole final message becomes the
  review body.
- Post one review with `gh api repos/<repo>/pulls/<n>/reviews`, using
  `event: "COMMENT"` and `commit_id: <reviewed sha>`. Pinning the commit means that if
  the PR has moved on since, the comments still point at the code that was reviewed.
- The review body opens with `🤖 **nabu** reviewed \`<sha7>\``, and each line comment
  opens with `🤖 **nabu**: `. Every body ends with `<!-- nabu -->`.
- Record the SHA as reviewed and remove the worktree.

**Why only `COMMENT`.** nabu posts as Kyle's `gh` login, and GitHub does not let an author
approve or request changes on their own pull request. So nabu never approves or blocks
anything, which is also the right amount of authority for it to have.

### The signature and the marker

Kyle and nabu post as the same login, so GitHub shows Kyle as the author of nabu's
reviews. Two things tell them apart, one for people and one for the watcher:

- **The signature**, `🤖 **nabu**`, opens every review body and every line comment. A line
  comment carries its own because it is read in the diff, apart from the review it came
  with.
- **The marker**, `<!-- nabu -->`, ends everything the watcher posts, and is invisible
  once rendered. Parts 2 and 3 never start a job from a comment that carries it. Without
  the marker, a review from nabu would be read as comments from Kyle, get addressed, and
  set off another review. The watcher keys on the marker rather than the signature
  because Kyle might quote the signature himself.

**Rejected: a separate GitHub identity.** A GitHub App (`nabu[bot]`) or a machine user
would make nabu the real author. Its reviews could then approve or request changes, and
the marker would not be needed. Kyle chose the signature: it needs no app, no account and
no key, and it is enough to tell who wrote what.

## How it fails

| What | Then |
|---|---|
| The daemon or GitHub cannot be reached | The poll is skipped. Nothing changes. |
| The watcher restarts partway through a job | It reattaches using the session ID in the state. |
| The session ends `blocked`, `paused` or `error` | Nothing is posted. The job is recorded as failed for that head SHA and not tried again until the PR has a new head. The worktree is kept for inspection. The watcher logs the session ID. |
| The session stays `running` or waits on a question | It is left alone. It appears in `nabu status`, and a question in it can be answered from the TUI. |
| Posting the review fails | Nothing is recorded as reviewed. The session ID and the review text are logged, and the next poll posts again from the same session's final message, without running a new session. |
| The session changes files anyway | The worktree is thrown away. Nothing is pushed. |

## Not doing

- Approving, requesting changes, or merging.
- Reviewing PRs from forks.
- Webhooks, or any other push from GitHub (see Rejected).
- Replies in threads, or resolving threads.
- Trying a failed job again when nothing about the PR has changed.

## Rejected

- **A background hook inside the daemon.** A `Service` interface plus `Host.StartSession`
  would keep the watcher running whenever the daemon runs. But it adds a mechanism to the
  module contract (architecture spec §14.1) that exists for one integration, and it lets
  modules create sessions, which they are deliberately unable to do. A client needs no
  change to the core.
- **GitHub Actions calling the daemon over Tailscale.** Instant, and no polling. But every
  repository needs a workflow and a Tailscale auth key, the daemon becomes reachable from
  CI runners, and worktrees still need something on this machine to make them. The
  watcher's job logic does not depend on how it learns about events, so this can be
  added later as a second trigger.
- **Sessions posting through new `vcs` operations** (`git.push`, `gh.pr.review`). The
  model could post as it goes and reply in threads. But `guard` rightly treats network
  access as high risk, so an unattended session would need something to approve that on
  its behalf. That puts the riskiest decision in the place least able to judge it. Here,
  the session only ever changes its worktree.
- **Webhooks.** The daemon is behind Tailscale with no public way in. `watch` chose
  polling for similar reasons.
- **Reviewing in Kyle's checkout.** `watch` exists because Kyle works in that checkout
  from other terminals. A job that checked out a PR there would pull the files out from
  under him.

## What proves it works

- A table test of the pure function (snapshot, state, now → jobs):
  - draft, fork and already-reviewed PRs are skipped;
  - the quiet period is respected;
  - `pushes: false` reviews only the first head;
  - a failed head is not retried, and a new head is;
  - running jobs count against `max_jobs`.
- A table test of splitting comments against a real multi-hunk diff: which lines inside
  and outside the hunks become line comments and which go into the body.
- Tests of the post step against a fake `gh` and a fake daemon client: the right review
  JSON is sent, the marker is present, and the state is written in the right order around
  a failed post.
- `nabu github --once --dry-run` against breezeway. The printed review is checked by eye.
- One real `--once` run on a scratch repository with an open PR, before breezeway.
