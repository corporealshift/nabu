# nabu

A coding agent that runs as a daemon on your machine, keeps every session as an
append-only log, and lets thin clients attach to it. Close the terminal and the run
keeps going. Open it again and the transcript replays from where you left off.

Named for the Mesopotamian god of scribes, because the central idea is the log.

> **Status:** early, and built for one person's daily use. It works: it drives a local
> model through real work, remembers what it learns, and refuses to claim it is done
> when it isn't. Interfaces still move. There is no release, no installer, and the
> Android client is half-built.

## Why it exists

Most coding agents are a process attached to your terminal. Close it and the work dies
with it. nabu splits that in two:

- A **daemon** owns the sessions, the model calls, the tools and the policy.
- **Clients** are windows onto it. A terminal UI, a headless CLI, eventually a phone.

Because the daemon owns the log, and every model request is built from that log, you
get some things for free. A run survives your terminal. Two clients can watch one
session. A laptop that closes mid-run picks up where it stopped.

## What makes it different

**It argues with itself before it stops.** When the agent thinks it's finished, a stop
gate asks: are tasks still open, did the project's test command pass, did this session
leave uncommitted changes, was the goal actually met? Any objection sends it back to
work. "Tests pass" while the fix sits uncommitted is the failure this is built to
prevent.

**It remembers across sessions.** After a session ends, a curator asks a model whether
anything in it is worth keeping, and writes what it finds as markdown files under
`~/.nabu/memory`. The next session on that repository starts with those facts in front
of it. The memory directory is a git repository, so you can read, diff and revert what
it decided to remember.

**It only interrupts you for things that matter.** The guard classifies a command by
what it would do, not what it is called. `rm -rf build` runs. `rm -rf /etc` asks. It
ships compiled in, so it works before you configure anything.

## Requirements

- Go 1.26 or newer
- An OpenAI-compatible model endpoint. A local `llama-server` works; so does anything
  that speaks the same API.
- Git, optionally. Without it you lose memory versioning and some reporting, nothing
  else.

Developed on Windows. The daemon and CLI are pure Go and should build anywhere Go does.

## Install

```bash
git clone https://github.com/corporealshift/nabu
cd nabu
go install ./cmd/nabu
```

That puts `nabu` in your Go bin directory, which you may need to add to your PATH.

When you upgrade later, stop the daemon first with `nabu daemon stop`. A running
daemon holds the binary open and the install will fail.

## Configure

nabu keeps everything under `~/.nabu`. Create `~/.nabu/config.json`:

```json
{
  "daemon": {
    "default_model": "local/my-model"
  },
  "providers": {
    "local": {
      "base_url": "http://localhost:8033/v1",
      "api_key": "none",
      "context_window": 128000
    }
  }
}
```

That is the minimum. Two things to know:

- **`default_model` is `provider/model`.** The part before the slash picks the provider
  block; the rest is sent to the endpoint as the model name.
- **Set `context_window`.** Without it nabu cannot tell how full the context is, so it
  never compacts, and a long session grows until your model refuses the request.

Unknown fields are rejected rather than ignored, so a typo fails loudly at startup
instead of silently doing nothing.

## Use it

```bash
nabu
```

That's the whole thing. It creates a session in the current directory, starts a daemon
if one isn't running, and opens the terminal UI.

| Key | What it does |
|---|---|
| `i` | Type a prompt |
| `s` | Switch sessions |
| `y` / `n` | Answer a permission prompt |
| `ctrl+x` | Interrupt the current turn |
| `g` / `G` | Jump to the top or bottom of the transcript |
| `q` | Quit, leaving the run going |

Quitting does not stop the run. Reopen with `nabu --session <id>` or press `s` and pick
it.

### Headless

```bash
nabu run "add a health endpoint and a test for it"
```

Runs to completion, prints a report, and exits with a status that says what happened:
0 completed, 1 blocked, 2 paused, 3 error. Good for scripting, and for handing work to
nabu from another agent.

Add a goal and it is judged in a fresh context before the run is allowed to finish:

```bash
nabu run --done-when "the new endpoint has a passing test" "add a health endpoint"
```

### The rest

```bash
nabu status            # list sessions
nabu status <id>       # one session's state
nabu attach <id>       # stream a session's events
nabu stop <id>         # end a session
nabu resume <id>       # resume a paused one
nabu daemon            # run the daemon in the foreground
nabu daemon stop       # stop it
```

## What the agent can do

Built-in tools: `read`, `write`, `edit`, `glob`, `grep`, `bash`, and `task.update` for
its own task list. Modules add `skill.load`, `memory.recall`, `memory.save` and
`memory.forget`.

## Making it yours

Everything below is optional. nabu works with none of it.

### Your test command

```json
{ "modules": { "verify": { "command": "go build ./... && go test ./..." } } }
```

Now the agent cannot finish a run while that command fails. This is the single most
useful thing to configure.

### Memory

On by default. To seed it from an existing Claude Code memory directory, read-only:

```json
{ "modules": { "memory": { "import_dirs": ["~/.claude/projects/<project>/memory"] } } }
```

Turn the automatic writing off with `"curator": false` and the `memory.save` tool still
works when the model chooses to use it.

### Skills

Markdown instructions the agent can load on demand. It reads `~/.claude/skills` and
`~/.nabu/skills` by default; point it elsewhere with
`{"modules": {"skills": {"paths": ["..."]}}}`.

### Permission mode

Sessions default to `auto`: the guard decides, and only genuinely dangerous calls reach
you. `ask` prompts for everything, `bypass` for nothing. Per session, not global.

### Budgets

```json
{ "budget": { "max_turns": 100 } }
```

Turns are the unit. A run that exhausts its budget pauses rather than dying, and
`nabu resume` continues it.

## Clients

- **Terminal UI** — the default, in this repo, built with the daemon.
- **Headless CLI** — `nabu run`, same binary.
- **Android** — Kotlin and Compose, under `clients/android`. Partly built: it speaks
  the protocol and mirrors sessions locally, but the screens are unfinished.
- **Desktop GUI** — a later milestone, not started.

The protocol is JSON-RPC over WebSocket and is specified in `protocol/spec.md` with
conformance vectors, so a client can be written in anything.

## Remote access

The daemon binds to loopback by default. To reach it from a phone, bind wider and set a
token:

```json
{ "daemon": { "bind": "0.0.0.0:8737", "token": "a-long-random-string" } }
```

A connection from anywhere but loopback is refused without that token. This matters:
the daemon runs shell commands in your repositories. Use Tailscale or a comparable
private network rather than exposing the port.

## Where things live

```
~/.nabu/
  config.json
  sessions/     one append-only .jsonl per session
  memory/       markdown, and a git repository
  skills/
  daemon.log
```

Session logs are plain JSON lines. You can read one with `cat`, and nothing is hidden
from you.

## Contributing

Read `ARCHITECTURE.md` first; it is short and states the invariants. The full design,
including every rejected alternative, is under `docs/superpowers/specs/`.

The gate, which CI runs on Linux and Windows:

```bash
go build ./... && go vet ./... && go test ./... && gofmt -l .
```

Policy belongs in a module under `daemon/modules/`, not in the daemon core. Changing
the protocol means changing the spec, the JSON schema and a conformance vector together.

## Licence

MIT. See `LICENSE`.
