package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/corporealshift/nabu/clients/goclient"
	"github.com/corporealshift/nabu/daemon"
	"github.com/corporealshift/nabu/daemon/config"
	"github.com/corporealshift/nabu/protocol"
)

// flusher is anything that can push buffered output out immediately. Spec 13
// requires flushed output per event, so silence means dead rather than
// buffered.
type flusher interface{ Flush() error }

// emit writes one JSON line and flushes it.
func emit(w io.Writer, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "%s\n", raw)
	if f, ok := w.(flusher); ok {
		_ = f.Flush()
	}
	if f, ok := w.(*os.File); ok {
		_ = f.Sync()
	}
}

// rootFlag adds --root and resolves it, falling back to the default.
func rootFlag(fs *flag.FlagSet) *string {
	return fs.String("root", "", "nabu root (default ~/.nabu, or $NABU_ROOT)")
}

func resolveRoot(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	return daemon.DefaultRoot()
}

// connect returns a client for the daemon under root, starting one detached if
// none is listening. Claude Code's delegation must never fail with "daemon not
// running" (spec 13).
func connect(ctx context.Context, root string, stderr io.Writer) (*goclient.Client, error) {
	addr, ok := daemon.RunningAddr(root)
	if !ok {
		fmt.Fprintln(stderr, "nabu: no daemon listening, starting one")
		if err := startDetached(root); err != nil {
			return nil, fmt.Errorf("starting a daemon: %w", err)
		}
		var err error
		addr, err = daemon.WaitForDaemon(ctx, root, 20*time.Second)
		if err != nil {
			return nil, err
		}
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, goclient.DialTimeout)
	defer cancel()
	return goclient.Dial(dialCtx, addr, cfg.Daemon.Token, "nabu-cli", version)
}

// startDetached spawns "nabu daemon" as a process that outlives this one.
func startDetached(root string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "daemon", "--root", root)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	detach(cmd)
	return cmd.Start()
}

// cmdDaemon runs the daemon in the foreground, or stops a running one.
func cmdDaemon(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "stop" {
		return cmdDaemonStop(args[1:], stdout, stderr)
	}
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := rootFlag(fs)
	bind := fs.String("bind", "", "listen address (default from config)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	dir, err := resolveRoot(*root)
	if err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}

	d, err := daemon.New(daemon.Options{Root: dir, Bind: *bind})
	if err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	if err := d.Listen(); err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	fmt.Fprintf(stdout, "nabu daemon listening on %s\n", d.Addr())
	if err := d.Serve(); err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	return exitOK
}

func cmdDaemonStop(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("daemon stop", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := rootFlag(fs)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	dir, err := resolveRoot(*root)
	if err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	if _, ok := daemon.RunningAddr(dir); !ok {
		fmt.Fprintln(stdout, "no daemon is running")
		return exitOK
	}
	ctx := context.Background()
	c, err := connect(ctx, dir, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	defer c.Close()
	if _, err := c.Call(ctx, "nabu.daemon.stop", nil, nil); err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	fmt.Fprintln(stdout, "daemon stopped")
	return exitOK
}

// cmdRun starts a headless run and follows it to completion. Its exit code
// mirrors the final session state.
func cmdRun(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := rootFlag(fs)
	asJSON := fs.Bool("json", false, "emit one JSON object per line")
	doneWhen := fs.String("done-when", "", "goal condition, judged in a fresh context")
	maxTurns := fs.Int("max-turns", config.RunDefaultMaxTurns, "turn budget; 0 is unlimited")
	workspace := fs.String("workspace", "", "workspace path (default: the current directory)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		fmt.Fprintln(stderr, "nabu run: a prompt is required")
		return exitUsage
	}
	dir, err := resolveRoot(*root)
	if err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	ws := *workspace
	if ws == "" {
		if ws, err = os.Getwd(); err != nil {
			fmt.Fprintf(stderr, "nabu: %v\n", err)
			return exitError
		}
	}

	ctx := context.Background()
	c, err := connect(ctx, dir, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	defer c.Close()

	var created struct {
		SessionID string `json:"session_id"`
	}
	create := map[string]any{"workspace": ws}
	if *maxTurns > 0 {
		// Spec 12: the budget is set at creation. A fresh session is idle
		// rather than paused, so resume cannot carry it.
		create["budget"] = map[string]any{"max_turns": *maxTurns, "source": "client"}
	}
	if err := callInto(ctx, c, "nabu.session.create", create, &created); err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	fmt.Fprintf(stderr, "nabu: session %s\n", created.SessionID)

	if *doneWhen != "" {
		if err := callInto(ctx, c, "nabu.session.set_goal",
			map[string]any{"session_id": created.SessionID, "condition": *doneWhen}, nil); err != nil {
			fmt.Fprintf(stderr, "nabu: %v\n", err)
			return exitError
		}
	}
	if _, err := c.Call(ctx, "nabu.session.subscribe",
		map[string]any{"session_id": created.SessionID}, nil); err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	if err := callInto(ctx, c, "nabu.session.send_prompt",
		map[string]any{"session_id": created.SessionID, "content": prompt}, nil); err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}

	state := follow(ctx, c, stdout, *asJSON, true)
	if state == protocol.StateIdle {
		// The turn is over and the session is waiting for a prompt that will
		// never come, because this run is the only caller. Ending it here is
		// also what emits the run report, which is how the caller verifies the
		// outcome without trusting the model's narration.
		if _, err := c.Call(ctx, "nabu.session.stop",
			map[string]any{"session_id": created.SessionID}, nil); err != nil {
			fmt.Fprintf(stderr, "nabu: %v\n", err)
			return exitError
		}
		state = follow(ctx, c, stdout, *asJSON, false)
	}
	if state == protocol.StatePaused {
		fmt.Fprintf(stderr, "nabu: paused. Resume with: nabu resume %s\n", created.SessionID)
	}
	return exitCode(state)
}

// follow streams a session's events until it reaches a terminal state, and
// reports that state.
//
// stopOnIdle also returns on idle. The agent takes a session to idle when the
// stop gates allow, which is right for an interactive session waiting on the
// next prompt but is not a state a headless run can wait out: nothing else
// will ever move it. Only the owner of a run sets this.
func follow(ctx context.Context, c *goclient.Client, stdout io.Writer, asJSON, stopOnIdle bool) protocol.SessionState {
	final := protocol.StateIdle
	_ = c.Stream(ctx, func(m goclient.Message) bool {
		switch m.Method {
		case "nabu.session.event":
			var p struct {
				Event protocol.Event `json:"event"`
			}
			if err := json.Unmarshal(m.Params, &p); err != nil {
				return true
			}
			if asJSON {
				emit(stdout, p.Event)
			} else {
				renderEvent(stdout, p.Event)
			}
			if p.Event.Type == protocol.EventStateChange {
				var sc protocol.StateChangeData
				if err := json.Unmarshal(p.Event.Data, &sc); err == nil {
					if terminal(sc.To) || (stopOnIdle && sc.To == protocol.StateIdle) {
						final = sc.To
						return false
					}
				}
			}
		case "nabu.session.delta":
			if !asJSON {
				var p struct {
					Text string `json:"text"`
				}
				if err := json.Unmarshal(m.Params, &p); err == nil {
					fmt.Fprint(stdout, p.Text)
				}
			}
		}
		return true
	})
	return final
}

// terminal reports whether a state ends the run.
func terminal(s protocol.SessionState) bool {
	switch s {
	case protocol.StateCompleted, protocol.StateBlocked,
		protocol.StatePaused, protocol.StateError:
		return true
	}
	return false
}

// exitCode maps a final session state to the process exit code.
func exitCode(s protocol.SessionState) int {
	switch s {
	case protocol.StateCompleted:
		return exitOK
	case protocol.StateBlocked:
		return exitBlocked
	case protocol.StatePaused:
		return exitPaused
	case protocol.StateError:
		return exitError
	default:
		return exitOK
	}
}

// renderEvent prints a human-readable line for an event.
func renderEvent(w io.Writer, ev protocol.Event) {
	fmt.Fprintf(w, "[%s] %s\n", ev.Type, strings.TrimSpace(summarize(ev)))
}

func summarize(ev protocol.Event) string {
	switch ev.Type {
	case protocol.EventMessage:
		var d protocol.MessageData
		if json.Unmarshal(ev.Data, &d) == nil {
			return d.Role + ": " + d.Content
		}
	case protocol.EventToolCall:
		var d protocol.ToolCallData
		if json.Unmarshal(ev.Data, &d) == nil {
			return d.Tool
		}
	case protocol.EventStateChange:
		var d protocol.StateChangeData
		if json.Unmarshal(ev.Data, &d) == nil {
			from := ""
			if d.From != nil {
				from = string(*d.From)
			}
			return from + " -> " + string(d.To) + " " + d.Reason
		}
	case protocol.EventReport:
		var d protocol.ReportData
		if json.Unmarshal(ev.Data, &d) == nil {
			return summarizeReport(d)
		}
	}
	return ""
}

// summarizeReport renders the run report on one line. The report exists so a
// caller can verify an outcome without trusting the model's narration, so the
// parts that contradict a confident summary come first: an unmet goal, a
// failed check, a dirty tree.
func summarizeReport(d protocol.ReportData) string {
	parts := []string{string(d.ExitStatus)}
	if d.Goal != nil {
		parts = append(parts, "goal "+d.Goal.State)
	}
	parts = append(parts, fmt.Sprintf("tasks %d/%d", d.Tasks.Done, d.Tasks.Total))

	for _, c := range d.Checks {
		parts = append(parts, c.Name+" "+c.Status)
	}
	if n := len(d.FilesTouched); n > 0 {
		parts = append(parts, fmt.Sprintf("%d files", n))
	}
	if n := len(d.Commits); n > 0 {
		parts = append(parts, fmt.Sprintf("%d commits", n))
	}
	if d.TreeDirty != nil && *d.TreeDirty {
		parts = append(parts, "tree dirty")
	}
	return strings.Join(parts, " · ")
}

// cmdStatus prints one session's state, or lists sessions when given no id.
func cmdStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := rootFlag(fs)
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	dir, err := resolveRoot(*root)
	if err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	ctx := context.Background()
	c, err := connect(ctx, dir, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	defer c.Close()

	if id := fs.Arg(0); id != "" {
		var st protocol.State
		if err := callInto(ctx, c, "nabu.session.state",
			map[string]any{"session_id": id}, &st); err != nil {
			fmt.Fprintf(stderr, "nabu: %v\n", err)
			return exitError
		}
		if *asJSON {
			emit(stdout, st)
		} else {
			fmt.Fprintf(stdout, "%s  turns=%d  tasks=%d\n", st.State, st.Turns, len(st.Tasks))
		}
		return exitCode(st.State)
	}

	var out struct {
		Sessions []struct {
			SessionID string `json:"session_id"`
			State     string `json:"state"`
			Workspace string `json:"workspace"`
		} `json:"sessions"`
	}
	if err := callInto(ctx, c, "nabu.session.list", nil, &out); err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	if *asJSON {
		for _, s := range out.Sessions {
			emit(stdout, s)
		}
		return exitOK
	}
	if len(out.Sessions) == 0 {
		fmt.Fprintln(stdout, "no sessions")
		return exitOK
	}
	for _, s := range out.Sessions {
		fmt.Fprintf(stdout, "%s  %-9s  %s\n", s.SessionID, s.State, s.Workspace)
	}
	return exitOK
}

// cmdAttach streams a session's events until it ends.
func cmdAttach(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := rootFlag(fs)
	asJSON := fs.Bool("json", false, "emit one JSON object per line")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	id := fs.Arg(0)
	if id == "" {
		fmt.Fprintln(stderr, "nabu attach: a session id is required")
		return exitUsage
	}
	dir, err := resolveRoot(*root)
	if err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	ctx := context.Background()
	c, err := connect(ctx, dir, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	defer c.Close()

	if _, err := c.Call(ctx, "nabu.session.subscribe",
		map[string]any{"session_id": id}, nil); err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	// An attached client is an observer, not the owner of the run: an idle
	// session may yet receive another prompt, so it keeps watching.
	return exitCode(follow(ctx, c, stdout, *asJSON, false))
}

func cmdStop(args []string, stdout, stderr io.Writer) int {
	return simpleSessionCommand("stop", "nabu.session.stop", args, stdout, stderr,
		func(w io.Writer, id string) { fmt.Fprintf(w, "stopped %s\n", id) })
}

func cmdResume(args []string, stdout, stderr io.Writer) int {
	return simpleSessionCommand("resume", "nabu.session.resume", args, stdout, stderr,
		func(w io.Writer, id string) { fmt.Fprintf(w, "resumed %s\n", id) })
}

// simpleSessionCommand runs a method that takes a session id and returns
// nothing interesting.
func simpleSessionCommand(name, method string, args []string, stdout, stderr io.Writer, done func(io.Writer, string)) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := rootFlag(fs)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	id := fs.Arg(0)
	if id == "" {
		fmt.Fprintf(stderr, "nabu %s: a session id is required\n", name)
		return exitUsage
	}
	dir, err := resolveRoot(*root)
	if err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	ctx := context.Background()
	c, err := connect(ctx, dir, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	defer c.Close()

	if _, err := c.Call(ctx, method, map[string]any{"session_id": id}, nil); err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	done(stdout, id)
	return exitOK
}

// callInto makes a call and decodes its result into out, which may be nil.
func callInto(ctx context.Context, c *goclient.Client, method string, params, out any) error {
	raw, err := c.Call(ctx, method, params, nil)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}
