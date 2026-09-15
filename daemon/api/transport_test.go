package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/corporealshift/nabu/protocol"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type noopHandler struct{}

func (noopHandler) ServeConn(context.Context, Conn) error { return nil }

// testHandler lets a test own the connection once hello has completed.
type testHandler struct {
	onConn func(ctx context.Context, conn Conn) error
	served chan struct{}
}

func newTestHandler(fn func(context.Context, Conn) error) *testHandler {
	return &testHandler{onConn: fn, served: make(chan struct{}, 1)}
}

func (h *testHandler) ServeConn(ctx context.Context, conn Conn) error {
	select {
	case h.served <- struct{}{}:
	default:
	}
	if h.onConn != nil {
		return h.onConn(ctx, conn)
	}
	return nil
}

// startServer runs a Server on a random loopback port and returns its ws URL.
func startServer(t *testing.T, h ConnHandler, cfg *Config) string {
	t.Helper()
	srv := &Server{handler: h, cfg: cfg, log: discardLogger()}
	hs := httptest.NewServer(srv)
	t.Cleanup(hs.Close)
	return "ws" + strings.TrimPrefix(hs.URL, "http")
}

// sayHello dials, sends nabu.hello with version, and returns the reply.
func sayHello(t *testing.T, url, version string) helloResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })

	req := helloRequest{
		JSONRPC: "2.0", ID: 1, Method: "nabu.hello",
		Params: map[string]any{"client": "test", "client_version": "0", "protocol_version": version},
	}
	if err := wsjson.Write(ctx, c, req); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	var resp helloResponse
	if err := wsjson.Read(ctx, c, &resp); err != nil {
		t.Fatalf("read hello reply: %v", err)
	}
	return resp
}

func TestRejectNonWebSocket(t *testing.T) {
	srv := &Server{handler: noopHandler{}, cfg: &Config{}, log: discardLogger()}
	hs := httptest.NewServer(srv)
	defer hs.Close()

	resp, err := http.Get(hs.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusUpgradeRequired)
	}
	if got := resp.Header.Get("Upgrade"); got != "websocket" {
		t.Errorf("Upgrade header: got %q, want %q", got, "websocket")
	}
}

func TestHelloHandshake(t *testing.T) {
	h := newTestHandler(nil)
	url := startServer(t, h, &Config{})

	resp := sayHello(t, url, protocol.Version)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.JSONRPC != "2.0" {
		t.Errorf("jsonrpc: got %q, want %q", resp.JSONRPC, "2.0")
	}
	if resp.Result["daemon_version"] != DaemonVersion {
		t.Errorf("daemon_version: got %v, want %q", resp.Result["daemon_version"], DaemonVersion)
	}
	if resp.Result["protocol_version"] != protocol.Version {
		t.Errorf("protocol_version: got %v, want %q", resp.Result["protocol_version"], protocol.Version)
	}
	caps, ok := resp.Result["capabilities"].([]any)
	if !ok || len(caps) == 0 {
		t.Fatalf("capabilities: got %#v, want a non-empty array", resp.Result["capabilities"])
	}

	select {
	case <-h.served:
	case <-time.After(5 * time.Second):
		t.Fatal("handler was never reached after a successful hello")
	}
}

func TestNonHelloBeforeHelloIsRefused(t *testing.T) {
	h := newTestHandler(nil)
	url := startServer(t, h, &Config{})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	req := helloRequest{JSONRPC: "2.0", ID: 7, Method: "nabu.session.list"}
	if err := wsjson.Write(ctx, c, req); err != nil {
		t.Fatal(err)
	}
	var resp helloResponse
	if err := wsjson.Read(ctx, c, &resp); err != nil {
		t.Fatalf("expected a refusal, got read error: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected an error response for a non-hello first message")
	}
	if resp.Error.Code != protocol.CodeProtocolMismatch {
		t.Errorf("code: got %d, want %d", resp.Error.Code, protocol.CodeProtocolMismatch)
	}

	select {
	case <-h.served:
		t.Fatal("handler must not be reached when hello is refused")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestProtocolVersionMismatch(t *testing.T) {
	h := newTestHandler(nil)
	url := startServer(t, h, &Config{})

	resp := sayHello(t, url, "99.0")
	if resp.Error == nil {
		t.Fatal("expected an error for a mismatched major version")
	}
	if resp.Error.Code != protocol.CodeProtocolMismatch {
		t.Errorf("code: got %d, want %d", resp.Error.Code, protocol.CodeProtocolMismatch)
	}

	select {
	case <-h.served:
		t.Fatal("handler must not be reached on version mismatch")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestMinorVersionDifferenceAccepted(t *testing.T) {
	url := startServer(t, newTestHandler(nil), &Config{})

	// Same MAJOR, absurd MINOR: must be accepted.
	same := strings.SplitN(protocol.Version, ".", 2)[0] + ".999"
	resp := sayHello(t, url, same)
	if resp.Error != nil {
		t.Fatalf("a differing MINOR must be accepted, got %+v", resp.Error)
	}
}

func TestLoopbackNoTokenAccepted(t *testing.T) {
	// A token is configured, but loopback connections must not need it.
	url := startServer(t, newTestHandler(nil), &Config{Token: "sekrit"})
	resp := sayHello(t, url, protocol.Version)
	if resp.Error != nil {
		t.Fatalf("loopback must be accepted without a token, got %+v", resp.Error)
	}
}

// The auth decision is exercised directly with a synthetic RemoteAddr, so the
// test does not depend on this machine having a non-loopback interface.
func TestBearerTokenOnNonLoopback(t *testing.T) {
	for _, tc := range []struct {
		name     string
		remote   string
		header   string
		token    string
		wantAuth bool
	}{
		{"non-loopback with correct token upgrades", "203.0.113.7:5000", "Bearer sekrit", "sekrit", true},
		{"non-loopback with wrong token refused", "203.0.113.7:5000", "Bearer nope", "sekrit", false},
		{"non-loopback with no token refused", "203.0.113.7:5000", "", "sekrit", false},
		// Spec 5: connections from non-loopback addresses must present a
		// bearer token. With none configured there is nothing to present, so
		// the connection is refused rather than admitted unauthenticated. The
		// daemon runs shell commands in the owner's repositories; an open port
		// on a shared network is the worst possible default.
		{"non-loopback refused when no token is configured", "203.0.113.7:5000", "", "", false},
		{"non-loopback refused even with a header when none is configured", "203.0.113.7:5000", "Bearer anything", "", false},
		{"loopback needs no token", "127.0.0.1:5000", "", "sekrit", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := &Server{handler: noopHandler{}, cfg: &Config{Token: tc.token}, log: discardLogger()}
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tc.remote
			r.Header.Set("Connection", "Upgrade")
			r.Header.Set("Upgrade", "websocket")
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, r)

			gotAuth := w.Code != http.StatusUnauthorized
			if gotAuth != tc.wantAuth {
				t.Fatalf("status %d: authorized=%v, want authorized=%v", w.Code, gotAuth, tc.wantAuth)
			}
		})
	}
}

func TestIsLoopback(t *testing.T) {
	for _, tc := range []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8737", true},
		{"127.0.0.1", true},
		{"[::1]:8737", true},
		{"::1", true},
		{"localhost:8737", true},
		{"localhost", true},
		{"127.0.0.53", true},
		{"203.0.113.7:5000", false},
		{"100.114.148.58:8737", false},
		{"", false},
		{"not-an-address", false},
	} {
		if got := isLoopback(tc.addr); got != tc.want {
			t.Errorf("isLoopback(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

func TestMajorVersion(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"1.0", 1, false},
		{"2.17", 2, false},
		{"10.3", 10, false},
		{"1", 0, true},
		{"", 0, true},
		{"x.0", 0, true},
	} {
		got, err := majorVersion(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("majorVersion(%q): want error, got %d", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("majorVersion(%q): %v", tc.in, err)
		} else if got != tc.want {
			t.Errorf("majorVersion(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
