package main

import (
	"fmt"
	"os"
)

const usage = `nabu — coding-agent harness

usage: nabu <command>

  daemon    run the daemon in the foreground
  run       start a headless run in the current workspace
  status    show session state
  attach    stream a session's events
  stop      stop a session
  resume    resume a paused session

Not implemented yet: this is the P0 scaffold. See ARCHITECTURE.md.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	fmt.Fprintf(os.Stderr, "nabu: %q is not implemented yet (P0 scaffold)\n", os.Args[1])
	os.Exit(2)
}
