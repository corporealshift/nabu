# Run re-planning: plan

Spec: `docs/specs/2026-10-12-run-replan-design.md`.

All of it is on one branch, `run-replan`, as one PR with a commit per step, so no branch
stacks on an unmerged one. Each step leaves `gofmt -l .` empty and `go build ./... &&
go vet ./... && go test ./...` green.

The marker line `PLAN CANNOT WORK` is defined twice, once in the `claude` module and
once in the runner, as `memory:no-curator` is: a client never imports a module.

### 1. The automatic question asks for a verdict

- **Changes:** `daemon/modules/claude/stuck.go`: `PlanVerdict = "PLAN CANNOT WORK"`, and
  the "What to do" section of `question` gains: "If the task cannot be done as it is set
  out, begin your answer with the line PLAN CANNOT WORK, then say what makes it
  impossible and what is true instead."
- **Proves it:** a row in `stuck_test.go` that the question carries the instruction and
  the marker, and still fits its cap.
- **Depends on:** nothing.

### 2. The workflow knows `replan-review`

Pure state and transition, with no runner code producing the new outcomes yet.

- **Changes:** `clients/runs/`:
  - `state.go`: `StepReplanReview Step = "replan-review"`, counted in `Claude()`;
    `ReplanFile = "replan-request.md"`, `FindingsFile = "findings.md"`; `Run` gains
    `Replans`, `Refusals`, `ReplanFrom Step` (the step that claimed), `ReplanAdvice`
    (Claude's refusal, for that step's next session), `Finding` (an automatic verdict
    waiting to be acted on) and `Seen` (the event cursor of the running session).
  - `transition.go`: `MaxReplans = 2`, `MaxRefusals = 2`. `Outcome` gains `ReplanRequest
    bool`, `Replan string` (an agreed finding) and `Refused string` (Claude's advice).
    For work, fix and ci-fix, before their other arrows: `Replan` re-plans,
    `ReplanRequest` sets `ReplanFrom` and enters `replan-review`. At `replan-review`:
    `Replan` re-plans; otherwise `Refusals++`, past the cap fails at `replan-review`,
    else `ReplanAdvice` is set and `ReplanFrom` is entered fresh. Re-planning counts
    `Replans`, past the cap fails at `replan-review` with the finding, else clears
    `FinalReviewed` and enters `plan`. An OK outcome of work, fix or ci-fix clears
    `ReplanAdvice`. `enter` clears `Seen` and `Finding` with the other session fields.
    `Resume` clears `Replans` and `Refusals`; a run failed at `replan-review` resumes at
    `plan`.
  - `parse.go`: `ParseReplan(answer) (finding, advice string, ok bool)`: a first line
    of `REPLAN` or `REFUSED`, the rest being the text; anything else is not ok.
- **Proves it:** rows in `transition_test.go` for every arrow above, the two caps and
  `Resume`; rows in `parse_test.go` for both answers, empty text and malformed answers.
- **Depends on:** nothing.

### 3. The model can claim it

- **Changes:** `clients/runs/`:
  - `prompts.go`: a `replanOffer(r)` paragraph in `WorkPrompt`, `FixPrompt` and
    `CIFixPrompt`, naming `r.File(ReplanFile)`; their goals gain "or `<ReplanFile>`
    explains why the plan cannot work". `WorkPrompt`, `FixPrompt` and `CIFixPrompt`
    carry `r.ReplanAdvice` when set. `ReplanReviewPrompt(r)` gives Claude the brief,
    plan, tasks, the step and task that claimed, and the claim, and asks for `REPLAN`
    or `REFUSED` as the spec says. `PlanPrompt`, `VerifyPrompt` and `TasksPrompt` name
    `findings.md` when `r.Replans > 0`, and the plan and tasks prompts say work is
    already committed (`git log <base>..`) and to plan only what remains.
  - `runner.go`: `produced` checks `ReplanFile` first for work, fix and ci-fix and
    returns `ReplanRequest`. `perform` handles `replan-review`: ask, parse, on `REPLAN`
    append the finding to `findings.md` under a `## <date>, from <step>` heading and
    commit, then remove the request either way; the note is the finding or the advice.
    `record` gives the results "asked to re-plan", "re-planned" and "refused".
- **Proves it:** `prompts_test.go`: the offer and goal text in the three prompts, the
  advice when set, findings named only after a re-plan. `runner_test.go`, with the fake
  Claude and git: a work session that writes the request goes to `replan-review`;
  `REPLAN` appends `findings.md`, removes the request and goes to `plan`; `REFUSED`
  removes the request and returns to the same task with the advice in its prompt.
- **Depends on:** step 2.

### 4. The automatic verdict re-plans

- **Changes:** `clients/runs/`:
  - `ports.go` and `daemon.go`: `Daemon.EventsAfter(ctx, id, cursor)`, one page from
    the existing `goclient` call; the test fake implements it.
  - `runner.go`: in `observe`, while a work, fix or ci-fix session is running, read the
    events after `r.Seen`, advance it, and look for a `claude.ask` call with
    `source: module:claude` whose result's first line is `PLAN CANNOT WORK`. On one:
    set `r.Finding` to the answer, `r.Stopped`, save, and `Stop` the session. When the
    session is next seen not running, `produced` acts on `r.Finding` first: reset the
    worktree to its head, append the finding to `findings.md`, and return `Replan`.
  - `record`: the result "Claude judged the plan cannot work".
- **Proves it:** `runner_test.go`: a running session whose log gains the marker from
  `module:claude` is stopped, then re-planned with `findings.md` appended and committed
  work kept; the same answer from the model's own `claude.ask` is ignored; a cursor
  saved across a restart reads only new events.
- **Depends on:** steps 2 and 3.

### 5. Docs

- **Changes:** `docs/how-it-works.md` and `docs/specs/2026-09-30-orchestrated-runs-design.md`'s
  step table, if they list the run's steps: add `replan-review` with a pointer to the
  new spec.
- **Proves it:** a read-through; the full gate stays green.
- **Depends on:** step 4.

## Not doing

- Re-planning the goal.
- Letting brief, plan, verify or tasks sessions claim.
- Changing `auto_ask_after`.
- A protocol or schema change: everything is in the runner and the module's prompt text.
