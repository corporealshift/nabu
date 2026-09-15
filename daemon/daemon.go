// Package daemon owns the nabu daemon's lifecycle: the storage layout under
// the nabu root, the pid and port files, the foreground run, and the graceful
// shutdown that pauses live sessions rather than losing them.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/corporealshift/nabu/daemon/agent"
	"github.com/corporealshift/nabu/daemon/api"
	"github.com/corporealshift/nabu/daemon/config"
	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/modules"
	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/daemon/session"
	"github.com/corporealshift/nabu/daemon/tools"
)

// File and directory names under the nabu root.
const (
	ConfigFile  = "config.json"
	SessionsDir = "sessions"
	MemoryDir   = "memory"
	LogFile     = "daemon.log"
	PIDFile     = "daemon.pid"
	PortFile    = "daemon.port"
)

// ErrAlreadyRunning is returned when a live daemon already holds the pid file.
var ErrAlreadyRunning = errors.New("a daemon is already running")

// DefaultRoot is the nabu root, ~/.nabu, unless NABU_ROOT overrides it.
func DefaultRoot() (string, error) {
	if r := os.Getenv("NABU_ROOT"); r != "" {
		return r, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("daemon: locating home: %w", err)
	}
	return filepath.Join(home, ".nabu"), nil
}

// EnsureLayout creates the directories the daemon expects under root.
func EnsureLayout(root string) error {
	for _, dir := range []string{root,
		filepath.Join(root, SessionsDir),
		filepath.Join(root, MemoryDir),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("daemon: creating %s: %w", dir, err)
		}
	}
	return nil
}

// Options configure a Daemon.
type Options struct {
	// Root is the nabu root. Empty means DefaultRoot.
	Root string
	// Bind overrides the configured listen address. Empty uses config.
	Bind string
	// LogWriter receives structured logs. nil writes to daemon.log under Root.
	LogWriter io.Writer
	// Modules are registered alongside the built-in tools.
	Modules []module.Module
}

// Daemon owns the whole stack: config, store, provider registry, module
// registry, agent manager and the WebSocket API.
type Daemon struct {
	root  string
	cfg   *config.Config
	log   *slog.Logger
	store *session.Store
	mgr   *agent.Manager
	api   *api.Server

	listener net.Listener
	logFile  *os.File

	// shutdownDone closes when Shutdown has finished its work. Serve waits on
	// it, because a shutdown triggered over the wire runs in its own goroutine
	// and os.Exit would otherwise kill it mid-cleanup.
	shutdownOnce sync.Once
	shutdownDone chan struct{}
}

// New builds a Daemon over root without binding anything yet.
func New(opts Options) (*Daemon, error) {
	root := opts.Root
	if root == "" {
		r, err := DefaultRoot()
		if err != nil {
			return nil, err
		}
		root = r
	}
	if err := EnsureLayout(root); err != nil {
		return nil, err
	}

	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	if opts.Bind != "" {
		cfg.Daemon.Bind = opts.Bind
	}

	w := opts.LogWriter
	var logFile *os.File
	if w == nil {
		f, err := os.OpenFile(filepath.Join(root, LogFile),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, fmt.Errorf("daemon: opening log: %w", err)
		}
		logFile, w = f, f
	}
	log := slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: levelOf(cfg.Daemon.LogLevel)}))

	// session.Open appends "sessions" itself, so it takes the root. Passing
	// the sessions directory produced ~/.nabu/sessions/sessions.
	store, err := session.Open(root)
	if err != nil {
		closeFile(logFile)
		return nil, fmt.Errorf("daemon: opening session store: %w", err)
	}

	builtins := &tools.Builtins{}
	// Built-ins first, then the compiled-in modules. Options override the list
	// so tests can run with a known set; nil means "whatever is registered".
	registered := opts.Modules
	if registered == nil {
		registered = modules.All
	}
	mods := append([]module.Module{builtins}, registered...)
	registry := module.NewRegistry(mods, module.Options{Log: log})

	// Every configured provider gets a real OpenAI-compatible client. The
	// first one configured is the default, since the config object has no
	// ordering of its own.
	providers := provider.NewRegistry()
	isFirst := true
	for name, p := range cfg.Providers {
		pc := provider.Config{
			Name:        name,
			BaseURL:     p.BaseURL,
			APIKey:      p.APIKey,
			MaxInFlight: p.MaxInFlight,
		}
		providers.Add(pc, provider.NewOpenAI(pc, nil), isFirst)
		isFirst = false
	}

	// The handler is built first because it is the manager's Asker and delta
	// sink; the manager is bound into it once it exists.
	handler := api.NewHandler(nil, store, log)

	mgr, err := agent.New(agent.Deps{
		Store: store, Providers: providers, Modules: registry,
		Builtins: builtins, Root: root, Log: log,
		Asker:  handler,
		Deltas: handler.Deltas(),
	}, agent.Config{
		DefaultModel:         cfg.Daemon.DefaultModel,
		MaxConsecutiveVetoes: cfg.Budget.MaxConsecutiveVetoes,
		NoProgressTurns:      cfg.Budget.NoProgressTurns,
	})
	if err != nil {
		_ = store.Close()
		closeFile(logFile)
		return nil, fmt.Errorf("daemon: building agent manager: %w", err)
	}
	handler.SetManager(mgr)

	srv := api.NewServer(handler, &api.Config{Bind: cfg.Daemon.Bind, Token: cfg.Daemon.Token}, log)

	d := &Daemon{root: root, cfg: cfg, log: log, store: store,
		mgr: mgr, api: srv, logFile: logFile,
		shutdownDone: make(chan struct{})}

	// The API exposes shutdown; the daemon owns it. nabu.daemon.stop is what
	// "nabu daemon stop" calls, because signalling by pid is not gracefully
	// portable to Windows.
	handler.OnShutdown = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := d.Shutdown(ctx); err != nil {
			log.Error("shutdown", "error", err)
		}
	}

	return d, nil
}

func closeFile(f *os.File) {
	if f != nil {
		_ = f.Close()
	}
}

// levelOf maps a configured level name to a slog level.
func levelOf(name string) slog.Level {
	switch strings.ToLower(name) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Root returns the nabu root this daemon owns.
func (d *Daemon) Root() string { return d.root }

// Addr returns the bound address, or an empty string before Listen.
func (d *Daemon) Addr() string {
	if d.listener == nil {
		return ""
	}
	return d.listener.Addr().String()
}

// Listen claims the pid file, binds the socket, and writes the port file. It
// refuses to start when a live daemon already holds the pid file, and takes
// over a stale one.
func (d *Daemon) Listen() error {
	if err := d.claimPID(); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", d.cfg.Daemon.Bind)
	if err != nil {
		d.releasePID()
		return fmt.Errorf("daemon: binding %s: %w", d.cfg.Daemon.Bind, err)
	}
	d.listener = ln

	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		port = ln.Addr().String()
	}
	if err := os.WriteFile(d.portPath(), []byte(port), 0o644); err != nil {
		_ = ln.Close()
		d.releasePID()
		return fmt.Errorf("daemon: writing port file: %w", err)
	}

	// Paused sessions are listed, never resumed: a client decides.
	paused, err := d.store.RecoverInterrupted()
	if err != nil {
		d.log.Warn("recovering interrupted sessions", "error", err)
	}
	for _, id := range paused {
		d.log.Info("session was interrupted and is paused", "session", id)
	}
	d.log.Info("daemon listening", "addr", ln.Addr().String(), "root", d.root)
	return nil
}

// Paused lists sessions that were interrupted by a previous shutdown.
func (d *Daemon) Paused() ([]string, error) {
	sums, err := d.store.List()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range sums {
		if s.State == "paused" {
			out = append(out, s.SessionID)
		}
	}
	return out, nil
}

// Serve runs until Shutdown is called. Listen must have been called first.
func (d *Daemon) Serve() error {
	if d.listener == nil {
		if err := d.Listen(); err != nil {
			return err
		}
	}
	err := d.api.Serve(d.listener)
	if errors.Is(err, http.ErrServerClosed) {
		// A shutdown is under way. Wait for it to finish cleaning up before
		// returning, or the caller exits and the pid and port files survive.
		<-d.shutdownDone
		return nil
	}
	return err
}

// Shutdown cancels in-flight work, pauses live sessions, and releases the pid
// and port files. Nothing is lost: the log holds every event up to the stop.
func (d *Daemon) Shutdown(ctx context.Context) error {
	defer d.shutdownOnce.Do(func() { close(d.shutdownDone) })
	var errs []error
	if err := d.api.Shutdown(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := d.mgr.Shutdown(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := d.store.Close(); err != nil {
		errs = append(errs, err)
	}
	d.releasePID()
	d.log.Info("daemon stopped")
	closeFile(d.logFile)
	return errors.Join(errs...)
}

func (d *Daemon) pidPath() string  { return filepath.Join(d.root, PIDFile) }
func (d *Daemon) portPath() string { return filepath.Join(d.root, PortFile) }

// claimPID writes this process's pid, refusing if a live daemon holds it.
// A pid file left by a crashed daemon is stale and is taken over.
func (d *Daemon) claimPID() error {
	if pid, ok := readPID(d.pidPath()); ok && processAlive(pid) {
		return fmt.Errorf("%w (pid %d)", ErrAlreadyRunning, pid)
	}
	return os.WriteFile(d.pidPath(), []byte(strconv.Itoa(os.Getpid())), 0o644)
}

func (d *Daemon) releasePID() {
	_ = os.Remove(d.pidPath())
	_ = os.Remove(d.portPath())
}

// readPID reads a pid file, reporting whether it held a usable number.
func readPID(path string) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// RunningAddr reports the address of a daemon already listening under root,
// or false when none is. It is what a CLI subcommand uses to decide whether
// to connect or to start one.
func RunningAddr(root string) (string, bool) {
	pid, ok := readPID(filepath.Join(root, PIDFile))
	if !ok || !processAlive(pid) {
		return "", false
	}
	b, err := os.ReadFile(filepath.Join(root, PortFile))
	if err != nil {
		return "", false
	}
	port := strings.TrimSpace(string(b))
	if port == "" {
		return "", false
	}
	return net.JoinHostPort("127.0.0.1", port), true
}

// WaitForDaemon polls until a daemon under root is listening, or the deadline
// passes. A detached start is not instantaneous, and a CLI command must not
// race it.
func WaitForDaemon(ctx context.Context, root string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		if addr, ok := RunningAddr(root); ok {
			return addr, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("daemon: none listening under %s after %s", root, timeout)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}
