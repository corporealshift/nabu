# Sessions with a parent and labels, and `/run` — plan

Spec: `docs/specs/2026-09-30-session-parent-and-labels-design.md`. One branch,
`runs-labels`, and one PR. Every step leaves
`go build ./... && go vet ./... && go test ./...` green and is its own commit. The steps
run in order: contract, then daemon, then client.

The other three parts' branches (`runs-runner`, `runs-ci`, `runs-issues`) hold only their
specs. Each is rebased onto `main` after the part before it merges, and gets its own plan
then.

## 1. The contract

**Depends on:** nothing.

**Changes:**
- `protocol/types.go`:
  - `Options` gains `Parent string` (`json:"parent,omitempty"`) and `Labels []string`
    (`json:"labels,omitempty"`).
  - `ValidLabels([]string) error` enforces at most 16 labels, each 1 to 64 characters
    from `[a-z0-9:_./-]`. It lives here so the daemon and any client share one rule.
- `protocol/project.go`: an `options_change` with key `labels` sets `Options.Labels`.
  `parent` is only ever read from the `session` event.
- `protocol/validate.go`: the allowed `options_change` keys gain `labels`, and a
  `labels` value must pass `ValidLabels`.
- `protocol/schema/event.json`:
  - the options object gains optional `parent` (string) and `labels` (array of strings
    matching the pattern, `maxItems` 16);
  - the `options_change` key enum gains `labels`.
- `protocol/spec.md`: §3.1 `session` and `options_change`, §5, §7.2, §7.3 and §7.13, as
  the spec says.
- `protocol/vectors/projection/06-parent-and-labels.json`: a session created with a
  parent and one label, relabeled twice, projects to the last labels and the same
  parent.
- `protocol/vectors/validation/`: a new vector rejecting an `options_change` whose labels
  include `Bad Label`.

**Proves it:**
- `go test ./protocol/` runs both new vectors.
- `TestValidLabels`: a table covering empty, 16 and 17 labels, a 64-character and a
  65-character label, an uppercase letter, a space, and each allowed punctuation mark.
- If Gradle runs on this machine, `./gradlew :app:testDebugUnitTest` in `clients/android`
  still passes the projection vectors, since its decoder ignores unknown keys. If it does
  not run, the PR says so.

## 2. The daemon accepts and lists them

**Depends on:** step 1.

**Changes:**
- `daemon/api/handler.go`: `CreateSessionOptions` gains `Parent` and `Labels`.
- `daemon/agent/manager.go`:
  - `CreateOptions` gains `Parent` and `Labels`. `Create` refuses with
    `nabu_invalid_params` if the parent is unknown (neither live nor archived), or if the
    labels fail `ValidLabels`. They are recorded in the `session` event's options.
  - `SetOption`:
    - `labels` takes a JSON array of strings, checked with `ValidLabels`, and records
      `from` as the current labels;
    - `parent` is refused with "parent is set at creation and never changes".
- `daemon/session/store.go`: `Summary` gains `Parent` and `Labels` from the projection.
- `clients/goclient/client.go`: `SessionSummary` gains `Parent` and `Labels`.

**Proves it:** tests in `daemon/api` against a real handler:
- create with a parent and labels, which then show in the `session` event, `state` and
  `list`;
- create with an unknown parent, which is refused and creates nothing;
- `set_option labels`, which appends `options_change`, and `list` shows the new labels;
- `set_option parent`, which is refused;
- invalid labels, at creation and in `set_option`, which are refused with nothing
  appended.

## 3. The TUI groups runs and starts them

**Depends on:** step 2.

**Changes:**
- `clients/go-tui/view.go`:
  - `picker` orders the list so that each session whose parent is in the list follows
    its parent, indented one level. A session whose parent is not listed is shown at the
    top level.
  - `pickerRow` shows a home session's run status from its labels: `run: <step>`, plus
    ` (<n>/<max>)` when a `run:attempt:<n>/<max>` label is present.
- The status line shows `↑ <parent short id>` for a session with a parent.
- `clients/go-tui/commands.go`: `/run [text]`:
  - with text, `set_goal` to the text, then `set_option labels`, keeping the existing
    labels minus any `run:*` label, plus `run:requested`;
  - without text, only the labels;
  - on a session labeled `run:failed`, the same, which resumes it;
  - `/run` is listed in `/help` as "hand this session to the runner as a brief".

**Proves it:** TUI tests:
- the picker order and indentation with a parent present and with it absent;
- the run status text from labels, with and without an attempt label;
- `/run text` sends `set_goal` then `set_option`, with the labels computed as above, and
  `/run` alone sends only `set_option`;
- `/help` lists `/run`.

## Checking it for real

With the branch's binary running as a scratch daemon (`--root`, its own port):
1. create a session;
2. create a second with the first as its parent;
3. `/run fix the thing` in the first, from the TUI;
4. check `nabu status` and the TUI picker show the child indented, and the first
   labeled `run:requested` with the goal set.

That's all this part can show. No runner exists until part 2.

## Not doing

- Anything that acts on the labels. That's part 2.
- The Android and Rust clients showing groups.
