package daemon

import (
	"context"
	"errors"
	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/modules/memory"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// newDaemon builds a daemon over a temp root bound to a random loopback port,
// so tests never touch a real ~/.nabu and never collide with a live daemon.
func newDaemon(t *testing.T) *Daemon {
	t.Helper()
	d, err := New(Options{Root: t.TempDir(), Bind: "127.0.0.1:0", LogWriter: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestEnsureLayoutCreatesTheTree(t *testing.T) {
	root := t.TempDir()
	if err := EnsureLayout(root); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{SessionsDir, MemoryDir} {
		info, err := os.Stat(filepath.Join(root, dir))
		if err != nil {
			t.Errorf("%s: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
	}
}

func TestDefaultRootHonoursEnvOverride(t *testing.T) {
	t.Setenv("NABU_ROOT", filepath.Join("some", "where"))
	got, err := DefaultRoot()
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join("some", "where") {
		t.Errorf("root: got %q", got)
	}
}

func TestListenWritesPidAndPortThenCleansUp(t *testing.T) {
	d := newDaemon(t)
	if err := d.Listen(); err != nil {
		t.Fatal(err)
	}

	pidPath := filepath.Join(d.Root(), PIDFile)
	portPath := filepath.Join(d.Root(), PortFile)

	pid, ok := readPID(pidPath)
	if !ok {
		t.Fatal("pid file missing or unreadable")
	}
	if pid != os.Getpid() {
		t.Errorf("pid: got %d, want %d", pid, os.Getpid())
	}

	portBytes, err := os.ReadFile(portPath)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(string(portBytes))
	if err != nil || port <= 0 {
		t.Fatalf("port file holds %q", portBytes)
	}
	if d.Addr() == "" {
		t.Error("Addr should report the bound address")
	}

	if err := d.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Error("pid file should be removed on shutdown")
	}
	if _, err := os.Stat(portPath); !os.IsNotExist(err) {
		t.Error("port file should be removed on shutdown")
	}
}

func TestSecondDaemonRefusesWhileOneIsLive(t *testing.T) {
	first := newDaemon(t)
	if err := first.Listen(); err != nil {
		t.Fatal(err)
	}
	defer first.Shutdown(context.Background())

	second, err := New(Options{Root: first.Root(), Bind: "127.0.0.1:0", LogWriter: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	err = second.Listen()
	if err == nil {
		t.Fatal("a second daemon must refuse to start on the same root")
	}
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("error: got %v, want %v", err, ErrAlreadyRunning)
	}
}

// A pid file left by a crashed daemon must not block a new one.
func TestStalePidFileIsTakenOver(t *testing.T) {
	d := newDaemon(t)
	// Pid 0 is never a live process, so this file is stale by construction.
	stale := filepath.Join(d.Root(), PIDFile)
	if err := os.WriteFile(stale, []byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.Listen(); err != nil {
		t.Fatalf("a stale pid file must be taken over, got %v", err)
	}
	defer d.Shutdown(context.Background())

	pid, ok := readPID(stale)
	if !ok || pid != os.Getpid() {
		t.Errorf("pid file should now hold this process, got %d", pid)
	}
}

func TestGarbagePidFileIsTakenOver(t *testing.T) {
	d := newDaemon(t)
	if err := os.WriteFile(filepath.Join(d.Root(), PIDFile), []byte("not a pid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.Listen(); err != nil {
		t.Fatalf("an unreadable pid file must be taken over, got %v", err)
	}
	_ = d.Shutdown(context.Background())
}

func TestProcessAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Error("this process should report as alive")
	}
	if processAlive(0) {
		t.Error("pid 0 should never report as alive")
	}
	if processAlive(-1) {
		t.Error("a negative pid should never report as alive")
	}
}

func TestRunningAddrFindsALiveDaemon(t *testing.T) {
	d := newDaemon(t)
	if _, ok := RunningAddr(d.Root()); ok {
		t.Fatal("no daemon is listening yet")
	}
	if err := d.Listen(); err != nil {
		t.Fatal(err)
	}
	addr, ok := RunningAddr(d.Root())
	if !ok {
		t.Fatal("a listening daemon should be discoverable")
	}
	_, want, err := net.SplitHostPort(d.Addr())
	if err != nil {
		t.Fatal(err)
	}
	if _, got, err := net.SplitHostPort(addr); err != nil || got != want {
		t.Errorf("addr %q should carry port %q", addr, want)
	}

	if err := d.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := RunningAddr(d.Root()); ok {
		t.Error("a stopped daemon should not be discoverable")
	}
}

func TestWaitForDaemonTimesOut(t *testing.T) {
	root := t.TempDir()
	start := time.Now()
	_, err := WaitForDaemon(context.Background(), root, 60*time.Millisecond)
	if err == nil {
		t.Fatal("waiting for a daemon that never starts must fail")
	}
	if time.Since(start) > 5*time.Second {
		t.Error("the deadline was not honoured")
	}
}

func TestWaitForDaemonFindsOne(t *testing.T) {
	d := newDaemon(t)
	if err := d.Listen(); err != nil {
		t.Fatal(err)
	}
	defer d.Shutdown(context.Background())

	addr, err := WaitForDaemon(context.Background(), d.Root(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if addr == "" {
		t.Error("expected an address")
	}
}

func TestServeAndShutdown(t *testing.T) {
	d := newDaemon(t)
	if err := d.Listen(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- d.Serve() }()

	if _, err := WaitForDaemon(context.Background(), d.Root(), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := d.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v, want nil after a clean shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after Shutdown")
	}
}

// session.Open appends "sessions" itself. Passing it the sessions directory
// produced ~/.nabu/sessions/sessions, which no other component looks in.
func TestSessionsLiveDirectlyUnderTheRoot(t *testing.T) {
	d := newDaemon(t)
	if err := d.Listen(); err != nil {
		t.Fatal(err)
	}
	defer d.Shutdown(context.Background())

	nested := filepath.Join(d.Root(), SessionsDir, SessionsDir)
	if _, err := os.Stat(nested); err == nil {
		t.Fatalf("sessions must not nest: %s exists", nested)
	}
	if _, err := os.Stat(filepath.Join(d.Root(), SessionsDir)); err != nil {
		t.Fatalf("the sessions directory should exist under the root: %v", err)
	}
}

// Memory lands at <root>/memory, per spec 11.2. This is the join between the
// host's data directory and the module's own layout, which neither package's
// tests can see on its own.
func TestMemoryLivesAtTheNabuRoot(t *testing.T) {
	root := t.TempDir()
	mem := &memory.Module{}
	d, err := New(Options{
		Root: root, Bind: "127.0.0.1:0", LogWriter: io.Discard,
		Modules: []module.Module{mem},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	want := filepath.Join(root, "memory", "global")
	if got := mem.GlobalStore().Dir(); got != want {
		t.Errorf("global memory at %q, want %q", got, want)
	}
	if strings.Contains(mem.GlobalStore().Dir(), "modules") {
		t.Error("memory is still buried under a modules directory")
	}
}

// configSpy records the config section it was initialised with.
type configSpy struct{ got module.Config }

func (c *configSpy) Name() string { return "spy" }
func (c *configSpy) Init(_ module.Host, cfg module.Config) error {
	c.got = cfg
	return nil
}

// The modules section of config.json has to reach the modules. Without this
// every per-module setting is accepted by the config loader and then silently
// ignored, which is worse than rejecting it.
func TestModuleConfigSectionsReachTheirModule(t *testing.T) {
	root := t.TempDir()
	cfg := `{"modules":{"spy":{"enabled":true,"import_dirs":["/one","/two"],"depth":3}}}`
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	spy := &configSpy{}
	d, err := New(Options{
		Root: root, Bind: "127.0.0.1:0", LogWriter: io.Discard,
		Modules: []module.Module{spy},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	if len(spy.got) == 0 {
		t.Fatal("the module was initialised with no config at all")
	}
	if dirs := spy.got.Strings("import_dirs", nil); len(dirs) != 2 || dirs[0] != "/one" {
		t.Errorf("import_dirs = %v, want the two configured", dirs)
	}
	if n := spy.got.Int("depth", 0); n != 3 {
		t.Errorf("depth = %d, want 3", n)
	}
	if !spy.got.Enabled() {
		t.Error("enabled was not delivered")
	}
}

// The configured context window has to reach the provider registry. It never
// did, so provider.Config.ContextWindow was always zero, which is the value
// that disables size-based compaction. Long sessions therefore grew until the
// model refused them, and no compaction ever ran in a real daemon.
func TestTheConfiguredContextWindowReachesTheProvider(t *testing.T) {
	root := t.TempDir()
	cfg := `{"providers":{"local":{"base_url":"http://localhost:8033/v1","context_window":256000}}}`
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := New(Options{Root: root, Bind: "127.0.0.1:0", LogWriter: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	_, _, pcfg, err := d.providers.Resolve("local/anything")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if pcfg.ContextWindow != 256000 {
		t.Errorf("ContextWindow = %d, want 256000: compaction cannot trigger at zero",
			pcfg.ContextWindow)
	}
}
