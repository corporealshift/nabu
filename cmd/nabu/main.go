// Command nabu is the single binary: "nabu daemon" runs the daemon in the
// foreground; every other subcommand connects to a running daemon and starts
// one detached if none is listening.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// version is reported to the daemon during the handshake.
const version = "0.0.0"

// Exit codes mirror the session state after a run, so a caller can tell what
// happened without parsing output (spec 13).
const (
	exitOK       = 0 // completed
	exitBlocked  = 1 // blocked: a gate refused, or a denial left no way forward
	exitPaused   = 2 // paused: the budget ran out; the resume command is printed
	exitError    = 3 // error: the run failed
	exitUsage    = 64
	exitInternal = 70
)

const usage = `nabu — coding-agent harness

usage: nabu [flags]              open the interactive UI on a new session
       nabu <command> [flags]

  daemon            run the daemon in the foreground
  daemon stop       stop a running daemon
  run <prompt>      start a headless run in the current workspace
  status [id]       show session state, or list sessions
  attach <id>       stream a session's events
  stop <id>         stop a session
  resume <id>       resume a paused session
  notes             print the working notes kept for this workspace

A daemon is started automatically if none is listening.

Interactive flags:
  --session ID      attach to an existing session instead of starting one
  --workspace DIR   workspace for the new session (default: current directory)

Common flags:
  --json            emit one JSON object per line, flushed per event
  --done-when TEXT  set the run goal (run only)
  --max-turns N     turn budget (run only; default 60)
  --root DIR        nabu root (default ~/.nabu, or $NABU_ROOT)

Keys in the UI: i type · s sessions · y/n answer a prompt · ctrl+x interrupt · q quit

Exit codes: 0 completed, 1 blocked, 2 paused, 3 error.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the whole CLI behind an injectable writer and argument list, so every
// command and every exit code is testable without spawning a binary.
func run(args []string, stdout, stderr io.Writer) int {
	// No command at all, or only flags, means the interactive UI. That is the
	// daily driver, so it is what the bare name does.
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
			fmt.Fprint(stdout, usage)
			return exitOK
		}
		return cmdTUI(args, stdout, stderr)
	}

	switch args[0] {
	case "help":
		fmt.Fprint(stdout, usage)
		return exitOK
	case "daemon":
		return cmdDaemon(args[1:], stdout, stderr)
	case "run":
		return cmdRun(args[1:], stdout, stderr)
	case "status":
		return cmdStatus(args[1:], stdout, stderr)
	case "attach":
		return cmdAttach(args[1:], stdout, stderr)
	case "stop":
		return cmdStop(args[1:], stdout, stderr)
	case "resume":
		return cmdResume(args[1:], stdout, stderr)
	case "notes":
		return cmdNotes(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "nabu: unknown command %q\n\n%s", args[0], usage)
		return exitUsage
	}
}
