# nabu

A coding agent that doesn't take its own word for it.

## Why it exists

Four things about working with coding agents wear thin, and nabu is an attempt at all
four.

**It tells you it's finished when it isn't.** The agent reports that the tests pass.
The fix is sitting uncommitted, or one task is still open, or the thing you actually
asked for never got done. You find out later. The model narrating its own success is
not evidence, and most harnesses treat it as though it were.

**It forgets everything the moment you close it.** Every session starts from nothing.
You explain again that this project deploys with `make ship`, that the port is taken,
that you prefer the other approach. You learn to keep a file of things to paste in.

**The run dies with your terminal.** Close the window, lose the work. Shut the laptop
mid-task and it's gone. A long run means babysitting a terminal you can't close.

**You can only reach it from where it's running.** No way to check on a run from
another room, let alone answer a question it's blocked on.

Those are the reasons. Everything below is how nabu addresses them.

## How it addresses them

**It argues with itself before it stops.** When the agent thinks it's done, a stop gate
asks: are tasks still open, did the project's test command pass, did this session leave
uncommitted changes, was the goal actually met? Any objection sends it back to work.
Nothing here trusts the model's account of itself.

**It remembers across sessions.** After a session ends, a curator asks a model whether
anything in it is worth keeping and writes what it finds as markdown under
`~/.nabu/memory`. The next session on that repository starts already knowing. The
memory directory is a git repository, so you can read, diff and revert what it chose to
remember, and delete anything it got wrong.

**The session outlives the window looking at it.** A daemon owns the sessions; the
terminal UI is just a view onto one. Quit it and the run continues. Reattach and the
transcript replays from where you left off. Two clients can watch the same session.

**Any client can attach.** The daemon speaks one documented protocol, so a terminal is
not the only way in. An Android client is being built against the same one, and it
needs no changes to the daemon to exist.

One more, not on that list but worth knowing: it only interrupts you for things that
matter. A guard classifies a command by what it would do rather than what it's called.
`rm -rf build` runs. `rm -rf /etc` asks.

Named for the Mesopotamian god of scribes and record-keeping, because the thing that
makes all of this work is that every session is an append-only log.

> **Status:** early, and built for one person's daily use. The first three of those
> four work today. The fourth is half-built: the Android app speaks the protocol and
> mirrors sessions, but its screens are unfinished. Interfaces still move, and there's
> no release or installer yet.

## Requirements

- Go 1.26 or newer
- An OpenAI-compatible model endpoint. A local `llama-server` works; so does anything
  that speaks the same API.
- Git, optionally. Without it you lose memory versioning and some reporting, nothing
  else.

Developed on Windows, with the tests run on macOS and Linux in CI too. The daemon and
CLI are pure Go with no cgo, so they build and cross-compile anywhere Go runs.

## Install

```bash
git clone https://github.com/corporealshift/nabu
cd nabu
go install ./cmd/nabu
```

That puts `nabu` in your Go bin directory, which you may need to add to your PATH.

When you upgrade later, stop the daemon first with `nabu daemon stop`. A running
daemon holds the binary open and the install will fail.

### Building for another machine

`scripts/build-release.sh` cross-compiles into `dist/` for macOS on Apple silicon and
Intel, Linux on x86-64 and arm64, and Windows. Any one machine builds all of them.

```bash
./scripts/build-release.sh 0.1.0
```

Nothing is code-signed or notarised. On macOS a binary you built or copied across runs
without complaint; one downloaded through a browser is quarantined and needs
`xattr -d com.apple.quarantine nabu` first.

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
