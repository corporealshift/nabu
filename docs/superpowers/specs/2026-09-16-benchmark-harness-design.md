# Benchmark harness — measuring task completion quality across harnesses

**Date:** 2026-09-16
**Author:** Kyle (corporealshift), with Claude
**Status:** Approved, not yet planned

## 1. Purpose

A tool the owner runs by hand to compare how well three coding-agent harnesses
complete the same tasks: **nabu** (the harness being built), **pi** (the incumbent),
and **Claude Code** (an outside reference point).

The expected ordering at the start is Claude > pi > nabu. The point of the tool is
not an absolute score. It is **the size of the gap and whether it is closing** as
nabu is tuned. A run that says "nabu 4/6" is useless without "pi 5/6, and last week
nabu was 2/6".

### Non-goals

- **Not CI.** It is never wired into a build, never gates a merge, and its exit code
  does not mean "the harness failed". It prints a report; that is all it does.
- Not a public or comparable-to-published benchmark. The numbers mean something only
  against this tool's own earlier runs.
- Not a way to score arbitrary ad-hoc tasks. The suite is fixed fixtures.

## 2. The model confound, and why it is not one here

nabu and pi both run on the same llama-server at `http://localhost:8033/v1` with the
same Qwen3.6-35B model. nabu vs pi is therefore a harness-only comparison: same model,
same machine, same endpoint. Claude Code runs on Claude and is understood to be a
different stack entirely — it is a reference ceiling, not a like-for-like competitor.

The local provider is configured `max_in_flight: 1`, so **runs are strictly
sequential**. The tool never runs two harnesses concurrently.

## 3. Where it lives

A Go program inside the nabu repository: package code under `bench/`, entry point
under `cmd/nabubench/`. It is covered by the repository's existing gate
(`go build ./... && go vet ./... && go test ./...`).

Rejected: a separate repository (neutral ground, but a second repo to maintain
outside the gate for a tool one person runs). Rejected: shell or Python scripts
(fastest to write, but this tool produces numbers that decisions are made on, and
untested glue driving three CLIs with process-orphan hazards on Windows would
quietly produce wrong results).

Living inside nabu does not privilege nabu: the tool shells out to the `nabu` CLI
exactly as it shells out to `pi` and `claude`, through the same interface.

## 4. Fixtures

Tasks are purpose-built, not drawn from real repositories and not from a public
benchmark suite. Rejected: snapshots of the owner's real repos (measures real work,
but each task is expensive to author and slow to run); rejected: SWE-bench-style
public tasks (comparable to published numbers, but Python-heavy, tuned to frontier
models, and a 35B local model would likely score near zero on all three harnesses,
which teaches nothing).

Known and accepted limitation: **purpose-built tasks reward purpose-built behaviour.**
A harness that wins here may not win on real work. This is worth revisiting once
there are results to look at.

Layout: `bench/tasks/<id>/` contains `task.json` and `repo/`.

`repo/` is a git repository. Git is the mechanism for two things: the diff the judge
reads, and detecting which files were touched.

`task.json` fields:

- `id` — the task's identifier
- `prompt` — the instruction given verbatim to every harness
- `verify` — argv of the command proving the task done, run in the workspace
- `unchanged` — files the task forbids modifying (for example the test that defines
  success). A file counts as changed if it appears in `git diff --name-only` against
  the fixture's HEAD, or if it has been deleted. Content is not compared: a file
  rewritten to be byte-identical still counts as untouched, which is the right answer
  because nothing was actually altered.
- `max_turns` — turn cap, applied where the harness supports one
- `timeout_seconds` — wall-clock cap, applied to every harness

Example:

```json
{ "id": "cursor-off-by-one",
  "prompt": "TestCursorAfter fails. Make it pass without changing the test.",
  "verify": ["go", "test", "./..."],
  "unchanged": ["cursor_test.go"],
  "max_turns": 12,
  "timeout_seconds": 900 }
```

## 5. Harness adapters

One `Harness` interface with three implementations. Each returns: exit code,
wall-clock duration, and turns and token usage where the CLI reports them.

Every harness is invoked with an argv slice and never through a shell. Prompts
contain quotes, backslashes and newlines, and a shell would mangle them differently
for each of the three CLIs — which would mean the three were not given the same task.

The three invocations, all confirmed to exist on the owner's machine:

- **nabu** — `nabu run --root <isolated root> --workspace <ws> --max-turns N
  --json "<prompt>"`. The `--json` stream carries the report event, which already
  gives exit status, tasks done/total, checks, files touched, commits and
  tree-dirty.
- **pi** — `pi.cmd -p --mode json "<prompt>"` run in the workspace. **Must be
  invoked as `pi.cmd`**, not `pi.ps1`: PowerShell execution policy on this machine
  blocks the `.ps1` shim. The JSON shape pi emits has not been inspected yet, so
  this adapter may begin reporting wall-clock time only and gain turns and tokens
  later.
- **claude** — `claude -p "<prompt>" --output-format json --permission-mode
  bypassPermissions --model <pinned>`. Its JSON reports `num_turns`, `duration_ms`,
  `total_cost_usd`, `usage` and `permission_denials`. The model is pinned in the
  runner's config rather than left to the CLI default, and recorded in the result
  file: Claude is the reference ceiling, and a ceiling that moves when the default
  changes underneath makes every earlier comparison meaningless.

## 6. Isolation and hazards

- A fresh copy of `repo/` per run, into a temporary workspace. Never the fixture
  itself.
- nabu runs against an **isolated `--root`**, so the benchmark never touches the
  owner's real `~/.nabu` sessions or config.
- The `web` module is **disabled** for benchmark runs. Otherwise results move with
  the internet and are not comparable between runs.
- **pi survives its launcher.** A pi process continues running detached after the
  command that started it reports completion. The runner MUST confirm that zero
  `pi-coding-agent` processes are alive before starting each run; otherwise one
  task's agent contaminates the next task's workspace. Kill by matching the process
  command line, which contains `pi-coding-agent`, not by process name, which is
  `node.exe`.
- Killing processes by name is forbidden in this tool for the same reason: it would
  kill unrelated processes belonging to the owner.

## 7. Scoring

Three dimensions, reported separately. They are deliberately **not** collapsed into
a single weighted composite: a composite hides which of the three moved.

### 7.1 Completed

The `verify` command exits 0, *and* every file listed in `unchanged` is genuinely
unchanged. Pass rate is the headline figure.

### 7.2 Cost

Wall-clock time, turns, tokens, and dollars for Claude.

### 7.3 Craft

A judge reads the diff against a fixed rubric and returns JSON with three integer
scores from 1 to 5, each with a one-line reason:

- `minimal` — is the change no larger than the task required
- `tested` — were tests added or extended where the change warranted it
- `focused` — is the diff free of unrelated churn: reformatting, renames, drive-by edits

Craft is scored only for runs that passed verification. Scoring the craft of a change
that does not work rewards writing tidy code that fails.

A judge call that fails or returns unparseable JSON records craft as unscored for that
run. It never fails the run and never blocks the report: the objective half stands on
its own.

#### The judge

Claude, via `claude -p`, one call per run rather than a whole agent loop. Rejected:
the local Qwen (free and offline, but a 35B judging work produced by the same 35B
is a weak referee, and it would be judging Claude's diffs too); rejected: running
both judges and comparing (informative once, too expensive every run).

**Claude is also one of the three subjects.** Runs produced by the Claude harness
and scored by the Claude judge MUST be labelled `self-judged` in the report. The
tool does not pretend this is neutral.

#### Suspect tasks

Claude is expected to pass essentially every task. Therefore **a task Claude fails is
more likely to be a broken task than a hard one.** The report flags such tasks as
suspect and excludes them from the headline pass rates, rather than letting a bad
fixture quietly drag all three harnesses down and read as a harness problem.

## 8. Variance

The local model is non-deterministic, so a single run proves little. `--repeat`
defaults to 3; the report gives pass rate and median duration across repeats.
`--repeat 1` exists for a quick loop after changing something in nabu.

The cost is worth stating plainly: six tasks times three harnesses times three
repeats is 54 sequential local-model runs, likely a few hours. The suite is meant
to stay small and the tasks short.

## 9. Results and comparison

Each run writes a timestamped result file under `bench/results/`. Because the
purpose is tracking a gap over time, the tool supports `--compare <earlier result>`,
which reports movement per task and per harness since that run. This is the primary
use, not an extra.

## 10. Failure handling

Four outcomes are recorded distinctly, and none of them is a panic or a crash:

- the harness completed and verification passed
- the harness completed and verification failed
- the harness hit its timeout
- the harness itself crashed, or the `verify` command could not be run at all (a
  fault in the fixture rather than in the harness)

Note: pi returning exit code 1 does not by itself mean the run failed — its server
connection drops mid-run routinely. Judge pi by what the workspace contains, not
by its exit status.

## 11. Testing

- A fake harness adapter that solves some tasks and fails others, so the whole
  pipeline is testable without invoking a model.
- Table tests for scoring and for the `unchanged` constraint check.
- The judge sits behind an interface so tests stub it; no test calls a real model.

---

## Decisions and rejected alternatives

| # | Decision | Chosen | Rejected and why |
|---|---|---|---|
| 1 | Location | Inside nabu repo, `bench/` + `cmd/nabubench/` | Separate repo (neutral ground, but a second repo to maintain); shell or Python (fast, but untested glue driving three CLIs with process-orphan hazards on Windows would quietly produce wrong results) |
| 2 | Fixture source | Purpose-built tasks in `repo/` directories | Real-repo snapshots (expensive to author, slow to run); SWE-bench-style public tasks (Python-heavy, tuned to frontier models, 35B local model scores near zero on all three) |
| 3 | Model for nabu vs pi | Same llama-server at `localhost:8033/v1`, same Qwen3.6-35B | Different models (would confound harness differences with model differences) |
| 4 | Scoring composite | Three separate dimensions (completed, cost, craft) | Single weighted score (hides which of the three moved) |
| 5 | Judge | Claude via `claude -p`, one call per run | Local Qwen (35B judging its own diffs is a weak referee); dual judges (informative once, too expensive every run) |
| 6 | Concurrency | Strictly sequential, never two harnesses at once | Parallel runs (faster, but `max_in_flight: 1` on the local provider means they would queue anyway, and workspace contamination risk) |
| 7 | pi invocation | `pi.cmd`, not `pi.ps1` | `pi.ps1` (PowerShell execution policy blocks the shim on this machine) |
| 8 | Process cleanup for pi | Kill by command line (`pi-coding-agent`), not by name | Kill by name `node.exe` (would kill unrelated processes belonging to the owner) |
| 9 | Variance handling | `--repeat` defaults to 3, reports median | Single run (non-deterministic local model proves nothing) |
| 10 | Claude's model | Pinned in config and recorded per run | CLI default (a reference ceiling that moves underneath makes earlier comparisons meaningless) |
| 11 | Craft scoring scope | Only runs that passed verification | Scoring every run (rewards tidy code that does not work) |
| 12 | Failure semantics | Four distinct outcomes, no panics | Exit-code-only judgment (pi's exit code 1 can mean a dropped connection, not failure) |
