# nabu P1c — `verify` and `report` Modules Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> This plan specifies contracts and behaviours, not code. Write the failing test first
> from the stated behaviour, then the implementation. If a requirement is ambiguous or
> contradicts the real code, STOP and report rather than guessing.

**Goal:** Stop the agent declaring victory early: refuse to finish on open tasks, a failing gate or a dirty tree, judge the run goal in a fresh context, and report what actually happened in the workspace.

**Architecture:** Two in-process modules. `verify` implements `ToolGate`, `StopGate` and `Reporter`: it enforces done_when on tasks, runs mechanical checks, vetoes at the stop gate, and asks a judge about the run goal. `report` implements `Reporter` and observes the workspace with git. Both fill one `report` event, which is why they are planned together.

**Tech Stack:** Go 1.26, standard library only. Git is invoked as a subprocess. No new dependencies.

---

## Decisions settled by this plan

The spec leaves several policy details open. This plan decides them:

### How the judge is prompted and how its answer is parsed

**Prompt structure.** The judge prompt is a single user message with four sections:

```
You are an impartial judge. The agent's work is done. Decide whether the goal is met.

## Condition
<goal.Condition>

## Tasks
- t1: <title> — status: <status>, evidence: <evidence_id or "none">
- t2: <title> — status: <status>, evidence: <evidence_id or "none">

## Recent transcript (last K turns)
<turn N-2 user>: ...
<turn N-2 assistant>: ...
<turn N-1 user>: ...
<turn N-1 assistant>: ...

## Verdict
Respond with exactly one of these three lines, nothing else:
VERDICT: met
VERDICT: unmet — <one sentence reason>
VERDICT: impossible — <one sentence reason>
```

**Response parsing.** The judge's response is scanned for the literal string `VERDICT: met`, `VERDICT: unmet`, or `VERDICT: impossible`. The first match wins. Everything after `VERDICT: met` is discarded. After `VERDICT: unmet` or `VERDICT: impossible`, the rest of the line (after the em-dash) is the reason.

**Error handling.** If the response does not contain one of the three verdict strings, or if the model call fails (network error, timeout, provider error), the judge returns a special "error" verdict. An error verdict is treated as `unmet` with the reason `"judge call failed: <error detail>"`. This means a judge that errors does **not** silently allow a stop — it vetoes. The model sees the veto and can try again.

**Model selection.** The judge uses `verify.judge_model` from config. If absent, it defaults to the session's model (from `protocol.State.Options.Model`). If the session has no model set, it defaults to the config's default provider model.

### How long a check or gate command may run

Command execution (both `require_done_when` mechanical checks and `verify.command`) uses a 5-minute timeout. This is configurable via `verify.command_timeout` (integer, seconds). The default is 300. If the command exceeds the timeout, it is killed and treated as a failure: `require_done_when` denies the tool call with the reason "command timed out after N seconds"; the workspace gate vetoes with the reason "command timed out".

If `verify.command_timeout` is zero or negative, the default of 300 is used (no override means default always applies).

### How much transcript the judge sees

The judge sees the last 10 turns (user + assistant message pairs) from the session log, plus the Current state block rendered from the latest `tasks` event and the active goal. This is a deliberate trade-off: enough context for the judge to reason about what happened without overwhelming the context window. The 10-turn window is configurable via `verify.judge_turns` (integer, default 10).

### What `report` does in a directory that is not a git repository

This is a normal case (the developer might be working in a plain directory). When git is unavailable or the directory is not a git repository:

- `files_touched` returns `nil` (not an empty slice — the field is omitted from the JSON report, per the spec's `omitempty` tags).
- `commits` returns `nil`.
- `tree_dirty` returns `nil` (the report module does not assert anything about the tree; `verify` owns the dirty-tree veto).

No error is returned. The `Report` method returns `(ReportFields{}, nil)`.

### How `files_touched` is determined

`files_touched` lists every file that has been modified, created, or deleted since the last clean state (HEAD). It is computed by running `git diff --name-only HEAD` for tracked files that changed, plus `git ls-files --others --exclude-standard` for untracked files. Both lists are combined and sorted. If there are no changes, it returns `nil` (omitted from the report).

**Untracked files are included.** The rationale: if the agent created a new file and the developer hasn't committed it yet, the report should show it. An untracked file is a touched file.

### What happens when the two modules disagree about `tree_dirty`

Both `verify` and `report` can observe `tree_dirty` (verify checks it at the stop gate; report observes it for the report). The registry's `Report` merge rule is: "a `TreeDirty` from any reporter wins over nil." Since `verify` runs before `report` in the registration list, `verify`'s `TreeDirty` is set first, and `report`'s value (if non-nil) would override it.

**Decision:** `report.TreeDirty` always returns `nil`. Only `verify` sets `TreeDirty`. This avoids confusion: `verify` is the authority on tree cleanliness (it is the one that vetoes on a dirty tree), and `report` simply reflects the same truth for the report. If `verify` is disabled, `report` still returns `nil` for `TreeDirty`, and the report field is omitted.

### How `verify` and `guard` coexist as two `ToolGate` modules

The registry dispatches `ToolGate` hooks in registration order. The first `Deny` short-circuits. `verify`'s `ToolGate` handles `require_done_when` (only for `task.update`) and mechanical checks (only for `task.update` when the status changes to `done`). `guard` handles everything else. Since `verify`'s verdicts are narrow (only `task.update` with specific conditions), they do not conflict with `guard`'s broader rules.

**Registration order:** `guard` before `verify`. This means `guard` evaluates first. If `guard` denies a `task.update` (e.g., high-risk bash embedded in the task title), `verify` never sees it. If `guard` allows, `verify` gets a chance to apply `require_done_when`. This is the safer order: broad policy first, then narrow verification.

### `blocked` tasks and the open-task veto

Spec §10.2 says: "at the stop gate, any `pending` or `in_progress` task produces a veto listing them. `blocked` tasks must carry a `note`; they are reported, not vetoed."

**Decision:** A `blocked` task without a `note` is treated as if it were `pending` — it produces a veto with the reason "blocked task tN has no note". This ensures that blocked tasks are always documented. A `blocked` task with a `note` is listed in the veto message but does not itself veto.

### The `check` event appended by `require_done_when`

When a mechanical check passes, the module appends a `check` event with:
- `name`: `"task:<task_id>"`
- `kind`: `"command"`
- `task_id`: the task id
- `status`: `"pass"`
- `summary`: `"exit 0"`
- `output`: the last 2000 characters of the command output (or the full output if shorter)

### The `check` event appended by the workspace gate

When the workspace gate runs, it appends a `check` event with:
- `name`: `"verify.command"`
- `kind`: `"module"`
- `status`: `"pass"` or `"fail"` (the gate itself vetoes on fail, so a pass event means the gate cleared)
- `summary`: the first 200 characters of the command output
- `output`: the last 2000 characters of the command output

### The `goal` event appended by the judge

When the judge returns `met` or `impossible`, the module appends a `goal` event with:
- `state`: `"met"` or `"impossible"`
- `reason`: the judge's reason (empty for `met`)
- `source`: `"module:verify"`

### Deterministic-first ordering

The spec requires: "open tasks, failed checks, and a dirty tree are evaluated before paying for a judge call." The `BeforeStop` method evaluates in this exact order:

1. Open tasks (pending / in_progress) → veto if any
2. Blocked tasks without notes → veto if any
3. Mechanical checks that failed → veto if any
4. Workspace gate (`verify.command`) → veto if it fails
5. Dirty tree → veto if dirty
6. Judge call (only if a goal is set and no veto above fired)

Steps 1–5 are free (no model call). The judge (step 6) is only called if all deterministic checks pass.

---

## File structure

| Path | Responsibility |
|---|---|
| `daemon/modules/verify/verify.go` | **New.** The verify module: implements `Module`, `ToolGate`, `StopGate`, `Reporter`. Core logic for `require_done_when`, mechanical checks, open-task veto, workspace gate, dirty-tree check, and goal judge orchestration. |
| `daemon/modules/verify/judge.go` | **New.** The goal judge: builds the prompt, calls the host model API, parses the response, returns a verdict. |
| `daemon/modules/verify/verify_test.go` | **New.** Table-driven tests for every hook, every config option, every error case. |
| `daemon/modules/report/report.go` | **New.** The report module: implements `Module` and `Reporter`. Git-based workspace observation. |
| `daemon/modules/report/report_test.go` | **New.** Table-driven tests for git operations, non-git directories, and edge cases. |
| `daemon/modules/all.go` | **Modify.** Add verify and report to the All registration list. |

---

## Task 1: Module skeletons and registration

**Files:**
- Create: `daemon/modules/verify/verify.go` (skeleton only — Module interface, no hooks yet)
- Create: `daemon/modules/verify/judge.go` (empty — judge logic comes later)
- Create: `daemon/modules/verify/verify_test.go` (config reading tests only)
- Create: `daemon/modules/report/report.go` (skeleton only — Module interface, no Reporter yet)
- Create: `daemon/modules/report/report_test.go` (config reading tests only)
- Modify: `daemon/modules/all.go` (add verify and report to the All list)

**Goal:** Both modules initialise from config, have stable names, and are registered. No hook logic yet.

- [ ] **Step 1: Write the failing tests for module names and empty init**

Append to `daemon/modules/verify/verify_test.go` and `daemon/modules/report/report_test.go` a test that creates a zero-value module and verifies:
- `Name()` returns the string "verify" (for verify) / "report" (for report)
- `Init(nil, nil)` returns nil (no error with empty config)

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./daemon/modules/verify/ -run TestName -v`
Run: `go test ./daemon/modules/report/ -run TestName -v`
Expected: compile errors `undefined: Module`.

- [ ] **Step 3: Create the module skeletons**

Create `daemon/modules/verify/verify.go` with:
- A `Module` struct (zero fields for now)
- A `Name()` method returning "verify"
- An `Init(_ module.Host, cfg module.Config) error` method that reads config, sets defaults, and returns nil

Create `daemon/modules/verify/judge.go` with an empty file (the judge logic is added in Task 4).

Create `daemon/modules/report/report.go` with:
- A `Module` struct (zero fields for now)
- A `Name()` method returning "report"
- An `Init(_ module.Host, cfg module.Config) error` method that reads config, sets defaults, and returns nil

- [ ] **Step 4: Run the tests — they should pass**

Run: `go test ./daemon/modules/verify/ -run TestName -v`
Run: `go test ./daemon/modules/report/ -run TestName -v`
Expected: both pass.

- [ ] **Step 5: Register verify and report in all.go**

Modify `daemon/modules/all.go` to import both packages and add `&verify.Module{}` and `&report.Module{}` to the `All` registration list. Verify comes after guard and skills; report comes after verify.

```
All = []module.Module{
    &guard.Module{},
    &skills.Module{},
    &verify.Module{},
    &report.Module{},
}
```

- [ ] **Step 6: Run the boundary test**

Run: `go test ./daemon/module/ -run TestModulesImportOnlyTheContract -v`
Expected: pass (both modules only import `daemon/module` and `protocol`).

- [ ] **Step 7: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

---

## Task 2: `require_done_when` — the ToolGate hook

**Files:**
- Modify: `daemon/modules/verify/verify.go` (add `GateTool` method)

**Goal:** A `task.update` introducing a task without `done_when` is denied through the tool gate when `require_done_when` is enabled (under `nabu run` or when a goal is set).

- [ ] **Step 1: Write the failing test for `require_done_when` enabled**

Append to `daemon/modules/verify/verify_test.go` a test that:
1. Creates a verify module with `require_done_when: true`
2. Creates a fake session with a goal set (`protocol.GoalData{Condition: "build passes"}`)
3. Calls `GateTool` with a `task.update` tool call containing a task with `title: "fix bug"` and `status: "pending"` but **no** `done_when`
4. Expects a `Verdict{Decision: Deny, Reason: "every task needs a done_when: the observable condition that proves it finished."}`

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./daemon/modules/verify/ -run TestRequireDoneWhenDenied -v`
Expected: compile error `undefined: module.ToolGate` or `GateTool` not implemented.

- [ ] **Step 3: Implement `GateTool` — `require_done_when` path**

Add to `daemon/modules/verify/verify.go`:
- A `GateTool(ctx context.Context, s module.Session, call protocol.ToolCallData) module.Verdict` method
- Config fields: `requireDoneWhen bool` (default: depends on context — see below)
- The method:
  1. Checks if the tool is `task.update` — if not, returns `Allow` immediately (passes through to guard and other gates)
  2. Parses the `task.update` arguments to extract the task list
  3. If any new task (one not in the previous snapshot, or one being created for the first time) lacks `done_when`, returns `Deny` with the reason from the spec
  4. If all tasks have `done_when`, returns `Allow`

**Config-driven vs context-driven `require_done_when`.** The spec says: "on under `nabu run` and whenever a goal is set; off for a plain interactive session unless configured on." Since the module does not know whether it is running under `nabu run` (that is a CLI concern), the implementation uses this logic:
- If `verify.require_done_when` is explicitly set in config, use that value (the owner can force it on or off).
- If a goal is set (`protocol.State.Goal != nil`), `require_done_when` is **on**.
- If no goal is set and config is absent, `require_done_when` is **off** (interactive sessions are not nagged).

This means: `nabu run` sets a goal → `require_done_when` activates automatically. A plain interactive session without a goal → `require_done_when` is off. The owner can override with config.

- [ ] **Step 4: Write the failing test for `require_done_when` off**

Append a test that:
1. Creates a verify module with no goal set and no config override
2. Calls `GateTool` with a `task.update` that has no `done_when`
3. Expects `Allow` (off for plain interactive sessions)

- [ ] **Step 5: Write the failing test for `require_done_when` with existing tasks**

Append a test that:
1. Creates a verify module with `require_done_when: true` and a goal set
2. The session has a previous `tasks` event with task t1 that has `done_when: "tests pass"`
3. The new `task.update` keeps t1's `done_when` (carried over from the previous snapshot) and adds t2 without `done_when`
4. Expects `Deny` (t2 is new and lacks `done_when`)

- [ ] **Step 6: Write the failing test for `require_done_when` with carried-over `done_when`**

Append a test that:
1. Same setup as Step 5, but t2 also has `done_when: "tests pass"`
2. Expects `Allow` (all tasks have `done_when`, either new or carried over)

- [ ] **Step 7: Run all `require_done_when` tests**

Run: `go test ./daemon/modules/verify/ -run TestRequireDoneWhen -v`
Expected: all pass.

- [ ] **Step 8: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass.

---

## Task 3: Mechanical checks — the `task.update` → `done` path

**Files:**
- Modify: `daemon/modules/verify/verify.go` (add mechanical check logic to `GateTool`)

**Goal:** When a task with a `check` moves to `done`, the module runs the command in the workspace before allowing the call. Exit 0 appends a passing `check` event and allows; non-zero denies with the tail of the output and leaves the task `in_progress`.

- [ ] **Step 1: Write the failing test for a passing mechanical check**

Append to `daemon/modules/verify/verify_test.go` a test that:
1. Creates a verify module with `command_timeout: 30`
2. Creates a fake session with a task t1 that has `check: "echo hello"` (a command that always succeeds)
3. Calls `GateTool` with a `task.update` that sets t1's status to `done`
4. Expects: the command is executed, a `check` event is appended, and the verdict is `Allow`

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./daemon/modules/verify/ -run TestMechanicalCheckPass -v`
Expected: fails because `GateTool` does not yet run commands.

- [ ] **Step 3: Implement the mechanical check — passing case**

Add to `daemon/modules/verify/verify.go`:
- A `runCommand(ctx, workspacePath, command, timeout) (exitCode int, output string, err error)` helper that:
  1. Invokes the command via `exec.CommandContext` with the given timeout
  2. Runs it in the workspace directory
  3. Captures stdout and stderr
  4. Returns the exit code and the combined output
- In `GateTool`, after checking `require_done_when`:
  1. For each task in the `task.update` that is changing to `done` status:
     - If the task has a non-empty `check` field:
       - Run the command via `runCommand`
       - If exit code is 0: append a `check` event (status: "pass", name: "task:<task_id>", kind: "command") and continue
       - If exit code is non-zero: return `Deny` with the reason containing the last 2000 characters of output; the model must change the task back to `in_progress` before retrying

- [ ] **Step 4: Write the failing test for a failing mechanical check**

Append a test that:
1. Creates a verify module with `command_timeout: 30`
2. Creates a fake session with a task t1 that has `check: "exit 1"` (a command that always fails)
3. Calls `GateTool` with a `task.update` that sets t1's status to `done`
4. Expects: the command is executed, the verdict is `Deny` with a reason containing "exit 1", and **no** `check` event is appended (the task stays `in_progress` — the tool call is denied, so the status change never happens)

- [ ] **Step 5: Write the failing test for a task without a `check`**

Append a test that:
1. Creates a verify module
2. Creates a fake session with a task t1 that has no `check` field
3. Calls `GateTool` with a `task.update` that sets t1's status to `done`
4. Expects `Allow` (no check to run — this is the "judged checks" path, handled by the stop-gate judge)

- [ ] **Step 6: Write the failing test for command timeout**

Append a test that:
1. Creates a verify module with `command_timeout: 1`
2. Creates a fake session with a task t1 that has `check: "sleep 10"` (or `timeout 2 sleep 10` on Windows — the test must skip on systems without `sleep`)
3. Calls `GateTool` with a `task.update` that sets t1's status to `done`
4. Expects: the command is killed by timeout, the verdict is `Deny` with a reason containing "timed out"

- [ ] **Step 7: Run all mechanical check tests**

Run: `go test ./daemon/modules/verify/ -run TestMechanicalCheck -v`
Expected: all pass.

- [ ] **Step 8: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass.

---

## Task 4: Open-task veto — the `StopGate` hook

**Files:**
- Modify: `daemon/modules/verify/verify.go` (add `BeforeStop` method)

**Goal:** At the stop gate, any `pending` or `in_progress` task vetoes, listing them. `blocked` tasks must carry a `note`; they are reported, not vetoed. A `blocked` task without a `note` vetoes.

- [ ] **Step 1: Write the failing test for the open-task veto**

Append to `daemon/modules/verify/verify_test.go` a test that:
1. Creates a verify module
2. Creates a fake session with three tasks: t1 (done), t2 (in_progress), t3 (pending)
3. Calls `BeforeStop` with a `StopInfo` containing those tasks and no goal
4. Expects a single veto with reason containing "t2 (in_progress), t3 (pending)"

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./daemon/modules/verify/ -run TestOpenTaskVeto -v`
Expected: compile error `StopGate` not implemented.

- [ ] **Step 3: Implement `BeforeStop` — open-task veto**

Add to `daemon/modules/verify/verify.go`:
- A `BeforeStop(ctx context.Context, s module.Session, info module.StopInfo) module.StopVerdict` method
- The method:
  1. Iterates `info.Tasks`
  2. Collects all tasks with status `pending` or `in_progress` into a list
  3. Collects all tasks with status `blocked` that have an empty `note` into a separate list
  4. If either list is non-empty, returns `StopVerdict{Allow: false, Reason: "N tasks are still open: <list>"}` where the list is formatted as "t2 (in_progress), t3 (pending)"
  5. If both lists are empty, returns `StopVerdict{Allow: true}`

The task list in the veto reason must include the task id and status, in id order (t1, t2, t3, ...).

- [ ] **Step 4: Write the failing test for blocked tasks with notes**

Append a test that:
1. Same setup as Step 1, but t2 is `blocked` with `note: "waiting on external API"`
2. Expects `Allow` (blocked tasks with notes are not vetoed)

- [ ] **Step 5: Write the failing test for blocked tasks without notes**

Append a test that:
1. Same setup as Step 1, but t2 is `blocked` with an empty `note`
2. Expects a veto with reason containing "t2 (blocked, no note)"

- [ ] **Step 6: Write the failing test for all tasks done**

Append a test that:
1. Creates a fake session with all tasks in `done` status
2. Calls `BeforeStop`
3. Expects `Allow`

- [ ] **Step 7: Run all open-task veto tests**

Run: `go test ./daemon/modules/verify/ -run TestOpenTaskVeto -v`
Expected: all pass.

- [ ] **Step 8: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass.

---

## Task 5: Workspace gate and dirty-tree check

**Files:**
- Modify: `daemon/modules/verify/verify.go` (add workspace gate and dirty-tree logic)

**Goal:** `verify.command` is the project's canonical gate. Run it at the stop gate and veto on failure. `verify.require_clean_tree` defaults on; a dirty tree vetoes.

- [ ] **Step 1: Write the failing test for `verify.command`**

Append to `daemon/modules/verify/verify_test.go` a test that:
1. Creates a verify module with `command: "go build ./..."`
2. Creates a fake session in a workspace directory that contains a valid Go module
3. Calls `BeforeStop`
4. Expects: the command is executed, a `check` event is appended with `name: "verify.command"`, and the verdict is `Allow` (assuming the command succeeds)

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./daemon/modules/verify/ -run TestWorkspaceGatePass -v`
Expected: fails because the workspace gate is not yet implemented.

- [ ] **Step 3: Implement the workspace gate**

Add to `daemon/modules/verify/verify.go`:
- Config fields: `command string` (default: `"go build ./... && go vet ./... && go test ./..."`), `requireCleanTree bool` (default: `true`)
- In `BeforeStop`, after the open-task check:
  1. If `command` is non-empty:
     - Run the command via `runCommand` (reusing the helper from Task 3)
     - If the command fails (non-zero exit): append a `check` event with `status: "fail"`, and return `StopVerdict{Allow: false, Reason: "workspace gate failed: <summary>"}`
     - If the command succeeds: append a `check` event with `status: "pass"`, and continue

- [ ] **Step 4: Write the failing test for a failing workspace gate**

Append a test that:
1. Creates a verify module with `command: "false"` (a command that always fails)
2. Calls `BeforeStop`
3. Expects: the command is executed, a `check` event with `status: "fail"` is appended, and the verdict is `Deny`

- [ ] **Step 5: Write the failing test for `require_clean_tree` — dirty tree**

Append a test that:
1. Creates a verify module with `require_clean_tree: true`
2. Creates a fake session in a git repository where `git status --porcelain` returns non-empty (there are uncommitted changes)
3. Calls `BeforeStop`
4. Expects: `StopVerdict{Allow: false, Reason: "the working tree is dirty; commit or explain uncommitted changes"}`

- [ ] **Step 6: Implement the dirty-tree check**

Add to `daemon/modules/verify/verify.go`:
- A `isTreeDirty(workspacePath string) (bool, error)` helper that:
  1. Runs `git status --porcelain` in the workspace directory
  2. Returns `true` if the output is non-empty (there are uncommitted changes)
  3. Returns `false` if the output is empty (tree is clean)
  4. Returns `(false, nil)` if git is not available or the directory is not a git repository (the check is inert, not an error)

- In `BeforeStop`, after the workspace gate:
  1. If `require_clean_tree` is true and `isTreeDirty` returns true: veto

- [ ] **Step 7: Write the failing test for `require_clean_tree` — clean tree**

Append a test that:
1. Creates a verify module with `require_clean_tree: true`
2. Creates a fake session in a git repository where `git status --porcelain` returns empty
3. Calls `BeforeStop`
4. Expects: no veto from the dirty-tree check (continues to judge or allows)

- [ ] **Step 8: Write the failing test for `require_clean_tree` disabled**

Append a test that:
1. Creates a verify module with `require_clean_tree: false`
2. Creates a fake session in a dirty git repository
3. Calls `BeforeStop`
4. Expects: no veto from the dirty-tree check (the check is skipped)

- [ ] **Step 9: Write the failing test for `require_clean_tree` in a non-git directory**

Append a test that:
1. Creates a verify module with `require_clean_tree: true`
2. Creates a fake session in a directory that is not a git repository
3. Calls `BeforeStop`
4. Expects: no veto (the check is inert when git is unavailable)

- [ ] **Step 10: Run all workspace gate and dirty-tree tests**

Run: `go test ./daemon/modules/verify/ -run TestWorkspaceGate -v`
Expected: all pass.

- [ ] **Step 11: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass.

---

## Task 6: The goal judge

**Files:**
- Modify: `daemon/modules/verify/judge.go` (implement the judge)
- Modify: `daemon/modules/verify/verify.go` (integrate the judge into `BeforeStop`)

**Goal:** With a goal set, call the host's model API in a fresh context with the condition, the tasks with evidence, and recent transcript. Return exactly one of `met`, `unmet{reason}`, or `impossible{reason}`.

- [ ] **Step 1: Write the failing test for the judge — met verdict**

Append to `daemon/modules/verify/verify_test.go` a test that:
1. Creates a fake `Host` / `Model` that returns a completion with content `"VERDICT: met"`
2. Creates a verify module with a goal set
3. Calls the judge (or `BeforeStop` with only a goal set and no open tasks)
4. Expects: the judge is called with a prompt containing the goal condition, the task list, and recent transcript; the verdict is `met`; a `goal` event with `state: "met"` is appended

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./daemon/modules/verify/ -run TestJudgeMet -v`
Expected: fails because the judge is not yet implemented.

- [ ] **Step 3: Implement the judge — prompt construction**

Add to `daemon/modules/verify/judge.go`:
- A `buildPrompt(goal protocol.GoalData, tasks []protocol.Task, transcript []protocol.Event) string` function that:
  1. Constructs the prompt per the format decided in "Decisions settled by this plan"
  2. Lists all tasks with their status and evidence
  3. Includes the last K turns from the transcript (K = `verify.judge_turns`, default 10)
  4. Ends with the verdict instruction

- A `Judgment` type with fields: `Verdict string` ("met", "unmet", "impossible"), `Reason string`

- [ ] **Step 4: Implement the judge — model call and response parsing**

Add to `daemon/modules/verify/judge.go`:
- A `Judge(ctx context.Context, s module.Session, goal protocol.GoalData, tasks []protocol.Task, transcript []protocol.Event) (Judgment, error)` function that:
  1. Calls `buildPrompt` to construct the prompt
  2. Calls `host.Model().Complete(ctx, s, module.CompletionRequest{...})` with:
     - `Model`: `verify.judge_model` from config, or the session's model
     - `System`: a brief instruction like "You are an impartial judge. Respond with exactly one verdict line."
     - `Messages`: one user message containing the prompt
     - `MaxTokens`: 256 (the response is at most one line)
  3. Parses the response content:
     - Scans for `VERDICT: met` → `Judgment{Verdict: "met"}`
     - Scans for `VERDICT: unmet — <reason>` → `Judgment{Verdict: "unmet", Reason: <reason>}`
     - Scans for `VERDICT: impossible — <reason>` → `Judgment{Verdict: "impossible", Reason: <reason>}`
     - No match or empty response → `Judgment{Verdict: "unmet", Reason: "judge response did not contain a recognized verdict"}`
  4. If the model call returns an error → `Judgment{Verdict: "unmet", Reason: "judge call failed: <error detail>"}`

- [ ] **Step 5: Write the failing test for the judge — unmet verdict**

Append a test that:
1. Fake model returns `"VERDICT: unmet — not all files were modified"`
2. Expects: `Judgment{Verdict: "unmet", Reason: "not all files were modified"}`

- [ ] **Step 6: Write the failing test for the judge — impossible verdict**

Append a test that:
1. Fake model returns `"VERDICT: impossible — the required dependency is not available"`
2. Expects: `Judgment{Verdict: "impossible", Reason: "the required dependency is not available"}`

- [ ] **Step 7: Write the failing test for the judge — unrecognized response**

Append a test that:
1. Fake model returns `"I think the work is mostly done but not quite"` (no VERDICT line)
2. Expects: `Judgment{Verdict: "unmet", Reason: "judge response did not contain a recognized verdict"}`

- [ ] **Step 8: Write the failing test for the judge — model call error**

Append a test that:
1. Fake model returns `err: some network error`
2. Expects: `Judgment{Verdict: "unmet", Reason: "judge call failed: some network error"}`

- [ ] **Step 9: Integrate the judge into `BeforeStop`**

Modify `verify.go`'s `BeforeStop` method:
- After all deterministic checks (open tasks, workspace gate, dirty tree), if a goal is set and no veto has fired:
  1. Extract the recent transcript from the session (last K turns)
  2. Call `judge.Judge(ctx, s, *info.Goal, info.Tasks, transcript)`
  3. If the verdict is `met`: append a `goal` event with `state: "met"`, return `Allow`
  4. If the verdict is `impossible`: append a `goal` event with `state: "impossible"`, return `Allow`
  5. If the verdict is `unmet`: return `StopVerdict{Allow: false, Reason: verdict.Reason}`

- [ ] **Step 10: Run all judge tests**

Run: `go test ./daemon/modules/verify/ -run TestJudge -v`
Expected: all pass.

- [ ] **Step 11: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass.

---

## Task 7: The `Reporter` hook on `verify`

**Files:**
- Modify: `daemon/modules/verify/verify.go` (add `Report` method)

**Goal:** `verify` contributes to the report by providing `checks` (the workspace gate check) and `TreeDirty`.

- [ ] **Step 1: Write the failing test for `Report` — checks and tree dirty**

Append to `daemon/modules/verify/verify_test.go` a test that:
1. Creates a verify module with `command: "echo ok"`, `require_clean_tree: true`
2. Creates a fake session in a clean git repository
3. Calls `Report(ctx, s)`
4. Expects: `ReportFields{Checks: [{Name: "verify.command", Status: "pass", Summary: "echo ok"}], TreeDirty: boolPtr(false)}`

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./daemon/modules/verify/ -run TestReportVerify -v`
Expected: compile error `Reporter` not implemented.

- [ ] **Step 3: Implement `Report` on `verify`**

Add to `daemon/modules/verify/verify.go`:
- A `Report(ctx context.Context, s module.Session) (module.ReportFields, error)` method that:
  1. If `command` is non-empty:
     - Run the command via `runCommand`
     - Build a `ReportCheck{Name: "verify.command", Status: pass/fail, Summary: first 200 chars of output}`
  2. If `require_clean_tree` is true:
     - Call `isTreeDirty`
     - Set `TreeDirty` to the result (pointer to bool)
  3. Return `(ReportFields{Checks: checks, TreeDirty: treeDirty}, nil)`

If `command` is empty and `require_clean_tree` is false, return empty `ReportFields`.

- [ ] **Step 4: Write the failing test for `Report` — dirty tree**

Append a test that:
1. Creates a verify module with `require_clean_tree: true`
2. Creates a fake session in a dirty git repository
3. Calls `Report`
4. Expects: `TreeDirty: boolPtr(true)`

- [ ] **Step 5: Write the failing test for `Report` — no command, no clean-tree check**

Append a test that:
1. Creates a verify module with no `command` and `require_clean_tree: false`
2. Calls `Report`
3. Expects: `ReportFields{}` (empty)

- [ ] **Step 6: Run all `Report` tests on verify**

Run: `go test ./daemon/modules/verify/ -run TestReport -v`
Expected: all pass.

- [ ] **Step 7: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass.

---

## Task 8: The `report` module — `Reporter` hook

**Files:**
- Modify: `daemon/modules/report/report.go` (add `Report` method)

**Goal:** `report` implements `Reporter` and provides `files_touched`, `commits`, and `TreeDirty: nil`.

- [ ] **Step 1: Write the failing test for `Report` — files touched and commits**

Append to `daemon/modules/report/report_test.go` a test that:
1. Creates a report module
2. Creates a fake session in a git repository with one committed file and one untracked file
3. Calls `Report(ctx, s)`
4. Expects: `ReportFields{FilesTouched: ["new_file.txt", "modified_file.go"], Commits: ["abc1234"]}`

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./daemon/modules/report/ -run TestReportFiles -v`
Expected: compile error `Reporter` not implemented.

- [ ] **Step 3: Implement `files_touched`**

Add to `daemon/modules/report/report.go`:
- A `gitDiffNames(ctx, workspacePath) ([]string, error)` helper that:
  1. Runs `git diff --name-only HEAD` (tracked files that changed)
  2. Runs `git ls-files --others --exclude-standard` (untracked files)
  3. Combines, deduplicates, sorts, and returns the list
  4. Returns `nil` if git is unavailable or the directory is not a git repository

- [ ] **Step 4: Implement `commits`**

Add to `daemon/modules/report/report.go`:
- A `gitCommits(ctx, workspacePath) ([]string, error)` helper that:
  1. Runs `git log --format=%H -20` (last 20 commit hashes)
  2. Returns the list of commit hashes
  3. Returns `nil` if git is unavailable or there are no commits

- [ ] **Step 5: Implement `Report` on `report`**

Add to `daemon/modules/report/report.go`:
- A `Report(ctx context.Context, s module.Session) (module.ReportFields, error)` method that:
  1. Calls `gitDiffNames` → `FilesTouched` (nil if error or empty)
  2. Calls `gitCommits` → `Commits` (nil if error or empty)
  3. Sets `TreeDirty: nil` (verify owns this)
  4. Returns `(ReportFields{FilesTouched, Commits, TreeDirty: nil}, nil)`

- [ ] **Step 6: Write the failing test for `Report` — non-git directory**

Append a test that:
1. Creates a report module
2. Creates a fake session in a non-git directory
3. Calls `Report`
4. Expects: `ReportFields{FilesTouched: nil, Commits: nil, TreeDirty: nil}` (no error)

- [ ] **Step 7: Write the failing test for `Report` — empty repository (no commits)**

Append a test that:
1. Creates a report module
2. Creates a fake session in a git repository with no commits (fresh `git init`)
3. Calls `Report`
4. Expects: `Commits: nil` (no commits to report), `FilesTouched: nil` (no HEAD to diff against)

- [ ] **Step 8: Run all report module tests**

Run: `go test ./daemon/modules/report/ -run TestReport -v`
Expected: all pass.

- [ ] **Step 9: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass.

---

## Task 9: Integration — registry merge, ordering, and end-to-end

**Files:**
- Modify: `daemon/modules/verify/verify_test.go`
- Modify: `daemon/modules/report/report_test.go`

**Goal:** Verify that the two modules compose correctly through the registry: `Report` merges their fields, `GateTool` short-circuits correctly, and `BeforeStop` evaluates in deterministic-first order.

- [ ] **Step 1: Write the failing integration test — registry Report merge**

Append to `daemon/modules/verify/verify_test.go` a test that:
1. Creates a registry with both verify (with `command: "echo ok"`, `require_clean_tree: true`) and report modules
2. Creates a fake session in a clean git repository with one modified file
3. Calls `registry.Report(ctx, s)`
4. Expects: `FilesTouched` contains the modified file (from report), `Checks` contains the verify.command check (from verify), `TreeDirty` is `false` (from verify)

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./daemon/modules/verify/ -run TestRegistryReportMerge -v`
Expected: fails because the test uses a real registry and the modules must integrate.

- [ ] **Step 3: Write the failing integration test — deterministic-first ordering**

Append a test that:
1. Creates a verify module with a goal set, open tasks, and a failing workspace gate
2. Calls `BeforeStop`
3. Expects: the veto is from the open-task check (step 1), NOT from the workspace gate (step 3) or the judge (step 6). The judge must not be called at all.

- [ ] **Step 4: Write the failing integration test — judge called only when deterministic checks pass**

Append a test that:
1. Creates a verify module with a goal set, all tasks done, clean tree, and a passing workspace gate
2. The fake model is set up to track whether it was called
3. Calls `BeforeStop`
4. Expects: the judge IS called (all deterministic checks passed), and the verdict determines the final result

- [ ] **Step 5: Run all integration tests**

Run: `go test ./daemon/modules/verify/ -run TestIntegration -v`
Run: `go test ./daemon/modules/report/ -run TestIntegration -v`
Expected: all pass.

- [ ] **Step 6: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass.

---

## Task 10: Edge cases, error handling, and config parsing

**Files:**
- Modify: `daemon/modules/verify/verify_test.go`
- Modify: `daemon/modules/report/report_test.go`

**Goal:** Handle all edge cases: empty task lists, malformed arguments, git errors, missing workspace, and invalid config.

- [ ] **Step 1: Write the failing test for `require_done_when` — empty task list**

Append a test that:
1. Calls `GateTool` with a `task.update` containing an empty task list
2. Expects: `Deny` with a reason from `Merge` ("tasks must not be empty") — the error propagates from the tool, not from verify

- [ ] **Step 2: Write the failing test for `require_done_when` — task.update with only status changes**

Append a test that:
1. Creates a module with `require_done_when: true` and a goal
2. The session has tasks t1 (done, with done_when) and t2 (pending, with done_when)
3. The new `task.update` only changes t1's evidence (carried over) and t2's status to in_progress
4. Expects: `Allow` (no new tasks, existing tasks already have done_when)

- [ ] **Step 3: Write the failing test for mechanical check — empty check string**

Append a test that:
1. Creates a task with `check: ""` (empty string, not absent)
2. Calls `GateTool` with the task moving to `done`
3. Expects: `Allow` (empty check is treated as no check — the judged-check path)

- [ ] **Step 4: Write the failing test for mechanical check — command execution error**

Append a test that:
1. Creates a task with `check: "nonexistent_command_xyz"` (a command that does not exist)
2. Calls `GateTool` with the task moving to `done`
3. Expects: `Deny` with a reason containing the error output (command not found)

- [ ] **Step 5: Write the failing test for config — invalid command_timeout**

Append a test that:
1. Creates a config with `command_timeout: -1`
2. Calls `Init`
3. Expects: `Init` uses the default of 300 seconds (negative values are clamped to default)

- [ ] **Step 6: Write the failing test for config — empty command**

Append a test that:
1. Creates a config with `command: ""`
2. Calls `Init`
3. Expects: the workspace gate is skipped (empty command means no gate)

- [ ] **Step 7: Write the failing test for the judge — empty transcript**

Append a test that:
1. Creates a verify module with a goal set
2. The session has no recent transcript (first turn)
3. Calls `BeforeStop`
4. Expects: the judge is called with an empty transcript section, and the response is parsed normally

- [ ] **Step 8: Write the failing test for the report module — git error**

Append a test that:
1. Creates a report module
2. Creates a fake session in a directory where `git status` returns an error (e.g., permission denied)
3. Calls `Report`
4. Expects: no error returned, `FilesTouched: nil`, `Commits: nil` (git errors are treated as "no data", not as module errors)

- [ ] **Step 9: Run all edge case tests**

Run: `go test ./daemon/modules/verify/ -run TestEdgeCase -v`
Run: `go test ./daemon/modules/report/ -run TestEdgeCase -v`
Expected: all pass.

- [ ] **Step 10: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass.

- [ ] **Step 11: Run gofmt check**

Run: `gofmt -l daemon/modules/verify/ daemon/modules/report/`
Expected: no output.

---

## Summary

**Files created:**
- `daemon/modules/verify/verify.go` — the verify module (Module, ToolGate, StopGate, Reporter)
- `daemon/modules/verify/judge.go` — the goal judge (prompt builder, model caller, response parser)
- `daemon/modules/verify/verify_test.go` — comprehensive tests for verify
- `daemon/modules/report/report.go` — the report module (Module, Reporter)
- `daemon/modules/report/report_test.go` — comprehensive tests for report

**Files modified:**
- `daemon/modules/all.go` — register verify and report in the module list

**Tasks:** 10 ordered tasks, each independently verifiable

**Decisions made:**

| Decision | Value |
|---|---|
| Judge prompt format | Structured text with Condition, Tasks, Recent transcript, and a VERDICT line instruction |
| Judge response parsing | Scan for `VERDICT: met`, `VERDICT: unmet — <reason>`, or `VERDICT: impossible — <reason>` |
| Judge error handling | Error → unmet with reason "judge call failed: <detail>" — never silently allows |
| Check/gate command timeout | 300 seconds (5 minutes), configurable via `verify.command_timeout` |
| Judge transcript window | Last 10 turns (configurable via `verify.judge_turns`) |
| Non-git directory handling | No error; `files_touched: nil`, `commits: nil`, `tree_dirty: nil` |
| `files_touched` includes untracked | Yes — `git diff --name-only HEAD` + `git ls-files --others --exclude-standard` |
| `tree_dirty` authority | Only `verify` sets `TreeDirty`; `report` always returns `nil` |
| `verify` / `guard` registration order | `guard` before `verify` — broad policy first, narrow verification second |
| Blocked task without note | Treated as pending — produces a veto |
| `require_done_when` activation | On when a goal is set; off otherwise; overridable via config |
| Default workspace gate | `go build ./... && go vet ./... && go test ./...` |
| `require_clean_tree` default | `true` |

**Symbols confirmed in source:**
- `module.Module` → `daemon/module/module.go` — `Name() string`, `Init(Host, Config) error`
- `module.ToolGate` → `daemon/module/module.go` — `GateTool(ctx, Session, protocol.ToolCallData) Verdict`
- `module.StopGate` → `daemon/module/module.go` — `BeforeStop(ctx, Session, StopInfo) StopVerdict`
- `module.Reporter` → `daemon/module/module.go` — `Report(ctx, Session) (ReportFields, error)`
- `module.Verdict` → `daemon/module/module.go` — `Decision Decision`, `Reason string`, `Summary string`, `Risk string`
- `module.StopVerdict` → `daemon/module/module.go` — `Allow bool`, `Reason string`
- `module.StopInfo` → `daemon/module/module.go` — `Goal *GoalData`, `Tasks []Task`, `LastAssistantMessage`, `TurnsSinceUser`, `VetoCount`
- `module.ReportFields` → `daemon/module/module.go` — `FilesTouched []string`, `Commits []string`, `TreeDirty *bool`, `Checks []ReportCheck`
- `module.Session` → `daemon/module/module.go` — `ID()`, `Workspace()`, `Events()`, `State()`, `Append()`
- `module.Host` → `daemon/module/module.go` — `Model()`, `Tools()`, `UI()`, `DataDir()`, `Log()`
- `module.Model` → `daemon/module/module.go` — `Complete(ctx, Session, CompletionRequest) (CompletionResponse, error)`
- `module.CompletionRequest` → `daemon/module/module.go` — `Model`, `System`, `Messages`, `MaxTokens`
- `module.Config` → `daemon/module/module.go` — `map[string]any` with `Bool`, `String`, `Int`, `Strings`, `Enabled`
- `module.Registry.GateTool` → `daemon/module/registry.go` — first Deny short-circuits, Ask aggregates (first Ask wins)
- `module.Registry.BeforeStop` → `daemon/module/registry.go` — collects all vetoes in order
- `module.Registry.Report` → `daemon/module/registry.go` — merges fields; TreeDirty from any reporter wins over nil
- `protocol.Task` → `protocol/types.go` — `ID`, `Title`, `Status`, `DoneWhen`, `Check`, `BlockedBy`, `Note`, `Evidence`
- `protocol.TaskStatus` → `protocol/types.go` — `TaskPending`, `TaskInProgress`, `TaskBlocked`, `TaskDone`, `TaskFailed`, `TaskCancelled`
- `protocol.GoalData` → `protocol/types.go` — `Condition`, `State`, `Reason`, `Source`
- `protocol.CheckData` → `protocol/types.go` — `Name`, `Kind`, `TaskID`, `Status`, `Summary`, `Output`
- `protocol.ReportCheck` → `protocol/types.go` — `Name`, `Status`, `Summary`
- `protocol.ReportData` → `protocol/types.go` — `ExitStatus`, `Goal`, `Tasks`, `Checks`, `FilesTouched`, `Commits`, `TreeDirty`
- `protocol.ToolCallData` → `protocol/types.go` — `CallID`, `Tool`, `Arguments`, `Source`
- `protocol.State` → `protocol/types.go` — `Options`, `Goal`, `Tasks`, `Budget` (via projection)
- `protocol.PermissionMode` → `protocol/types.go` — `PermissionAsk`, `PermissionAuto`, `PermissionBypass`
- `tools.Merge` → `daemon/tools/tasks.go` — whole-list task merge with evidence
- `guard.Module` → `daemon/modules/guard/guard.go` — existing ToolGate
- `modules.All` → `daemon/modules/all.go` — registration list

**Symbols NOT confirmed (gaps):**
- `protocol.State` fields accessed by modules: the `Session.State()` method returns a `protocol.State` that includes `Goal *GoalData` and `Tasks []Task`. These fields are used by `verify` to determine whether a goal is set and to inspect task state. The exact shape of `protocol.State` is defined in `protocol/types.go` (or the session package's projection) — the implementer should confirm the field names by reading the actual file.
- The `Session.Events(after *string)` return type: the implementer should confirm the exact event structure and how to extract the last K turns from the event list.