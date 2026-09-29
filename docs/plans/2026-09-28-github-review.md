# nabu github: reviewing pull requests — plan

Spec: `docs/specs/2026-09-28-github-review-design.md`. One branch, `github-review`, one PR.
Every step leaves `go build ./... && go vet ./... && go test ./...` green and is its own
commit. Steps 1 and 2 are pure and could land in either order. Step 3 needs both, and
step 4 needs step 3.

Parts 2 and 3 (`github-comments`, `github-issues`) each have their own branch, and at the
moment each holds only its spec. Neither is based on this branch. Each is rebased onto
`main` after this PR merges, and gets its own plan then.

The package is `clients/github`. It imports `clients/goclient`, `protocol` and the
standard library, and nothing under `daemon/`. Step 3 adds a test that fails the build if
it does.

## 1. Config, state, and choosing jobs

**Depends on:** nothing.

**Changes:**
- `clients/github/config.go`:
  - `Config` with `Repos []Repo{Name, Clone}`, `Label`, `Poll`, `Quiet`, `MaxJobs` and
    `Review{Enabled, Pushes, MaxTurns}`. Durations are strings (`"2m"`) parsed with
    `time.ParseDuration`.
  - `Load(root string) (Config, error)` reads `<root>/github/config.json`.
  - Defaults: `label` `nabu`, `poll` 2m, `quiet` 5m, `max_jobs` 1, `review.enabled` true,
    `review.pushes` true, `review.max_turns` 30.
  - A missing file is an error saying where the file should be and what goes in it,
    because the watcher has nothing to do without repos.
  - A repo with no `clone`, or a `name` that is not `owner/repo`, is an error.
- `clients/github/state.go`:
  - `State{Repos map[string]*RepoState}`. A `RepoState` holds:
    - `Seen map[int]Seen{SHA, At}`: when each PR's current head was first seen. This is
      how the quiet period is measured, because GitHub does not say when a head was
      pushed. The spec's "the head for at least `quiet`" is implemented as "seen by the
      watcher for at least `quiet`".
    - `Reviewed map[int]Reviewed{SHA, Summary}`.
    - `Failed map[int]string`, holding the head SHA that failed.
    - `Running []Job`.
  - `LoadState(path)` returns an empty state for a missing file. `Save(path)` writes a
    temp file in the same directory and renames it over the old one, so a crash never
    leaves a half-written file.
- `clients/github/jobs.go`:
  - `PR{Number, HeadSHA, HeadRef, BaseRef, Title, Body, Draft, Fork}` and
    `Job{Kind, Repo, PR, SHA, SessionID, Worktree, Started}`.
  - `Observe(st *RepoState, prs []PR, now time.Time)` updates `Seen`. A new SHA resets
    `At`. Closed PRs are dropped from `Seen`.
  - `ReviewJobs(cfg Config, repo string, st *RepoState, prs []PR, now time.Time) []Job`:
    - skip drafts, forks, a PR with a job already running, a head that is in `Reviewed`,
      a head that is in `Failed`, and a head seen for less than `Quiet`;
    - with `Pushes` false, skip any PR that has anything in `Reviewed`;
    - order by PR number, oldest first.
  - `Admit(cfg, running int, queued []Job) []Job` returns the first
    `MaxJobs - running` jobs.

**Proves it:**
- `go test ./clients/github/` with:
  - `TestLoadConfig`, a table covering defaults, a missing file, a bad repo name, and a
    bad duration;
  - `TestStateRoundTrip`, and a test that `Save` leaves no temp file behind;
  - `TestReviewJobs`, a table with one row per rule above, plus a failed head followed by
    a new head (the new one is reviewed);
  - `TestObserve`, covering a new SHA resetting `At` and a closed PR being dropped;
  - `TestAdmit`, covering running jobs counting against `MaxJobs`.

## 2. Reading the review out of the final message

**Depends on:** nothing.

**Changes:**
- `clients/github/review.go`:
  - `ParseReview(final string) (Review, bool)` takes the last fenced `json` block and
    decodes `{summary, comments:[{path, line, body}]}`. It returns false if there is no
    block, or if the block does not decode.
  - `RightLines(diff string) map[string]map[int]bool` returns, for each file in a unified
    diff, the new-side line numbers inside a hunk: added lines and context lines, not
    removed lines.
  - `BuildReview(r Review, ok bool, final string, diff string, sha string) ReviewPost`:
    - comments that fall on right-side lines become line comments with `side: RIGHT`;
    - the rest are appended to the body as `path:line — body`;
    - when `ok` is false, the body is `final`;
    - `event` is `COMMENT`, `commit_id` is `sha`, and the body ends with
      `\n\n<!-- nabu -->`.
  - `const Marker = "<!-- nabu -->"`.
- `docs/specs/2026-09-28-github-review-design.md`: the diff used for splitting is
  `git diff origin/<base>...<sha>`, computed in the worktree before it is removed, rather
  than `gh pr diff`. `gh pr diff` shows the PR's current head, and that may no longer be
  the SHA that was reviewed. This sentence changes in the same commit.

**Proves it:**
- `TestParseReview`:
  - one block;
  - two blocks, where the last one wins;
  - prose around the block;
  - no block;
  - bad JSON;
  - a block tagged `json` versus an untagged one (only `json` counts).
- `TestRightLines` against a checked-in multi-file, multi-hunk diff in
  `clients/github/testdata/pr.diff`. It must include a deleted file and a renamed file.
- `TestBuildReview`:
  - a comment on an added line, on a context line, on a removed-only line number, on a
    line between hunks, and on a file not in the diff;
  - a parse failure, where the body is the final message;
  - every case ends with the marker.

## 3. The watcher: sessions, worktrees, posting

**Depends on:** steps 1 and 2.

**Changes:**
- `clients/github/ports.go` defines three interfaces, so the watcher can be tested without
  gh, git or a daemon:
  - `GitHub`: `OpenPRs(ctx, repo) ([]PR, error)` and
    `PostReview(ctx, repo, n, ReviewPost) error`.
  - `Git`: `FetchPR(ctx, clone, n, base) error`,
    `AddWorktree(ctx, clone, path, sha) error`,
    `Diff(ctx, dir, base, sha) (string, error)` and
    `RemoveWorktree(ctx, clone, path) error`.
  - `Daemon`: `Create(ctx, workspace, maxTurns) (string, error)`,
    `SendPrompt(ctx, id, text) error`, `State(ctx, id) (protocol.State, error)`,
    `Stop(ctx, id) error` and `Events(ctx, id) ([]protocol.Event, error)`.
- `clients/github/exec.go`: the real `GitHub` and `Git`, over a
  `Runner func(ctx, dir, name string, args ...string) ([]byte, error)`.
  - `OpenPRs` runs `gh pr list --repo <repo> --state open --limit 100 --json …`, with the
    fields from the spec plus `title,body`.
  - `PostReview` runs `gh api repos/<repo>/pulls/<n>/reviews --method POST --input -`,
    with the JSON on stdin. `Runner` gains a stdin argument for this.
  - `FetchPR` runs `git -C <clone> fetch origin pull/<n>/head <base>`.
  - `AddWorktree` runs `git -C <clone> worktree add --detach <path> <sha>`.
  - `RemoveWorktree` runs `git -C <clone> worktree remove --force <path>`.
- `clients/github/daemon.go`: the real `Daemon` over `*goclient.Client`.
  - `Create` sends `{workspace, options:{permission_mode:"auto"}, budget:{max_turns, source:"client"}}`.
  - It never calls `Subscribe`.
- `clients/github/prompt.go`: `ReviewPrompt(pr PR, base string, prev *Reviewed) string`,
  built from the spec's list: title, body, the diff command, "change no files", the
  earlier summary with the diff since the old SHA when `prev` is set, and the output
  format.
- `clients/github/watcher.go`:
  - `Watcher{Cfg, Root, GH, Git, Dial func(ctx) (Daemon, error), Now, DryRun, Log}`.
  - `Poll(ctx) error`. For each repo:
    1. Finish or reattach running jobs. A job is done when its state is `idle` and its
       log has an assistant `message`. That second condition covers the window between
       `Create` and the loop starting, when the state is also `idle`. A job has failed
       when its state is `blocked`, `paused`, `error` or `completed` without that message.
    2. Observe the PRs.
    3. Choose jobs and admit them.
    4. Start each admitted job. Record it in `Running` and save **before** `Create`
       returns an ID. Save again once the ID is known.
  - Finishing a job:
    1. `Stop`.
    2. Read the final message from `Events`.
    3. `Diff`, then `BuildReview`.
    4. Post it, or print it under `DryRun`.
    5. Only after a successful post: move the job to `Reviewed`, save, and remove the
       worktree.
    6. A failed post leaves the job in `Running`, marked `stopped`. The next poll posts
       it again from the same session's events, without calling `Stop` again or starting
       a new session.
  - A failed job goes to `Failed` with its SHA. Its worktree is kept, and its session ID
    is logged.
  - If `Dial` or `OpenPRs` fails, the poll returns the error and saves nothing.
  - Under `DryRun` the state path is `state.dry-run.json`.
  - `Run(ctx)` calls `Poll` every `cfg.Poll` until `ctx` is done.
    `RunOnce(ctx)` polls, then keeps polling every 10 s until nothing is running.
- `clients/github/boundary_test.go` fails if any file in `clients/github` imports
  `github.com/corporealshift/nabu/daemon/…`.

**Proves it:**
- `TestWatcher*` in `watcher_test.go`, using in-memory fakes of the three ports and a
  fake clock:
  - a new PR, once past the quiet period, creates a session in a worktree at the PR's SHA
    with `permission_mode: auto` and the review budget, and never subscribes;
  - an `idle` session with an assistant message is stopped, its review is posted with
    `commit_id` set to the reviewed SHA and ending with the marker, the SHA is recorded,
    and the worktree is removed;
  - `idle` with no assistant message yet is left running;
  - `blocked` records a failure, keeps the worktree, and posts nothing;
  - a failed post records nothing as reviewed, and the next poll posts again without a
    second `Create` or `Stop`;
  - a restart between `Create` and `Save` is reattached from the saved session ID. The
    test builds a second `Watcher` from the same state file;
  - `DryRun` writes `state.dry-run.json` and never calls `PostReview`;
  - with `max_jobs` 1 and two eligible PRs, the first poll starts one session;
  - if `Dial` fails, nothing is saved.
- `TestExecArgs`: a recording `Runner` checks the exact argument lists and the stdin JSON
  of every real `GitHub` and `Git` method.
- `TestReviewPrompt` checks that the prompt holds the title, the diff command, "change
  no files", the output format, and, when `prev` is set, the earlier summary and the old
  SHA.
- `TestBoundary`.

## 4. `nabu github`

**Depends on:** step 3.

**Changes:**
- `cmd/nabu/github.go`: `cmdGitHub(args, stdout, stderr) int` with `--once`, `--dry-run`
  and `--root`.
  - It loads the config, builds the real ports with an `exec`-backed `Runner`, and dials
    through `connect`, so a stopped daemon is started as it is for `run`.
  - It runs until Ctrl-C (`signal.NotifyContext`), or runs `RunOnce` under `--once`.
  - Every job start, finish, failure and post goes to stderr as one line with the repo,
    PR number, SHA and session ID.
- `cmd/nabu/main.go`: `case "github"` and a usage line.
- `ARCHITECTURE.md`: `clients/` in the layout gains `github/`, described as the GitHub
  watcher: it polls, runs sessions, and posts reviews.

**Proves it:**
- `TestGitHubWithoutConfigSaysWhereItGoes`: an empty root exits with `exitError`, and
  stderr names `github/config.json`.
- `TestHelpWordPrintsUsage` is extended so that the usage text includes `github`.
- The full gate.

## 5. Checking it for real

**Depends on:** step 4. This step changes no code, and its results go in the PR
description.

1. Create a scratch repository on GitHub with one open PR that has a deliberate bug. Add
   it to `config.json`, run `nabu github --once --dry-run`, and read the printed review.
2. Run `nabu github --once` on the same repository. Check on GitHub that there is one
   `COMMENT` review, that its line comments are on the right lines, and that the marker is
   in the source. Run it again and check that nothing is posted, because the SHA is
   recorded.
3. Push a commit to the PR, wait out the quiet period, and run again. Check that the
   second review refers to the first review.
4. Run `nabu github --once --dry-run` against breezeway, and read the printed reviews for
   its open PRs.

## Not doing in this plan

- Comment and issue jobs. Those are parts 2 and 3, on their own branches.
- Running the watcher as a Windows service, or registering it with Task Scheduler.
  `--once` makes either possible, and setting one up is Kyle's choice.
- Rate limiting. At a 2-minute poll, one `gh pr list` per repo is far below GitHub's
  limits.
- Cleaning up worktrees kept from failed jobs. They are kept on purpose, and `git
  worktree remove` is the manual cleanup.
