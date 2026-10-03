# Runs from issues — plan

Spec: `docs/specs/2026-09-30-runs-from-issues-design.md`. Branch `runs-issues`, and one
PR. **This branch is stacked on `runs-ci` (part 3, not yet merged)**, because both change
the runner's `pr` step and its end of run. #127 (part 3) must merge first, and then this
branch is rebased onto `main` before it merges.

## Changes to the spec in this plan

- **The runner posts the closing comment, not the watcher.** The runner is what sees a run
  become `done` or `failed`, so it comments on the issue then. The watcher would have to
  poll every run's labels to find that out.
- **Deferred:** "a done run whose PR was closed without merging gets a new run once the
  issue is updated". The main path, and resuming a failed run, come first. Until then,
  re-labeling such an issue does nothing, and the spec says so.

## 1. The runner knows its issue

**Depends on:** nothing.

**Changes:** `clients/runs`:
- `Run` gains `Issue` and `Reported`.
- `pickUp`:
  - reads `issue:<owner>/<repo>/<n>` from the home's labels;
  - an issue run's slug is `issue-<n>-<title>`;
  - on a resume, it rewrites `brief.md` when the home's description has changed, in a
    commit `run: brief updated from the issue`.
- `pr`: the body starts with `Closes #<n>`.
- When a run ends, `done` or `failed`, and has an issue it has not yet reported on, it
  comments on the issue once.
  - At `done`, the comment links the PR.
  - At `failed`, it gives the step and the reason, and says that editing the issue or
    commenting on it starts the run again.
  - Both end with the signature and the marker.
  - Resuming clears `Reported`.
- `GH` gains `CommentIssue`, over `gh issue comment <n> --body-file -`.

**Proves it:**
- An issue run from pickup to `done`: the slug, `Closes #n`, and one comment linking the
  PR.
- A failed issue run: one comment naming the step.
- A resume with a changed description: `brief.md` is rewritten and committed, and a
  second failure comments again.
- `TestExecArgs` gets a row for `CommentIssue`.

## 2. The watcher starts runs for labeled issues

**Depends on:** step 1, for the label and description the runner reads.

**Changes:** `clients/github`:
- Config gains `issues.enabled` (default true).
- `GitHub` gains:
  - `OpenIssues(repo, label)`, over `gh issue list`;
  - `IssueComments(repo, n)`.
- `Daemon` gains:
  - `CreateHome(workspace, description, labels)`: `session.create` with those options. It
    sends no prompt and sets no goal, so the session stays idle;
  - `Labels(id)`;
  - `Rerun(id, description, labels)`.
- `RepoState` gains `Issues map[int]IssueRun{Home, Updated}`.
- `IssueBrief(issue, comments)`: the title, the body, a line naming the issue, then the
  comments that don't carry the marker, cut to 16,000 bytes.
- `Poll`:
  - an issue with no run gets a home in the repository's clone, with the brief as its
    description, labeled `run:requested` and `issue:<owner>/<repo>/<n>`, lower-cased;
  - an issue whose run is labeled `run:failed`, and that was updated since its record,
    is re-run: its description is the new brief, and it is labeled `run:requested`
    again.
  - Neither takes a job slot: the runner's sessions do.

**Proves it:** watcher tests:
- a labeled issue gets one home with the right description and labels, and a second poll
  makes no second home;
- a failed run is re-run only after the issue is updated;
- a run that is not failed is never touched;
- issues off means no homes;
- the brief skips comments carrying the marker, and is cut at the limit.

## 3. Checking it for real

With `<root>/github/config.json` pointing at the scratch repository and `nabu runner
--once`:
1. Open an issue labeled `nabu` asking for a small function.
2. Check that the home is created, the run goes to an open PR whose body starts
   `Closes #n`, CI is watched, and the issue gets one comment linking the PR.

## Not doing

- New runs for done runs whose PR was closed (deferred; see above).
- Assigning issues, or changing their labels.
