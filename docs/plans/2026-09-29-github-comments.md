# nabu github: addressing comments — plan

Spec: `docs/specs/2026-09-28-github-comments-design.md`. One branch, `github-comments`,
rebased on `main` after #123 merged, and one PR. Every step leaves
`go build ./... && go vet ./... && go test ./...` green and is its own commit. Step 1 is
independent of the others. Steps 2 and 3 are pure, and step 4 needs both.

## 1. verify: a repository's gate covers its worktrees

**Depends on:** nothing.

**Changes:**
- `daemon/modules/verify/verify.go`:
  - `commandFor` tries the exact workspace first, as now.
  - If there is no exact entry and `Commands` is not empty, it looks up the main worktree:
    `git rev-parse --path-format=absolute --git-common-dir`, run in the workspace, whose
    parent directory is the main worktree. It uses that entry if there is one.
  - A git failure means there is no entry, and the fallback is `Command`.
  - The comment on `Commands` says an entry also covers the repository's worktrees.

**Proves it:** new tests in `commands_test.go`, run against a real temporary repository
with a worktree added to it:
- the worktree gets the main worktree's entry;
- an exact entry for the worktree wins over the main worktree's;
- a directory that is not a repository gets `command`.

## 2. Choosing comment jobs

**Depends on:** nothing.

**Changes:**
- `clients/github/config.go`: `Comments{Enabled *bool, MaxTurns int}`, with a default of
  60 turns.
- `clients/github/exec.go`: `prFields` gains `labels`, and `PR` gains `Labels []string`.
- `clients/github/comments.go`:
  - `Comment{Kind, ID, Author, Body, Path, Line, DiffHunk, InReplyTo, Created, URL}`.
    `Kind` is `line`, `review` or `issue`.
  - `Marks{Line, Review, Issue int64}`, with `Of(kind)` and `Raise(Comment)`.
  - `CommentsDue(cfg, st, pr, comments, now) []Comment`. It returns the counting comments,
    or nil when:
    - comments are off;
    - the PR is a fork, or lacks the label;
    - a comment job is already running on the PR;
    - no comment counts (no marker, the review body is not empty, and the ID is above
      that kind's handled mark);
    - the newest counting comment is younger than `quiet`;
    - a failed-through mark exists and no counting comment is above it.
- `clients/github/state.go`:
  - `RepoState` gains `Handled map[int]Marks` and `FailedThrough map[int]Marks`.
  - `Job` gains `HeadRef`, `Through Marks`, `Prompt`, `Goal`, `MaxTurns`, `Pushed` and
    `Posted []string`.
  - The prompt and goal are stored at the start, so a restart can create the session
    without fetching anything again.

**Proves it:** `TestCommentsDue`, a table with one row per rule. It includes the three
watermarks being independent (a review ID below the line mark still counts) and a
failed-through PR coming back once a newer comment arrives.

## 3. The prompt, and turning the answer into replies

**Depends on:** step 2.

**Changes:**
- `clients/github/comments.go`:
  - `CommentsPrompt(repo, pr, due, all)` gives the PR, then each thread that has a new
    comment. Every comment in the thread is shown, in order, marked new or earlier, with
    the marker stripped. Review bodies and conversation comments come after. The prompt
    ends with the rules from the spec (commit, don't push, no `ask`, questions go in
    replies) and the JSON format.
  - `CommentsGoal` returns the goal text from the spec.
  - `ParseReplies(final) (map[int64]string, bool)`.
  - `BuildReplies(due, replies) Replies{Threads []ThreadReply{Key, Root, Body}, Conversation string}`:
    - a line comment's reply goes to its thread root (GitHub only takes replies to a
      top-level comment);
    - review and conversation comments are quoted and answered in one conversation
      comment;
    - any due comment without a reply is listed by URL in the closing line;
    - every body opens with `Signature + ": "` and ends with the marker.

**Proves it:**
- `TestCommentsPrompt`: threads grouped, new and earlier marked, the marker stripped, the
  rules present.
- `TestParseReplies`.
- `TestBuildReplies`:
  - a reply to a reply goes to the root;
  - review and conversation comments are quoted;
  - an unanswered comment is in the closing line;
  - with no replies at all, only the closing line;
  - signature and marker on every body.

## 4. The watcher runs comment jobs

**Depends on:** steps 2 and 3.

**Changes:**
- `clients/github/ports.go`:
  - `GitHub` gains `PRComments(ctx, repo, n) ([]Comment, error)`,
    `ReplyTo(ctx, repo, n, root int64, body)` and `Comment(ctx, repo, n, body)`.
  - `Git` gains `FetchBranch(ctx, clone, ref)`, `Head(ctx, dir) (string, error)` and
    `Push(ctx, dir, ref)`.
  - `Daemon` gains `SetGoal`.
- `clients/github/exec.go`:
  - `PRComments` reads all three endpoints with `gh api --paginate`. It decodes the
    stream of JSON arrays, and uses `line`, falling back to `original_line`.
  - `ReplyTo` posts to `pulls/<n>/comments/<root>/replies`, and `Comment` to
    `issues/<n>/comments`, each with the JSON on stdin.
  - `FetchBranch` runs `git fetch origin +refs/heads/<ref>:refs/remotes/origin/<ref>`.
  - `Push` runs `git -C <dir> push origin HEAD:refs/heads/<ref>`.
- `clients/github/daemon.go`: `SetGoal` calls `nabu.session.set_goal`.
- `clients/github/watcher.go`:
  - Starting a job, advancing its session and failing it all work for both kinds. The
    prompt, goal and budget come from the job.
  - Comment jobs are queued ahead of reviews.
  - Finishing a comment job:
    1. push, if `Head` differs from the job's SHA and the job is not yet `Pushed`;
    2. post each thread reply, then the conversation comment, skipping any key already in
       `Posted` and saving after each;
    3. raise `Handled` to `Through`;
    4. drop the job and remove the worktree.
  - A failed push fails the job, and it is not retried.
  - A dry run prints the push and the posts instead of doing them.
  - Failing a comment job records `FailedThrough`.

**Proves it:** fake-backed tests:
- a labeled PR with a due comment starts a session with a goal and the comments budget;
- finishing pushes to `HEAD:refs/heads/<head>` and posts one thread reply and one
  conversation comment;
- the handled marks rise, so a second poll starts nothing;
- a session with no new commits pushes nothing;
- a rejected push posts nothing and records failed-through;
- a failed post resumes without posting twice or pushing again;
- a comment job is admitted before a review under `max_jobs: 1`;
- a dry run neither pushes nor posts.

Then `TestExecArgs` gains rows for every new command.

## 5. Checking it for real

On the scratch repository:
1. Open a PR with the `nabu` label.
2. Leave two line comments (one a question) and one conversation comment.
3. Run `nabu github --once`, and check the pushed commit, the thread replies, the
   conversation comment, and that nabu's own replies start nothing on the next run.
4. `--dry-run` against breezeway.

## Not doing in this plan

- Part 3 (issues).
- Resolving threads, and any GraphQL.
- Warming build caches in worktrees.
