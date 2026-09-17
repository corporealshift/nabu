# bench

Compares how well nabu, pi and Claude Code complete the same tasks.

Run by hand, never in CI. The exit code says whether the suite ran, not whether
any harness did well: there is no score to fail.

```bash
go run ./cmd/nabubench                        # the whole suite, 3 repeats
go run ./cmd/nabubench --task 01-off-by-one --only nabu --repeat 1
go run ./cmd/nabubench --compare bench/results/bench-20260917-020000.json
```

`--nabu <path>` measures a particular build rather than whatever is on PATH,
which is what you want when comparing a change to nabu against its predecessor.

## What it measures

Three things, kept separate, because a composite hides which one moved:

- **completed** — the task's `verify` command passes *and* the files it forbids
  changing are untouched.
- **cost** — wall time, turns, tokens, and dollars for Claude.
- **craft** — Claude scores the diff 1–5 on minimal, tested and focused. Only
  runs that passed are judged: grading the elegance of a change that does not
  work rewards tidy failure.

Claude judges Claude's own runs. Those are labelled `(self)` rather than
presented as neutral.

A task Claude fails is marked `?` and left out of the headline rates. Claude is
expected to pass everything, so its failure is evidence about the task.

## Adding a task

`bench/tasks/<id>/task.json` plus a `repo/` holding the fixture.

```json
{ "id": "07-something",
  "prompt": "what every harness is asked, verbatim",
  "verify": ["go", "test", "./..."],
  "unchanged": ["thing_test.go"],
  "max_turns": 14,
  "timeout_seconds": 900 }
```

Give the fixture its own `go.mod`. It keeps deliberately broken code out of the
repository's own build.

`TestEveryFixtureStartsFailing` will tell you if the new fixture already passes,
which would hand every harness a free point.
