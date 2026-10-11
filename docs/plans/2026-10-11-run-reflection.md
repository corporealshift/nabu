# Run reflection: plan

Spec: `docs/specs/2026-10-11-run-reflection-design.md`.

All of it is on one branch, `run-reflection`, as one PR with a commit per step, so no
branch stacks on an unmerged one. It does not depend on PR #163 (the curator's token
cap): the two touch different lines of the memory module and can merge in either
order. Each step leaves `gofmt -l .` empty and `go build ./... && go vet ./... &&
go test ./...` green.

### 1. The memory module skips labeled sessions

- **Changes:** `daemon/modules/memory/`:
  - `memory.go`: `NoCuratorLabel = "memory:no-curator"`, and a `curated(s)` check that
    reads `s.State().Options.Labels`;
  - `SessionEnd` and `BeforeCompaction`: no curator pass and no "in progress" memory
    for a labeled session. Its own saves are still committed.
- **Proves it:** new tests in `curator_test.go`:
  - a labeled session makes no model call at session end or before compaction;
  - it writes no "in progress" memory with open tasks;
  - a `memory.save` it made is still committed;
  - an unlabeled session is unchanged.
- **Depends on:** nothing.

### 2. The runner's sessions carry the label

- **Changes:** `clients/runs/daemon.go`: `NoCuratorLabel`, added to `Create`'s labels,
  with a comment saying why.
- **Proves it:** a test that `Create`'s options carry `guard:no-push`, `unattended` and
  `memory:no-curator`, using a recording JSON-RPC client if one exists, or the request
  `Create` builds, split into its own function so it can be tested.
- **Depends on:** nothing. The label is harmless until step 1 reads it.

### 3. A run keeps its history

- **Changes:** `clients/runs/`:
  - `state.go`: `Record` (step, task, session, ok, why, note) and `Run.History`;
  - `transition.go`: `Outcome.Note`;
  - `runner.go`:
    - `perform` sets the note: the end of a failed check's output, the final review's
      blockers, Claude's refusal, the CI failure;
    - `advance` appends a record for each finished step before `Transition` clears the
      session. A CI poll still pending adds nothing. Notes are cut to a bound, and
      the history to a bound.
- **Proves it:** `runner_test.go`:
  - a run driven through work, a failed check, a fix and a final review with blockers
    has one record per step, with the sessions and notes;
  - a long check output's note is cut.
- **Depends on:** nothing.

### 4. The digest and the prompt

- **Changes:** `clients/runs/`:
  - `reflect.go`: `Digest(r Run, brief, tasks string, logs map[string][]protocol.Event) string`.
    It holds the outcome, the brief, the tasks, each record with its session's turns,
    and every `claude.ask` answer with the step and task that asked. Bounded at
    60,000 characters.
  - `prompts.go`: `ReflectPrompt(r Run, digest string)`.
- **Proves it:**
  - `reflect_test.go`: the digest holds each of those parts, counts turns, pairs each
    `claude.ask` call with its result, and stays within the bound on a huge input;
  - `prompts_test.go`: a `reflect` row checks for "what worked", for lessons written
    as what to do, for "at most 5", for `memory.save`, and that saving nothing is
    allowed. It also has the shared phrases every session prompt has.
- **Depends on:** step 3 (`Record`).

### 5. The `reflect` step

- **Changes:** `clients/runs/`:
  - `state.go`: `StepReflect`, a session step;
  - `transition.go`:
    - `finish(r)` sends a run with a session in its history to `reflect`, and any
      other run to `done` or `failed`;
    - `fail` records `failed_at` and `why`, then finishes;
    - `merge` and `ci` finish instead of entering `done`;
    - `reflect`, whatever came of it, goes to `failed` when `failed_at` is set and to
      `done` otherwise, with no retry;
  - `config.go`: `ReflectTurns`, default 30;
  - `runner.go`:
    - `begin` uses `ReflectTurns` for `reflect`;
    - `prompt` builds the digest from the history and `d.Events` of each session;
    - `produced` resets a reflection's worktree to its start and reports OK;
    - `advance` logs a failure when `failed_at` is first set, not when `failed` is
      reached.
  - `docs/specs/2026-09-30-orchestrated-runs-design.md`: a dated amendment adding the
    step to the step table.
- **Proves it:**
  - `transition_test.go` rows: `merge` → `reflect` → `done`; a failure → `reflect`
    (with `failed_at` and `why`) → `failed` with the same `why`; a `reflect` that did
    not work still reaches the outcome; a run with no session history skips
    `reflect`; `Resume` of the failed run still works;
  - `runner_test.go`: a run end to end with the fake daemon starts a reflect session
    with the digest in its prompt, at the reflect turn cap, then ends `done`. A
    reflection that commits a file leaves the worktree at its start;
  - existing tests that expected `done` or `failed` straight away are updated to pass
    through `reflect`.
- **Depends on:** steps 3 and 4.

## Not doing

- An off switch for reflection. It can be added if it misbehaves. Until then,
  `reflect_turns` bounds its cost.
- Memory growth and curation across runs. Kyle wants that as its own discussion once
  reflection has run for a while.
- `docs/configuration.md` lives on the unmerged `docs-readme` branch (PR #161).
  `reflect_turns` goes there once that merges, not by stacking this branch on it.
- Reflection for goals as a whole. A goal's runs each reflect, and the goal's own
  steps are Claude calls, not sessions.
