# bench

Compares how well nabu, pi and Claude Code complete the same tasks.

Run by hand, never in CI. The exit code says whether the suite ran, not whether
any harness did well: there is no score to fail.

```bash
go run ./cmd/nabubench                        # nabu vs pi, the whole suite
go run ./cmd/nabubench --task 07-needle --repeat 1
go run ./cmd/nabubench --claude               # add the reference, spends Claude quota
go run ./cmd/nabubench --compare bench/results/bench-20260917-020000.json
```

Claude is opt-in. It is the reference the suite trusts, and the judge that scores
craft, but a routine comparison of the two local harnesses needs neither and a
full run of both costs several dollars. Without it there is no reference, so no
task is checked for being broken, and the report says so.

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

## Tiers

`basic` tasks confirm a harness works at all. `hard` tasks are the ones meant to
tell good harnesses apart, and they are reported on their own line: a tier every
harness passes cannot show movement, and averaging it into the headline hides
the difference you are looking for.

If the hard tier stops discriminating, it needs harder tasks, not a new metric.

## Held-back checks

A check the harness can run is a check it can grind against: change something,
run the command, read the assertion, try again. That measures persistence, and
every harness has it, which is why the first ten tasks could not tell any of
them apart.

A task may therefore keep part of its check in , copied over the
workspace only after the harness has finished. The harness has to satisfy a
contract it cannot read, from a prompt that deliberately does not spell out
every edge, which is the thing worth measuring.

Such a task also ships : the change that satisfies the held-back
check.  applies it and insists the check goes
quiet, and  insists the visible
fixture does not already pass. Together they are the evidence the task is
sound, and they cost nothing — which matters now that the reference harness is
opt-in.

Both directories begin with an underscore so the go command leaves them out of
this repository own build.

## Adding a task

`bench/tasks/<id>/task.json` plus a `repo/` holding the fixture.

```json
{ "id": "07-something",
  "prompt": "what every harness is asked, verbatim",
  "verify": ["go", "test", "./..."],
  "tier": "hard",
  "unchanged": ["thing_test.go"],
  "max_turns": 14,
  "timeout_seconds": 900 }
```

Give the fixture its own `go.mod`. It keeps deliberately broken code out of the
repository's own build.

`TestEveryFixtureStartsFailing` will tell you if the new fixture already passes,
which would hand every harness a free point.
