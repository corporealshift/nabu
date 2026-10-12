// Command rho is the single binary: `rho daemon` runs the daemon in the foreground;
// every other subcommand (run, status, attach, stop, resume) connects to a running
// daemon and starts one detached if none is listening.
package main
