# Runs from issues

**Date:** 2026-09-30
**Status:** Approved by Kyle in conversation
**Part 4 of 4** of orchestrated runs (`2026-09-30-orchestrated-runs-design.md`).
**Supersedes** `2026-09-28-github-issues-design.md`, which was never merged; it is on the
unpushed `github-issues` branch. That spec had one long session work an issue under a
goal, and the local model does badly at exactly that.

## Problem

Kyle wants to label an issue and come back to a finished PR. Orchestrated runs already go
from a brief to a green PR. An issue is a brief that arrives from GitHub instead of the
TUI.

## Decision

The watcher starts a run for each open issue carrying the `label` from config (`nabu`)
that has no run yet.

### Starting a run

1. **Create a home session** in the repository's clone. It is never prompted: it exists
   so the run has somewhere to hang in the TUI and a goal to carry.
   - Its labels: `run:requested` and `issue:<owner>/<repo>/<n>`, lower-cased, because
     labels allow only `[a-z0-9:_./-]` (part 1).
   - Its goal: the brief, as below.
2. **The brief** is the issue's title and body, then each comment on the issue that does
   not carry the marker, oldest first. It is committed as `brief.md` by the `brief` step,
   exactly as a `/run <text>` brief is. The text is cut at 16,000 characters, with a note
   where it was cut.
3. **The run's slug** is `issue-<n>-<title, lower-case, dashes, at most 40 characters>`,
   so the branch is `nabu/issue-<n>-…`.

From then on it is an ordinary run: the plan, the Claude gates, the tasks and the fix
loop. It follows the `issue:` label only at two points:
- **At `pr`:** the PR body starts with `Closes #<n>`, and the PR carries the `nabu` label.
- **At the end:** the watcher comments on the issue once. At `done` the comment links the
  PR. At `failed` it says which step the run stopped at, and that editing the issue or
  commenting on it starts it again. Both end with the signature and the marker.

### One run per issue

The watcher's state records the home session for each issue. An issue with a run, in any
state, gets no second run, except:
- **The run failed, and the issue was updated since** (`updated_at` is newer than the
  failure). The run resumes at its failed step, as `/run` on a failed run does. The
  issue's new comments are appended to `brief.md` as the first commit of the resumed run,
  and the goal is set again.
- **The run is done and its PR was closed without merging.** The run is over, and the
  issue needs a new run. It gets one only once it is updated after the close.

## How it fails

| What | Then |
|---|---|
| The issue is too vague to plan | The `plan` session says so. The run fails at `plan`, and the watcher's comment on the issue says the plan step could not proceed and quotes the session's last message. |
| The label is removed while the run is going | The run continues. Removing the label stops new runs, not this one. `nabu stop <home>` is how a run is stopped. |
| The issue is closed while the run is going | The run continues to `pr`, whose `Closes #<n>` does nothing. Stopping a run because an issue closed is a policy nobody has asked for. |

## Not doing

- Assigning issues, or changing their labels.
- Grouping several issues into one run.
- Working issues without the label.

## Rejected

- **The old spec's single session with a goal.** It is superseded; see the header.
- **Posting to the issue at every step.** One comment at the end says what happened. The
  TUI is where a run in progress is watched.
- **Silence on failure.** The old spec posted nothing when it failed. Here the run's
  failure is specific (a step, and the session's last word), and the issue is where
  whoever filed it will look.

## What proves it works

- The watcher's job function, table-tested:
  - a labeled issue gets a run;
  - an issue with a running or done run gets nothing;
  - a failed run resumes only after `updated_at` moves;
  - a closed PR and a later update give a new run.
- Building the brief: comments carrying the marker are skipped, the text is cut, and the
  slug is built.
- The PR body starts with `Closes #<n>`, and the closing comment is right for `done` and
  for `failed`.
- Live: a labeled issue on the scratch repository, run to a green PR.
