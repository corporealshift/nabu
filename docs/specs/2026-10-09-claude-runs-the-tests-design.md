# Claude may run the tests

**Date:** 2026-10-09
**Status:** Approved by Kyle in conversation
**Amends:** `docs/superpowers/specs/2026-09-19-asking-claude-design.md` §4 ("the cost is
that Claude cannot run the tests"), for the `claude` module only.

## Problem

The reviewer was made read-only on purpose, and that still holds: a reviewer that can
edit the repository removes what a review is for. The cost was put this way: "Claude does
not need a shell to know a test failed; it needs the failure text and the code that
produced it."

The automatic asks from 2026-10-08 and 2026-10-09 show that this cost is higher than
assumed. Across two liftoff sessions, all four of Claude's answers came with the same
caveat: "I couldn't run Gradle here because it needs approval, so this comes from reading
the code." Claude worked from whatever result files happened to be on disk:

- One answer started from a results file written before the model's latest edits. Claude
  had to argue that "the failing verify run was testing the last commit, not the current
  edits" instead of running the tests and seeing.
- Another found a layout bug (a button laid out with zero width) and could only offer
  "Step 1 below confirms it with one print" for the model to try.
- Every diagnosis of a Robolectric timing or layout problem was a reading of the code.
  One run of the test would have settled it.

When nabu asks on its own, the question carries the last failures. But the model keeps
editing while Claude reads, and the failure text is a snapshot that goes stale.

## Decision

The `claude` module gets a setting, `commands`: command prefixes Claude may run, such as
`bash gradlew.sh` or `go test`. It defaults to none, which keeps today's behaviour.

```json
{ "modules": { "claude": { "commands": ["bash gradlew.sh", "./gradlew", "go test", "go vet", "go build"] } } }
```

- Each prefix becomes one more `--allowedTools "Bash(<prefix> *)"` for the CLI, beside
  `Read`, `Grep` and `Glob`. Claude still cannot write or edit files, and it cannot run
  any other command.
- **Both kinds of ask get them**: the model's own `claude.ask` calls and the automatic
  asks at 100 and 200 turns. The automatic asks are where every one of the caveats came
  from.
- **The tool's description says what Claude can run** when any commands are set, in
  place of "It cannot run commands: if a test matters, run it yourself and put the output
  in the prompt."
- **The automatic question says so too.** Its closing line tells Claude it may run those
  commands to see the failure for itself. It also says the model keeps working in the
  same checkout meanwhile, so a build may wait on a lock, and a result file may change
  under it.

### A prefix that would allow anything is refused

A prefix that is only a shell or an interpreter (`bash`, `sh`, `pwsh`, `powershell`,
`cmd`, `python`, `node` and the like), or one that hands a shell a script to run
(`bash -c`, `pwsh -Command`), would let Claude run anything at all. Such a prefix is
dropped and logged. The rest still apply. `bash gradlew.sh` is fine: the shell runs one
named file.

### Running alongside the model

The automatic ask runs in the background (`2026-10-07-ask-claude-when-stuck-design.md`),
so Claude's build and the model's can overlap in one checkout. Kyle chose to accept that
over making the session wait. Gradle and `go` lock what they share, so one usually waits
for the other. At worst a build fails on a lock timeout and is run again. A model's own
`claude.ask` blocks its turn, so it never overlaps.

## Rejected

- **Putting `Bash(...)` in `allowed_tools`.** That works today with no code. But the tool
  description would still tell the model Claude cannot run anything, and nothing would
  stop a prefix that allows every command.
- **Running only the run's `verify.sh`.** The module does not know which run a session
  belongs to, and `verify.sh` deletes the test results before it runs, which is exactly
  what would trip up the model working alongside.
- **The model's own asks only.** There is no overlap that way, but the automatic asks,
  where every caveat came from, would stay as blind as they are now.
- **Pausing the session while the automatic ask runs.** That undoes the background
  design for minutes at a time, to avoid an overlap that Gradle's locks mostly handle.
- **The runner's Claude gates** (`plan-review`, `verify-review`, `revise`,
  `final-review`). They stay read-only for now. `verify-review` and `check` already run
  the script and hand Claude the output. `revise` could use it, which is a separate
  change if it proves worth it.

## What proves it works

- `daemon/modules/claude`, table-driven:
  - no `commands`: the argv is unchanged (`Read`, `Grep`, `Glob`, no `Bash`), and the
    description still says Claude cannot run commands;
  - `commands` set: one `Bash(<prefix> *)` per prefix; the description names them; the
    automatic question names them and warns about the checkout it shares;
  - `bash`, `pwsh -Command`, `bash -c` and blank entries are dropped, and the others kept.
- The full gate.
- Verified against the real CLI: with `Bash(go test *)` allowed, Claude runs `go test`
  in a scratch module, and its attempts to write a file or run another command are
  blocked.
