package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tui "github.com/corporealshift/nabu/clients/go-tui"
	"github.com/corporealshift/nabu/clients/goclient"
	"github.com/corporealshift/nabu/daemon"
	"github.com/corporealshift/nabu/daemon/config"
	"github.com/corporealshift/nabu/daemon/modules/notes"
	workspacepkg "github.com/corporealshift/nabu/daemon/workspace"
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
	if _, ok := daemon.RunningAddr(root); !ok {
		fmt.Fprintln(stderr, "nabu: no daemon listening, starting one")
	}
	addr, err := daemon.EnsureRunning(ctx, root)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, goclient.DialTimeout)
	defer cancel()
	return goclient.Dial(dialCtx, addr, cfg.Daemon.Token, "nabu-cli", version)
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
	if _, err := c.Call(ctx, "nabu.daemon.stop", nil); err != nil {
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
		map[string]any{"session_id": created.SessionID}); err != nil {
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
		// A run owns its session, so it ends it. That is also what emits the
		// report.
		if _, err := c.Call(ctx, "nabu.session.stop",
			map[string]any{"session_id": created.SessionID}); err != nil {
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
// reports that state. stopOnIdle also returns on idle, which a headless run
// needs because nothing will move a session out of it on its own.
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

// summarizeReport renders the run report on one line, leading with whatever
// contradicts a confident summary: an unmet goal, a failed check, a dirty tree.
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
		map[string]any{"session_id": id}); err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	// An observer is not the owner: an idle session may yet get another prompt.
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

func cmdArchive(args []string, stdout, stderr io.Writer) int {
	return simpleSessionCommand("archive", "nabu.session.archive", args, stdout, stderr,
		func(w io.Writer, id string) { fmt.Fprintf(w, "archived %s\n", id) })
}

func cmdRestore(args []string, stdout, stderr io.Writer) int {
	return simpleSessionCommand("restore", "nabu.session.restore", args, stdout, stderr,
		func(w io.Writer, id string) { fmt.Fprintf(w, "restored %s\n", id) })
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

	if _, err := c.Call(ctx, method, map[string]any{"session_id": id}); err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	done(stdout, id)
	return exitOK
}

// callInto makes a call and decodes its result into out, which may be nil.
func callInto(ctx context.Context, c *goclient.Client, method string, params, out any) error {
	raw, err := c.Call(ctx, method, params)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// launchTUI is a variable so the default command is testable without a
// terminal.
var launchTUI = tui.Run

// cmdTUI is the default command: the interactive UI on a new session in the
// current directory. Opening the UI is one step, because needing a second
// command to produce a session first is the thing it replaces.
func cmdTUI(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nabu", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := rootFlag(fs)
	workspace := fs.String("workspace", "", "workspace path (default: the current directory)")
	sessionID := fs.String("session", "", "attach to this session instead of starting one")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	dir, err := resolveRoot(*root)
	if err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}

	ctx := context.Background()
	id := *sessionID
	if id == "" {
		if id, err = createSession(ctx, dir, *workspace, stderr); err != nil {
			fmt.Fprintf(stderr, "nabu: %v\n", err)
			return exitError
		}
	}
	if err := launchTUI(ctx, tui.Options{Root: dir, SessionID: id}); err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	return exitOK
}

// createSession starts a session in the workspace, defaulting to the current
// directory.
func createSession(ctx context.Context, root, workspace string, stderr io.Writer) (string, error) {
	if workspace == "" {
		var err error
		if workspace, err = os.Getwd(); err != nil {
			return "", err
		}
	}
	c, err := connect(ctx, root, stderr)
	if err != nil {
		return "", err
	}
	defer c.Close()

	var created struct {
		SessionID string `json:"session_id"`
	}
	if err := callInto(ctx, c, "nabu.session.create",
		map[string]any{"workspace": workspace}, &created); err != nil {
		return "", err
	}
	return created.SessionID, nil
}

// cmdNotes prints the working notes the agent has kept. It reads the files
// directly rather than asking the daemon: notes are plain markdown on disk, and
// wanting to read them is not a reason to need a daemon running.
func cmdNotes(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("notes", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := rootFlag(fs)
	all := fs.Bool("all", false, "every workspace, not just this one")
	workspace := fs.String("workspace", "", "the workspace to read (default: the current directory)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	dir, err := resolveRoot(*root)
	if err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	notesRoot := filepath.Join(dir, "notes")

	if *all {
		byKey := notes.AllWorkspaces(notesRoot)
		if len(byKey) == 0 {
			fmt.Fprintln(stdout, "no notes")
			return exitOK
		}
		keys := make([]string, 0, len(byKey))
		for k := range byKey {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(stdout, "== %s\n", k)
			printNotes(stdout, byKey[k])
		}
		return exitOK
	}

	path := *workspace
	if path == "" {
		if path, err = os.Getwd(); err != nil {
			fmt.Fprintf(stderr, "nabu: %v\n", err)
			return exitError
		}
	}
	ws, err := workspacepkg.Resolve(path)
	if err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	live := notes.ForWorkspace(notesRoot, ws.Key)
	if len(live) == 0 {
		fmt.Fprintf(stdout, "no notes for %s\n", ws.Key)
		return exitOK
	}
	fmt.Fprintf(stdout, "== %s\n", ws.Key)
	printNotes(stdout, live)
	return exitOK
}

func printNotes(w io.Writer, live []notes.Note) {
	now := time.Now()
	for _, n := range live {
		fmt.Fprintf(w, "\n-- %s  (written %s, %s)\n", n.Name,
			n.Written.Local().Format("2006-01-02 15:04"), humanAge(now.Sub(n.Written)))
		fmt.Fprintf(w, "%s\n", n.Body)
	}
}

// humanAge matches the module's wording, so the CLI and the model describe the
// same note the same way.
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
