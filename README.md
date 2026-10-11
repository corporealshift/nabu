# nabu

A coding agent built to get real work out of a local model. It ships as one Go binary
containing a daemon, a terminal UI and a headless CLI, plus an Android client that
connects to the daemon.

Nabu was the Mesopotamian god of scribes and record-keeping. Every session is an
append-only log, and every request to the model is built from that log.

> **Status:** early, and built for one person's daily use. It drives a local model
> through real work every day. Interfaces still change, and there is no release or
> installer: you build both clients from source.

## Why nabu

Most coding agents assume a frontier model that follows a long workflow and knows when
it's done. A local model like Qwen3.6-35B-A3B does neither reliably. It drifts from the
task, repeats itself, and says it has finished when it hasn't. nabu is built around those
failures.

- **It doesn't take the model's word for it.** When the agent says it's done, stop gates
  check for open tasks, failed checks, a failing test command, and uncommitted changes.
  When a goal is set, a judge with a fresh context checks that too. Any objection sends
  the agent back to work.
- **It takes work all the way to a merged PR.** `nabu runner` puts each brief through
  fixed steps: plan, write the check (`verify.sh`) before any code, tasks, fixes until the
  check passes, a pull request, and fixes until CI is green. The model never has to
  remember the workflow, because each step is its own short session. A goal is broader:
  nabu splits it into briefs and runs them in rounds until a check finds the goal met.
- **The local model does the bulk of the work; Claude reviews.** Claude reviews the plan,
  the check and the finished work. It also steps in when a session stalls, and it can run
  your tests to see the failure for itself. So your Claude allowance goes on a few
  judgment calls, and the local model does the many turns of work.
- **Sessions live in the daemon, not the terminal.** Close the terminal and the work goes
  on. Reattach from the terminal UI or your phone, answer the agent's questions from
  either, and get a notification when a run finishes or needs you.
- **It remembers.** A curator keeps facts worth keeping in a git-backed memory, and the
  agent keeps working notes that carry a task across sessions and then expire.
- **You own it.** It's one binary with no runtime to install, and it builds for Windows,
  macOS and Linux. There's no plugin system: policy is Go modules compiled in, so to
  change how it behaves, you change the source.

## Install

You need:
- Go 1.26 or newer;
- an OpenAI-compatible model endpoint, such as `llama-server`.

Git is optional, but memory versioning and runs need it. Runs also need the `claude` CLI,
and `gh` to open pull requests.

```bash
git clone https://github.com/corporealshift/nabu
cd nabu
go install ./cmd/nabu
```

That puts `nabu` in your Go bin directory. To upgrade, run `nabu daemon stop` first,
because a running daemon holds the binary open. To build for other platforms, use
`./scripts/build-release.sh 0.1.0`. It cross-compiles for macOS, Linux and Windows into
`dist/`. Nothing is signed. On macOS, a binary downloaded through a browser needs
`xattr -d com.apple.quarantine nabu` before it will run.

## Quick start

Create `~/.nabu/config.json`:

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

`default_model` is `provider/model`: `local` picks the provider block, and the rest is
sent to the server as the model name. Always set `context_window`. Without it, nabu never
compacts, and a long session grows until the server refuses it.

Then, in a repository:

```bash
nabu
```

This starts a daemon if none is running, creates a session in the current directory, and
opens the terminal UI.

## Recommended setup: Qwen3.6-35B-A3B

This is the setup nabu is developed and run with every day. Run llama-server like this:

```bash
llama-server -m Qwen3.6-35B-A3B-UD-Q4_K_XL-MTP.gguf \
  --spec-type draft-mtp --spec-draft-n-max 2 \
  --fit on --fit-ctx 128000 -np 1 \
  -fa on -ctk q8_0 -ctv q8_0 -b 1024 -ub 1024 \
  --temp 0.6 --top-p 0.95 --top-k 20 --min-p 0.0 --presence-penalty 1.5 \
  --reasoning-budget 4096 --reasoning-budget-message "... thinking budget exceeded, let's answer now." \
  --chat-template-kwargs '{"preserve_thinking": true}' \
  --port 8033
```

- **The sampling settings and `preserve_thinking`** follow the
  [model card](https://huggingface.co/Qwen/Qwen3.6-35B-A3B)'s advice for thinking mode
  and agents. The presence penalty makes the model less likely to repeat itself.
- **`--reasoning-budget`** stops the model thinking forever.
- **`-np 1`**: one slot, so one request at a time. nabu's `max_in_flight` defaults to 1 to
  match.
- **The MTP build with `draft-mtp`** speeds up generation by drafting tokens
  speculatively. Without an MTP model file, leave both `--spec` flags out.

Then set up nabu.

**`~/.nabu/config.json`**:

```json
{
  "daemon": { "default_model": "local/qwen3.6-35b-a3b" },
  "providers": {
    "local": {
      "base_url": "http://localhost:8033/v1",
      "api_key": "none",
      "context_window": 128000,
      "normal_window": 64000,
      "clear_at": -1
    }
  },
  "modules": {
    "verify": { "command_timeout": 900 },
    "claude": {
      "commands": ["go test", "go vet", "bash gradlew.sh"],
      "auto_ask_after": 50
    }
  }
}
```

**`~/.nabu/runner/config.json`**:

```json
{ "work_turns": 130, "goal_rounds": 10 }
```

Why these values:

- **Context: `normal_window: 64000` and `clear_at: -1`.** These summarize the
  conversation at 85% of 64k, and never clear old tool results. Clearing results breaks
  the local server's prompt cache, and the model just reads the same files again. A short
  context is where this model is fastest.
- **Asking Claude: `auto_ask_after: 50`.** Half of all work sessions in past runs
  finished within 42 turns, but about one in five ran past 150. The long ones we looked
  at closely were stuck on one thing an outside look fixed: a test asserting the wrong
  thing, or failure output cut off by `| tail`. At 50 turns nabu asks Claude what's going
  wrong, and asks again at 100. The model keeps working meanwhile, and nothing is stopped.
- **`claude.commands`** lets Claude run your build and tests. Name the commands your
  projects use.
- **Turn cap: `work_turns: 130`.** Each work session gets two rounds of help before the
  cap. A session that hits it is retried once from a clean start. If it fails again, the
  goal re-plans that work, usually as smaller runs.
- **Rounds: `goal_rounds: 10`.** When one run fails, the rest of its round is dropped and
  the goal re-plans. With 10 rounds, a goal can recover from a few failures overnight
  without stopping to wait for you.
- **`command_timeout: 900`** gives an Android or Rust build time to finish.

Runs and goals need the `claude` CLI on PATH, logged in to Claude Code. A run never
skips its reviews, so without the CLI the runner won't start one. Interactive sessions
work without it, but they get no `claude.ask` and no automatic asks.

## Using it

### The terminal UI

| Key | What it does |
|---|---|
| `i` | Type a prompt |
| `s` | Switch sessions |
| `t` | Show or hide the model's thinking |
| `o` | Open the newest page the agent made |
| `y` / `n` | Answer a permission prompt |
| `ctrl+x` | Interrupt the current turn |
| `g` / `G` | Jump to the top or bottom of the transcript |
| `?` | List every key |
| `q` | Quit, leaving the session running |

When the agent asks you something, the question takes over the screen. Type a number to
pick one of the offered answers, or type your own and press enter.

A line starting with `/` is a command:

| Command | What it does |
|---|---|
| `/run [text]` | Hand the work to the runner, as one brief that becomes one PR |
| `/goal [text]` | Hand the runner a broader goal, split into runs until it's met |
| `/compact` | Summarize the history now (interrupt a running turn first) |
| `/context large` | Let the session fill the whole window before summarizing; `/context normal` undoes it |
| `/stats` | Turns, tokens, how the context grew, tool failures, files read over and over |
| `/open <name>` | Open a page the agent made |
| `/archive`, `/stop`, `/sessions`, `/help` | What they say |

Quitting doesn't stop the session. Reopen it with `nabu --session <id>`, or press `s` and
pick it.

### Runs and goals

`/run` and `/goal` only queue the work. The runner does it:

```bash
nabu runner
```

Leave it running. It does one run at a time, and polls every two minutes. Each run works
in its own worktree on its own branch, and opens a PR labeled `nabu`. A goal's runs merge
into the goal's branch, and the goal opens one PR at the end. The steps and settings are
described in [docs/configuration.md](docs/configuration.md#the-runner).

With `~/.nabu/github/config.json`, the runner also:
- reviews open pull requests;
- answers review comments on PRs labeled `nabu`;
- starts a run for each issue labeled `nabu`.

To run only those GitHub jobs, use `nabu github`.

### Headless

```bash
nabu run "add a health endpoint and a test for it"
```

This runs to completion and prints a report. The exit status says how it ended: 0
completed, 1 blocked, 2 paused, 3 error.

### Other commands

```bash
nabu status [id]       # list sessions, or show one
nabu attach <id>       # stream a session's events
nabu stop <id>         # end a session
nabu resume <id>       # resume a paused session
nabu archive <id>      # put a session away; nabu restore <id> brings it back
nabu notes [--all]     # the agent's working notes for this repository
nabu stats [id]        # a session's numbers as JSON, or tokens per day
nabu daemon            # run the daemon in the foreground
nabu daemon stop       # stop it
```

## More

- [docs/configuration.md](docs/configuration.md): every setting. This covers models,
  compaction, budgets, each module, the runner and the GitHub jobs.
- [docs/android.md](docs/android.md): the phone client, remote access and notifications.
- [docs/how-it-works.md](docs/how-it-works.md): the session log, stop gates, permission,
  memory and the tools.
- [ARCHITECTURE.md](ARCHITECTURE.md): the map and its invariants. Every design decision is
  under `docs/specs/`.
- [bench/README.md](bench/README.md) runs the same tasks through nabu, pi and Claude
  Code, and compares the results.

## Contributing

Read `ARCHITECTURE.md` first. The gate, which CI runs on Linux, macOS and Windows:

```bash
go build ./... && go vet ./... && go test ./... && gofmt -l .
```

Policy belongs in a module under `daemon/modules/`, not in the daemon core. Changing the
protocol means changing the spec, the JSON schema and a conformance vector together.

## Licence

MIT. See `LICENSE`.
