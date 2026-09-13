// Command nabu is the single binary: "nabu daemon" runs the daemon in the
// foreground; every other subcommand connects to a running daemon and starts
// one detached if none is listening.
package main

import (
	"fmt"
	"io"
	"os"
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

usage: nabu <command> [flags]

  daemon            run the daemon in the foreground
  daemon stop       stop a running daemon
  run <prompt>      start a headless run in the current workspace
  status [id]       show session state, or list sessions
  attach <id>       stream a session's events
  stop <id>         stop a session
  resume <id>       resume a paused session

Common flags:
  --json            emit one JSON object per line, flushed per event
  --done-when TEXT  set the run goal (run only)
  --max-turns N     turn budget (run only; default 60)
  --root DIR        nabu root (default ~/.nabu, or $NABU_ROOT)

Exit codes: 0 completed, 1 blocked, 2 paused, 3 error.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the whole CLI behind an injectable writer and argument list, so every
// command and every exit code is testable without spawning a binary.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}

	switch args[0] {
	case "-h", "--help", "help":
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
	default:
		fmt.Fprintf(stderr, "nabu: unknown command %q\n\n%s", args[0], usage)
		return exitUsage
	}
}
