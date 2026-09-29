# nabu github: addressing comments on pull requests

**Date:** 2026-09-28
**Status:** Approved by Kyle in conversation
**Part 2 of 3.** Builds on the watcher in `2026-09-28-github-review-design.md`. Its config,
state, queue, worktrees, signature, marker and failure handling apply here unchanged, and
this spec only covers what is different.
**Changes:** `2026-09-25-per-workspace-gate-design.md`. A gate configured for a
repository's path also applies to that repository's worktrees.

## Problem

When Kyle leaves comments on a pull request, someone has to go back to the branch, make
the changes, push them, and answer each comment. For a PR nabu opened (part 3), or one
Kyle hands to it, nabu should do that.

## Decision

A second job kind for the watcher, `comments`.

### Which PRs, and which comments

**PRs.** Open, not from a fork, and carrying the `label` from config (`nabu`). The label
is the opt-in. Part 3 puts it on every PR nabu opens, and Kyle can add it to any other
PR. Taking the label off stops the job.

**Comments.** Three kinds, each read with `gh api`:

- review comments on lines, from `pulls/<n>/comments`;
- review bodies that are not empty, from `pulls/<n>/reviews`;
- conversation comments, from `issues/<n>/comments`.

A comment counts if it does not carry the `<!-- nabu -->` marker and its ID is newer than
the last one handled **of its own kind** on that PR. The three kinds come from three
endpoints, each with its own sequence of IDs, so one number cannot be compared across
them. The state keeps three watermarks per PR, one for each kind.

The first time the watcher sees a labeled PR, nothing on it has been handled, so every
comment on it counts. Labeling a PR hands it over, comments and all. On a PR with a long
history, that includes threads that were settled long ago. The model sees the code and
can reply that something is already done, and the REST API gives no way to tell a
resolved thread from an open one.

The newest counting comment must also be at least `quiet` old (default 5 minutes), going
by GitHub's `created_at` for comments and `submitted_at` for reviews. That way a review
written as several comments becomes one job rather than several.

**One job per PR at a time.** Comments that arrive while a job is running wait for the
next one.

**Comment jobs go first.** When more jobs are due than `max_jobs` allows, comment jobs
start before reviews, because a person is waiting on a reply.

### Worktree

Kyle's clone may have the PR's branch checked out, and git will not check out one branch
in two worktrees. So the worktree is detached:

```
git fetch origin <head>
git worktree add --detach <root>/github/worktrees/<repo>/comments-<n>-<first comment id> origin/<head>
```

The watcher pushes with `git push origin HEAD:refs/heads/<head>`. It never uses `--force`.

### Session

- `permission_mode: "auto"`. The budget is `comments.max_turns` (default 60).
- The goal condition: "Every comment listed in the first message has been addressed by a
  change or answered by a reply, the changes are committed, and nothing is pushed." With
  a goal set, the stop gate runs the judge and the repeated checks from the architecture
  spec §10.2–10.4. That includes the dirty-tree check and the project gate (see the gate
  change below).
- The prompt gives the PR's title, body, base and head. It then lists each new comment
  with:
  - its ID and author;
  - its kind;
  - for review comments, the path, line and diff hunk;
  - its body.

  It includes earlier comments in the same thread as context, marked as already handled.
- The prompt says:
  - commit, and do not push;
  - follow the repository's conventions;
  - if a comment is unclear, change nothing for it and ask the question in the reply.
    Whoever wrote the comment answers on GitHub, which is a new comment and so a new job.
    Waiting on the `ask` tool in a session nobody is watching blocks the session for
    nothing.
- The final message ends with a fenced `json` block:

```json
{"replies": [{"id": 123456, "body": "Renamed to parseCursor in a1b2c3d."}]}
```

### When it is done

When the state is `idle`, the watcher calls `nabu.session.stop` and reads the final
message. Then:

1. If there are new commits since the head the job started from, push them. If the push
   is rejected, the branch has moved: post nothing, record the job as failed, keep the
   worktree.
2. Post the replies:
   - a review comment is replied to in its thread (`pulls/<n>/comments/<id>/replies`);
   - review bodies and conversation comments get one conversation comment that quotes
     each and answers it underneath;
   - every body opens with the signature (`🤖 **nabu**: `) and ends with the marker.
3. Any counting comment without a reply goes into a closing line: "Not answered
   individually: …, see the pushed commits."
4. Record the newest ID of each kind in the job as handled, and remove the worktree.

If there is no JSON block, or it does not parse, step 1 still runs. Instead of step 2,
the whole final message is posted as one conversation comment.

### The gate in a worktree

`verify` finds a workspace's gate in `commands`, keyed by exact path. A worktree at
`~/.nabu/github/worktrees/breezeway/…` does not match `C:/…/breezeway`, so it would get
the global `command`, which is empty. Breezeway is where a session once opened two pull
requests without compiling, and that is the reason it has a gate.

**Change:** when a workspace has no entry of its own, `verify` looks for its repository's
main worktree and uses that entry. It finds it with
`git rev-parse --path-format=absolute --git-common-dir`, whose parent is the main
worktree. It only does this when `commands` is not empty, and if git fails the answer is
simply "no entry".

The config is still keyed by a path a person can read and write, as the gate spec
requires. What changes is only which workspaces an entry covers: every worktree of that
repository, which is what the entry was always meant to mean.

A gate run in a new worktree starts cold: a fresh `target/` and a fresh Gradle build
directory. That is slower, and it is the gate command's business rather than the
watcher's. Kyle can point `CARGO_TARGET_DIR` at a shared directory inside the command if
it proves too slow.

## How it fails

The part 1 table applies, plus:

| What | Then |
|---|---|
| The push is rejected because the branch moved | Nothing is posted. The job is recorded as failed. The comments stay unhandled, so the next new comment makes a new job, starting from the moved branch. |
| The session changed nothing and only replied | Nothing is pushed. The replies are posted. |
| The label is removed while a job is running | The job finishes and posts. No new jobs start. |
| The judge never agrees and the session ends `blocked` | Nothing is pushed or posted. The job's watermarks are recorded as failed-through. Only a comment newer than those starts a job again, and that job still includes every comment not yet handled. |

## Not doing

- Resolving review threads.
- PRs from forks, and pushing anywhere except the PR's own head branch.
- Force-pushing, rebasing, or resolving merge conflicts with the base.
- Addressing comments on PRs without the label.
- Answering comments that nabu itself posted.

## Rejected

- **Addressing comments on every PR.** Kyle's own work-in-progress PRs would get commits
  pushed to them that he did not ask for. The label keeps it opt-in, and it is how part 3's
  PRs are included automatically.
- **A job per comment.** One review is several comments about one change. Separate jobs
  would each run the gate and push, and could conflict with each other. The quiet period
  plus one job per PR turns a review into one job.
- **Checking out the head branch in the worktree.** git refuses to check out a branch that
  is already checked out in Kyle's clone, and taking it over would change his checkout.
  A detached worktree pushed with `HEAD:refs/heads/<head>` has neither problem.
- **Keying the gate by the workspace key.** The gate spec rejected this because a person
  cannot read or write the key. Matching a worktree to its main worktree keeps the config
  in paths.
- **A persistent worktree per repository to keep build caches warm.** It would conflict
  with keeping a failed job's worktree for inspection, and it adds cleanup rules. Starting
  cold costs time and never gives a wrong answer.

## What proves it works

- A table test of the job function, extended:
  - comments with the marker are ignored;
  - the quiet period applies to the newest comment;
  - PRs without the label are skipped;
  - a PR with a job running gets no second job;
  - the handled ID moves forward only after a post.
- A table test of building replies from the JSON: thread replies versus the quoted
  conversation comment, and the closing line listing unanswered comments.
- Post-step tests against a fake `gh` and fake git:
  - a rejected push posts nothing;
  - a session that only replied pushes nothing;
  - the push refspec is exactly `HEAD:refs/heads/<head>`.
- A `verify` test: a worktree of a repository with a `commands` entry gets that entry;
  a directory that is not a repository gets `command`; an exact entry for the worktree
  wins over its main worktree's.
- A real run on a scratch repository: open a PR with the label, leave two line comments
  and one conversation comment, run `nabu github --once`, and check the pushed commit and
  the three replies.
