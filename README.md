# nabu

A self-contained, custom, multiplatform coding harness built in Go.

Nabu was the Mesopotamian god of scribes and record-keeping. The name is the design:
every session is an append-only log, and every request to the model is built from that
log. Most of what follows falls out of it.

I built it because I wanted a Claude Code-like experience that I own outright. There is
no plugin system and no extension API, because there is nothing to extend around: if it
should behave differently, I change it. The source is the customisation layer.

> **Status:** early, and built for one person's daily use. It drives a local model
> through real work every day. Interfaces still move, there is no release or installer,
> and the Android client speaks the protocol but its screens are unfinished.

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

### Searching the web

Off until you give it a key. Add a `modules.web` block:

```json
{
  "modules": {
    "web": {
      "provider": "brave",
      "brave_api_key": "...",
      "tavily_api_key": "..."
    }
  }
}
```

Two services, because they answer different questions:

- **Brave** returns an index's links and snippets, which the model follows with
  `web.fetch`. The free tier needs an account but no card.
- **Tavily** is built for agents: it returns cleaned page text and often a direct
  answer, so a search costs fewer follow-up fetches.

Configure both and **the agent chooses per search**, by what it wants back rather than
by which company it asks: `mode: "links"` for ranked sources to follow with `web.fetch`,
`mode: "answer"` for a read reply to a small factual question. The choice only appears
in the tool's schema when both keys are configured, so the model is never offered a
decision it cannot act on.

`provider` is the default for a search that names no mode, not a restriction. With one
key, that service answers everything and the mode parameter is not offered. With
neither key the module offers no tools at all, rather than a tool that fails the first
time the model reaches for it.

`web.fetch` reads http and https only, caps a page at 200 KB, and is classed
medium-risk: what comes back is whatever the page decided to say.

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
| `t` | Show or hide the model's thinking |
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

## Why it looks like this

**Self-contained.** One binary. No Node, no Python, no runtime to install, nothing
downloaded at startup. The daemon, the terminal UI and the headless CLI are the same
executable, and the only hard dependency is a model endpoint to talk to.

**Custom.** Policy lives in Go modules compiled into that binary: what the agent is
allowed to run, when it is allowed to stop, what it remembers. Adding behaviour means
adding a file and a line to a list, not learning an extension format that someone
designed for a general case I do not have.

**Multiplatform,** in two senses. It builds for macOS, Linux and Windows, and the tests
run on all three in CI. And a session is not tied to whatever is looking at it: a
daemon owns the sessions and clients attach to it, so the terminal is not the only way
in.

## What it actually does

**It refuses to claim it is done when it isn't.** When the agent thinks it has
finished, a stop gate asks whether tasks are still open, whether the project's test
command passes, whether this session left uncommitted changes, and whether the stated
goal was met. Any objection sends it back to work. The model's account of its own
success is not treated as evidence.

**It remembers between sessions.** After a session ends, a curator asks whether
anything in it is worth keeping and writes what it finds as markdown under
`~/.nabu/memory`. The next session on that repository starts already knowing. That
directory is a git repository, so you can read, diff and revert whatever it chose to
remember.

**The run outlives the window.** Quit the terminal UI and the agent keeps working.
Reattach and the transcript replays from where you left off. Two clients can watch the
same session at once.

**It only interrupts for things that matter.** A guard judges a command by what it
would do rather than what it is called. `rm -rf build` runs. `rm -rf /etc` asks.

> **Status:** early, and built for one person's daily use. It drives a local model
> through real work every day. Interfaces still move, there is no release or installer,
> and the Android client speaks the protocol but its screens are unfinished.

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
