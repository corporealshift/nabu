# Configuration

Everything lives under `~/.nabu` (or `--root`, or `$NABU_ROOT`). There are three files:

| File | Read by | Needed |
|---|---|---|
| `config.json` | the daemon | yes |
| `runner/config.json` | `nabu runner` | no; every field has a default |
| `github/config.json` | `nabu github`, and `nabu runner` when it exists | only for the GitHub jobs |

The daemon rejects unknown fields in `config.json`, so a typo fails at startup instead
of doing nothing. The daemon and the runner read their files once, when they start, so
restart them after a change. Restart the daemon only when no session is running. A
session that is running when the daemon stops is paused, and the runner counts a paused
step session as a failed one.

The README has a [recommended setup](../README.md#recommended-setup-qwen36-35b-a3b) for a
local model. This page lists every setting.

## The model

```json
{
  "daemon": { "default_model": "local/my-model" },
  "providers": {
    "local": {
      "base_url": "http://localhost:8033/v1",
      "api_key": "none",
      "context_window": 128000
    }
  }
}
```

- **`default_model`** is `provider/model`. The part before the first slash picks the
  provider block, and the rest is sent to the endpoint as the model name.
- **`context_window`**: set it. Without it nabu can't tell how full the context is, so it
  never compacts, and a long session grows until the server refuses the request. The
  clients also use it to show how full the window is.
- **`max_in_flight`**: how many requests nabu sends the endpoint at once. Default 1, which
  suits a local server running one slot.
- **`max_tokens`**: caps one reply. The default is 16384, and never more than the context
  has room for. Without a cap, a local model stuck in a loop writes until the ten-minute
  timeout and the turn is lost. A hosted API that allows less output refuses the request
  outright, so set it lower there.
- **`repeat_limit`**: ends a reply early when the model writes the same passage over and
  over. The default is 8 copies in a row of the same 40 bytes or more. A negative value
  turns it off. The copies are not logged, and the session blocks so you can look.
  Repeated thinking is only trimmed from the log, and the turn goes on.

### Compaction

As the context fills, compaction runs in two stages. At 70% of the window, old tool
results are swapped for one-line stubs. At 85%, the conversation is summarized. Each
provider has three settings for this:

- **`normal_window`**: the window a normal session compacts against, smaller than
  `context_window`. A session switched to large context (`/context large`) uses all of
  `context_window`. A run or goal started from a large session makes all of its sessions
  large.
- **`clear_at`**: the fraction at which old results are cleared. Below 0 means never.
- **`summarize_at`**: the fraction at which the conversation is summarized.

For a local server, use `"normal_window": 64000, "clear_at": -1`. Clearing changes
messages a few turns back, so a local server's prompt cache misses and it reprocesses
everything after them. It also takes away files the model read a few turns earlier,
which it then reads again. In one measured 400-turn session, clearing fired every 4 or
5 turns and freed about one turn's growth each time. Summarizing earlier keeps the
context short instead, and a local model is fastest with a short context.

## Budgets

```json
{ "budget": { "max_turns": 0, "max_consecutive_vetoes": 5, "no_progress_turns": 3 } }
```

- **`max_turns`**: the turn cap for a session that sets none itself. 0 means no cap. A
  session that uses up its budget pauses, and `nabu resume <id>` continues it. The runner
  sets its own caps (see [The runner](#the-runner)).
- **`max_consecutive_vetoes`**: how many times in a row the stop gates may refuse to let
  a session finish before it blocks.
- **`no_progress_turns`**: how many identical refusal rounds with no tool use end the
  session sooner.

## Sessions

- **Permission mode**: sessions default to `auto`, where the guard decides and only
  dangerous calls reach you. `ask` prompts for everything, and `bypass` prompts for
  nothing. The mode is set per session.
- **Archiving**: a session with no activity for three days is archived. It leaves the
  list and the daemon stops loading it, but nothing in it is deleted. A running session is
  never archived. Set `{ "daemon": { "archive_after_days": 7 } }` to change the wait, or 0
  to turn archiving off.
- **`daemon.log_level`**: `debug`, `info`, `warn` or `error`.

## Remote access

The daemon binds to loopback. To reach it from a phone, bind wider and set a token:

```json
{ "daemon": { "bind": "0.0.0.0:8737", "token": "a-long-random-string" } }
```

A connection from anywhere but loopback is refused without that token. The daemon runs
shell commands in your repositories, so use Tailscale or another private network rather
than exposing the port. Phone notifications are set up in [android.md](android.md).

## Modules

Each module has a block under `modules`. All of them are optional.

### `verify`: your test command

```json
{ "modules": { "verify": {
  "command": "go build ./... && go test ./...",
  "commands": { "C:/src/app": "cd android && ./gradlew.sh :app:assembleDebug :app:testDebugUnitTest" },
  "command_timeout": 300
} } }
```

- **`command`** is the project gate. It runs only when a turn changed something, so a
  turn that only read files is never held up by a build.
- **`commands`** sets the gate for one repository, keyed by its path. It replaces
  `command` there, and an empty string turns the gate off for that repository.
- **`command_timeout`** is in seconds, default 300. Raise it for a slow build.

How strictly the gate is enforced depends on whether anyone is watching:
- **In an ordinary session**, when a turn changed files, nabu reminds the agent once. The
  reminder says what is uncommitted or unpushed, whether there is a pull request, which
  tasks are open, and what the gate said. Then it lets the agent stop.
- **In a session with a goal**, which every runner work session has, nobody is there to
  say "carry on". So the agent can't finish while the gate fails, the tree is dirty, or
  tasks are open.

### `claude`: a second opinion from Claude

`claude.ask` runs the `claude` CLI in the session's workspace, so Claude reads the same
repository nabu is working in. The tool appears only when `claude` is on PATH. It uses
the machine's Claude Code login, and each call spends your allowance.

```json
{ "modules": { "claude": {
  "enabled": true,
  "timeout_seconds": 300,
  "model": "",
  "allowed_tools": ["Read", "Grep", "Glob"],
  "commands": ["go test", "go vet", "bash gradlew.sh"],
  "auto_ask_after": 100,
  "auto_ask_max": 2
} } }
```

- **`allowed_tools`** defaults to reading only. A reviewer that can quietly edit the
  repository defeats the point of a review.
- **`commands`** are the commands Claude may run, so it can see a failure for itself. Each
  one becomes a `Bash(<command> *)` and a `PowerShell(<command> *)` rule. Claude can run
  those and nothing else, and still can't change a file. A command that would allow
  anything, such as a bare `bash`, is refused and logged.
- **`auto_ask_after`**: nabu asks Claude on the session's behalf after this many turns
  since anyone last sent the session a message. It asks again at each multiple, up to
  `auto_ask_max` times. The question carries the task, what the model has been saying,
  what keeps failing and the files it keeps changing. The model keeps working while Claude
  looks. The answer arrives as an ordinary `claude.ask` call in the conversation. Sessions
  in `ask` permission mode are skipped, because you are there to be asked. 0 turns it off.

### `builtins`: reading another repository

A session is bound to one workspace, and its writes never leave it. Reading can, if you
name the repositories it may read:

```json
{ "modules": { "builtins": { "workspaces": {
  "nabu": "C:/Users/you/projects/nabu",
  "web": "C:/Users/you/projects/web"
} } } }
```

`read`, `glob` and `grep` then take an optional `workspace` naming one of these, and
`write`, `edit` and `bash` do not. The model gives names, not paths, and a path that
climbs out of a named root with `../` is refused. With nothing configured, the argument
isn't offered at all.

### `memory`

On by default. After a session ends, a curator writes anything worth keeping as markdown
under `~/.nabu/memory`, which is a git repository you can read, diff and revert. To seed
it read-only from a Claude Code memory directory:

```json
{ "modules": { "memory": { "import_dirs": ["~/.claude/projects/<project>/memory"] } } }
```

`"curator": false` turns the automatic writing off. `memory.save` still works when the
model chooses to use it.

### `notes`: working notes

The agent keeps notes on work in progress in a repository: dead ends, ordering
constraints, how far through a refactor it got. A note expires when nobody has rewritten
it for `expire_days`, so notes don't turn into a second memory.

```json
{ "modules": { "notes": { "enabled": true, "expire_days": 14, "max_bytes": 4096 } } }
```

Notes are plain markdown at `~/.nabu/notes/ws/<workspace-key>/<name>.md`. `nabu notes`
prints this repository's notes, and `nabu notes --all` prints every repository's. The key
is per repository, so worktrees and subdirectories of one repo share their notes.

### `skills`

These are markdown instructions the agent loads on demand. It reads `~/.claude/skills` and
`~/.nabu/skills` by default:

```json
{ "modules": { "skills": { "paths": ["~/.nabu/skills"] } } }
```

A skill can name the files it covers with a `paths:` line in its frontmatter, such as
`paths: **/*.gradle.kts, **/AndroidManifest.xml`. The first time the agent touches a
matching file without having loaded the skill, it is told to load it.

### `web`

Off until you give it a key:

```json
{ "modules": { "web": { "provider": "tavily", "brave_api_key": "...", "tavily_api_key": "..." } } }
```

- **Brave** returns links and snippets, which the model follows with `web.fetch`.
- **Tavily** returns cleaned page text and often a direct answer.

With both keys, the model chooses for each search: `mode: "links"` or `mode: "answer"`.
`provider` is the default when a search names no mode. With one key, that service answers
everything. `web.fetch` reads http and https only, and caps a page at 200 KB.

### `watch`: noticing your own edits

When you edit the workspace from another terminal while a session runs, the agent is told
which paths changed, before its next request. Its own `write` and `edit` calls are left
out. Dotted paths, `node_modules`, `build`, `target` and `vendor` are skipped. A workspace
with more than `max_files` files isn't scanned at all.

```json
{ "modules": { "watch": { "enabled": true, "max_files": 20000, "max_reported": 20 } } }
```

### `answer`: questions get answers

When your last message only asks something, such as "why did it stop?", the agent may
read and run commands to answer it. An edit or a commit waits for your approval. A
message that asks for something, like "can you…" or "fix…", is an ordinary turn. To turn
this off, use `{ "modules": { "answer": { "enabled": false } } }`.

### `artifact`: pages the agent makes

The agent can make a page for a chart, a sortable table or a diagram. In the terminal UI,
press `o` to open the newest one, or use `/open <name>`. A page can run scripts but can't
reach the network. To turn this off, use `{ "modules": { "artifact": { "enabled": false } } }`.

## The runner

`nabu runner` takes work to pull requests. `/run` and `/goal`, from the terminal UI or the
phone, hand work to it. With `github/config.json` present, it does the GitHub jobs too.
It needs `git`, `gh`, and the `claude` CLI. A run never skips its reviews, so without
`claude` the runner won't start a run or a goal.

- **A run** takes one brief through fixed steps. Each step is its own session:
  1. brief and plan, which Claude reviews;
  2. `verify.sh`, the check that says when the brief is done, written before any code and
     reviewed by Claude;
  3. tasks, then one session per task;
  4. the check, and fixes until it passes;
  5. a final review by Claude;
  6. a pull request, fixed until CI is green.
- **A goal** is broader. Claude breaks it into briefs, runs them one after another, and
  checks the result. Each round's check plans the next round from whatever is still
  missing, until the goal is met.

`runner/config.json`, with the defaults:

```json
{
  "poll": "2m",
  "max_jobs": 1,
  "label": "nabu",
  "verify_timeout": "30m",
  "claude": { "path": "", "model": "", "timeout": "15m" },
  "plan_turns": 0,
  "work_turns": 0,
  "goal_rounds": 5
}
```

- **`plan_turns`** caps brief, plan, tasks and verify sessions. **`work_turns`** caps
  work, fix and CI-fix sessions. 0 means no cap. A session that hits its cap is a failed
  try. Its commits are thrown away, and the step is tried once more.
- **`goal_rounds`** is how many rounds a goal gets before it blocks and waits for you.
  When one run fails, the rest of its round is dropped and the goal goes to its check. So
  on the last round, a single failure blocks the goal. To resume a blocked goal, send
  `/goal` again in its session, with new text if you want to steer it. That gives it a
  fresh allowance of rounds.
- **`label`** goes on every PR a run opens, so the comments job answers review comments
  on it.

## The GitHub jobs

`github/config.json` names the repositories to watch. Each needs an existing clone, which
the jobs fetch into and add worktrees beside, but never check anything out in:

```json
{
  "repos": [{ "name": "owner/repo", "clone": "C:/src/repo" }],
  "label": "nabu",
  "poll": "2m",
  "quiet": "5m",
  "max_jobs": 1,
  "review":   { "enabled": true, "pushes": true, "max_turns": 0 },
  "comments": { "enabled": true, "max_turns": 0 },
  "issues":   { "enabled": true }
}
```

- **review**: reviews open pull requests. A head has to stay the head for `quiet` first,
  so a burst of pushes gets one review. `pushes: false` reviews only a PR's first head.
- **comments**: answers review comments on pull requests that carry `label`.
- **issues**: starts a run for each open issue that carries `label`. The run's PR closes
  the issue.

`nabu github --once` polls once and exits. `--dry-run` prints instead of posting.

## Where things live

```
~/.nabu/
  config.json
  sessions/     one append-only .jsonl per session
  memory/       markdown, and a git repository
  notes/        working notes, per repository
  skills/
  modules/      whatever a module keeps for itself
  runner/       config.json, runs/, goals/ and the run worktrees
  github/       config.json, state and the job worktrees
  daemon.log    beside daemon.pid and daemon.port
  devices.json  the phones that asked to be notified
```

Session logs are plain JSON lines, readable with `cat`. `nabu runner` and `nabu github`
log to stderr.
