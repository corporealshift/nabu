package tui

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/corporealshift/nabu/clients/goclient"
	"github.com/corporealshift/nabu/daemon"
)

// recorder stands in for the program and keeps everything sent to it.
type recorder struct {
	mu   sync.Mutex
	msgs []tea.Msg
	got  chan struct{}
}

func newRecorder() *recorder { return &recorder{got: make(chan struct{}, 1)} }

func (r *recorder) Send(m tea.Msg) {
	r.mu.Lock()
	r.msgs = append(r.msgs, m)
	r.mu.Unlock()
	select {
	case r.got <- struct{}{}:
	default:
	}
}

// mark is a place in what has been sent, so a wait can ignore what came before.
func (r *recorder) mark() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.msgs)
}

// waitFor blocks until something sent after seen matches, or fails the test.
func (r *recorder) waitFor(t *testing.T, seen int, what string, match func(tea.Msg) bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		r.mu.Lock()
		msgs := r.msgs[seen:]
		seen = len(r.msgs)
		r.mu.Unlock()
		for _, m := range msgs {
			if match(m) {
				return
			}
		}
		select {
		case <-r.got:
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// startDaemon runs a real daemon over a temp root and returns its address.
func startDaemon(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	d, err := daemon.New(daemon.Options{Root: root, Bind: "127.0.0.1:0", LogWriter: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Listen(); err != nil {
		t.Fatal(err)
	}
	go d.Serve()
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })
	addr, err := daemon.WaitForDaemon(context.Background(), root, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return addr
}

func createSession(t *testing.T, addr string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := goclient.Dial(ctx, addr, "", "test", "0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var out struct {
		SessionID string `json:"session_id"`
	}
	if err := c.CallInto(ctx, "nabu.session.create", map[string]any{"workspace": t.TempDir()}, &out); err != nil {
		t.Fatal(err)
	}
	return out.SessionID
}

func isConnected(m tea.Msg) bool {
	c, ok := m.(connMsg)
	return ok && c.state == connected
}

// Switching away from a session with nothing happening in it has to happen at
// once. The switch used to be noticed only when the old session next sent
// something, so leaving an idle one sat on "attaching to" forever (issue 102).
func TestAttachingLeavesAnIdleSession(t *testing.T) {
	addr := startDaemon(t)
	first, second := createSession(t, addr), createSession(t, addr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := newRecorder()
	actions := make(chan action, 1)
	done := make(chan struct{})
	go func() {
		connectLoop(ctx, rec, addr, "", first, actions)
		close(done)
	}()
	defer func() { cancel(); <-done }()

	rec.waitFor(t, 0, "the first session to connect", isConnected)

	from := rec.mark()
	actions <- action{kind: actAttach, sessionID: second}
	rec.waitFor(t, from, "the second session to connect", isConnected)
}
