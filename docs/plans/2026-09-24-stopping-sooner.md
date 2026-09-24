# Stopping sooner — plan

Spec: `docs/specs/2026-09-24-stopping-sooner-design.md`. One branch, `stop-sooner`, one PR.
Every step leaves `go build ./... && go vet ./... && go test ./...` green, and each is its own
commit. The four steps touch different files and could land in any order. They are ordered
by how much damage each prevents.

## 1. Guard: rewriting history is high risk

**Depends on:** nothing.

**Changes:**
- `daemon/modules/guard/guard.go`: in `classifyCommand`, a segment whose program is `git`
  is passed to a new `gitTier(args []string) Tier`, which returns `TierHigh` for each row of
  spec §2.3 and `TierMedium` otherwise:
  - `reset` with `--hard`;
  - `push` with `-f`, `--force`, `--force-with-lease[=…]`, or any refspec starting `+`;
  - `branch` with `-D`, `-f` or `--force`, including combined short flags such as `-Df`;
  - `checkout -B`, `switch -C` / `--force-create`;
  - `update-ref`, `rebase`, `filter-branch`, `filter-repo`;
  - `clean` with `-f` in any short-flag cluster;
  - `checkout` with a `--` separator, and `restore` without `--staged`.

  Global options before the verb (`git -C dir …`, `git -c k=v …`) are skipped to find it.
- `daemon/modules/guard/doc.go`: one line saying git is rated by verb and flags.

**Proves it:**
- A table test `TestGitHistoryRewritesAreHighRisk` in `guard_test.go`. It has one row per
  spec §2.3 row, the breezeway command verbatim (`git checkout main && git reset --hard
  HEAD~1 && git checkout -B feat/x && git push -f origin feat/x`), and `git -C repo reset
  --hard`, all `TierHigh`.
- Rows that stay medium: `git push origin feat/x`, `git push -u origin feat/x`,
  `git branch feat/x`, `git branch -d feat/x`, `git checkout feat/x`, `git switch -c
  feat/x`, `git restore --staged f.go`, `git clean -n`, `git reset HEAD~1`, `git reset
  --soft HEAD~1`.
- `TestAutoMode` gains one case: under `auto`, `bash git push --force origin x` gets
  `module.Ask`.

## 2. Loop module: the stalled case

**Depends on:** nothing.

**Changes:**
- `daemon/module/question.go`: `mutatingCommand` adds
  `branch\s+-[a-zA-Z]*[dDfmMcC]`, `update-ref`, `checkout\s+-B` and `switch\s+(-C|--force-create)`.
  `checkout` and `switch` are already there, so the last two are covered, but they get
  test rows.
- `daemon/modules/loop/loop.go`:
  - `Init` reads `stalled_after` (5, at least 2) and `stalled_halt_after` (2, at least 1).
  - A new `turns(log)` groups the model's calls since the person's last message by the
    assistant `message` that made them. For each turn with at least one model tool call,
    it records whether every result, as `status + content`, had already come back earlier
    in the window. It also records the turn's opening sentence (thinking, else message),
    its calls' labels, and its time.
  - A new `stalled(turns) stall` returns the current run length, how many runs have
    reached `stalled_after` since the person spoke, how long the current run has lasted,
    its most repeated label and result, and its most repeated opening sentence.
  - `BeforeRequest`: when the latest turn makes the run exactly `stalled_after` long, it
    appends the stalled notice (text per spec §2.2) and an info `notice`. The watching
    hint is skipped for that request. If an identical-change notice was produced for
    this request, the stalled notice is not.
  - Blocking: the module has no tool call to halt at `BeforeRequest`. So `GateTool` returns
    `module.Halt` for the next model call of any tool when either rule in spec §2.2 holds:
    - the current run has reached `stalled_after + stalled_halt_after`;
    - a second run has reached `stalled_after`.

    `GateTool` now looks at every model call, not only `write`/`edit`. It stays cheap:
    one `s.Events` read, as now.
  - The package doc lists the fourth case.
- `docs/specs/2026-09-23-loop-module-design.md`: under Thresholds, add the two keys, with a
  pointer to the new spec.

**Proves it:**
- `loop_test.go`, built from events as the existing tests are:
  - five turns whose results were all seen before → a stalled notice quoting the opening
    sentence, with no `wait` hint;
  - four such turns, then one new result, then four more → no notice;
  - a run of five, then two more stale turns → the next call is `Halt`;
  - a run of five, a new result, then a second run of five → `Halt`;
  - a person's message resets both counts;
  - an identical-change notice on the same request suppresses the stalled one.
- `question_test.go` `TestChangesWorkspace`: rows for `git branch -D x`, `git branch -f x
  HEAD`, `git update-ref refs/heads/x HEAD`, `git checkout -B x` (true), and `git branch`,
  `git branch -a` (false).
- `NABU_LOOP_REPLAY=~/.nabu/sessions go test ./daemon/modules/loop/ -run Replay -v` prints
  stalled notices and blocks. Compare with spec §4:
  - 9 notices, 7 blocks, all in `01M2Y8K0`, `01M3336A`, `01M360WG`, `01M39RT5` and
    `01M30HBK`;
  - `01M39RT5`: notice at event 281, block at the first call after event 351;
  - no `wait` hint in `01M39RT5` after event 281.

  Put the counts in the PR description. A difference from the spec's script numbers is
  explained there, not tuned away.

## 3. Agent core: stop a reply that repeats itself

**Depends on:** nothing.

**Changes:**
- `daemon/agent/repeat.go` (new): `repeatWatch{limit, minSpan int; text strings.Builder}`
  with `add(s string) (tripped bool)` and `kept() string`.
  - `add` appends, and at most every 256 new bytes checks the tail. It takes the last
    `minSpan` (40) bytes and finds their earlier occurrences within the last 64 KB. Each
    distance `p` to one is a candidate period. The check trips when the last `limit*p`
    bytes are `p`-periodic.
  - `kept` returns the text up to where the periodic run began, plus one period, cut to
    valid UTF-8.
- `daemon/agent/runner.go` `turn`:
  - Two watches, one for deltas and one for thinking, when the limit is at least 1.
  - The call runs under `callCtx, cancel := context.WithCancel(ctx)`. A tripped watch
    records which stream tripped and cancels.
  - When `Complete` returns after a trip (the parent `ctx` is not done):
    - append `thinking` and the assistant `message` with the kept text of each stream;
    - append a warn notice: `the reply repeated one passage N times; stopped after X
      characters and dropped Y. The passage: "…"` (clipped to 200);
    - `finish` to `blocked`, reason `the reply repeated itself`;
    - run no tool calls.
- `daemon/provider/provider.go`: `Config.RepeatLimit int`, with `DefaultRepeatLimit = 8`
  applied in `withDefaults` when 0. A negative value disables the check.
- `daemon/config/config.go`: `ProviderConfig.RepeatLimit int \`json:"repeat_limit"\``, with a
  comment like `MaxTokens`'s. `daemon/daemon.go` passes it through where `MaxTokens` is.
- `README.md`: one sentence beside `max_tokens`.
- `daemon/agent/testdata/degenerate-reply.txt` and `degenerate-thinking.txt`: events 430
  and 429 of `01M39RT5…`, verbatim.

**Proves it:**
- `repeat_test.go`, a table test on `repeatWatch` fed in 20-byte chunks:
  - Must trip: both testdata files, each within the first 4 KB; a 41-byte line × 8.
  - Must not trip:
    - a 39-byte span × 20, which is under `minSpan`;
    - a 41-byte line × 7;
    - a `go test -v` log of 30 passing tests;
    - 12 Go import lines;
    - a markdown table with 10 distinct rows;
    - the 01M39RT5 summary replies from before the loop.
  - `kept()` on the testdata ends with exactly one copy of the paragraph.
- `runner_test.go`, with the fake provider streaming the degenerate reply in chunks:
  - the session ends `blocked` with reason `the reply repeated itself`;
  - the logged message is under 1 KB;
  - the warn notice quotes the passage;
  - a tool call in the same response is not run;
  - with `RepeatLimit: -1` the full reply is logged and the session does not block.
- `config_test.go`: `repeat_limit` round-trips, and 0 becomes 8.

## 4. The prompt: say so when the premise is wrong

**Depends on:** nothing.

**Changes:**
- `daemon/agent/config.go`: the rule from spec §2.1 goes into `DefaultSystemPrompt`, after
  "When the user asks a question…", since both are about when not to act.

**Proves it:**
- `go test ./...` passes. No test pins the prompt text. Check that with
  `grep -rn "DefaultSystemPrompt" --include=*_test.go daemon`, and update any test that
  does pin it.
- By hand, after the PR is merged and the daemon rebuilt: in a scratch repo whose work is
  already on `main`, prompt "create a PR for this work". Pass: the model says the work is
  already on `main` before running any git command that writes. Record the outcome in the
  PR thread, including if it fails. Steps 1–3 are the backstop either way.

## Not doing

- DRY or any other sampling change (spec §5).
- Retrying a degenerate reply (spec §2.4).
- Collapsing repeats when the request is assembled, or any other protocol change. Nothing
  here touches `protocol/spec.md`: `blocked` with a free-text reason already exists.
- Detecting a stall from repeated words alone (spec §5).
- Cleaning up `android-client-m4` in the nabu repo. That's Kyle's call, and separate.
