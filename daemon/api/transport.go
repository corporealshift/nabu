package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/corporealshift/nabu/protocol"
)

// DaemonVersion is reported to clients in the hello handshake.
const DaemonVersion = "0.0.0"

// Config holds the server configuration read from the daemon config.
type Config struct {
	Bind  string // listen address, e.g. "127.0.0.1:8737"
	Token string // bearer token required from non-loopback connections
}

// ConnHandler is called once a connection is fully established: upgraded,
// authenticated, and past the hello handshake. Implementations own JSON-RPC
// dispatch and subscription fan-out.
type ConnHandler interface {
	// ServeConn runs the connection's message loop until it closes.
	ServeConn(ctx context.Context, conn Conn) error
}

// Conn is the minimal WebSocket surface the handler needs, so handler tests
// do not have to stand up a real socket.
type Conn interface {
	ReadJSON(ctx context.Context, v any) error
	WriteJSON(ctx context.Context, v any) error
	Close(code websocket.StatusCode, reason string)
	CloseNow()
}

// helloRequest is the JSON-RPC request a client sends first.
type helloRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

// helloResponse is the daemon's reply to nabu.hello.
type helloResponse struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Result  map[string]any `json:"result,omitempty"`
	Error   *rpcError      `json:"error,omitempty"`
}

// rpcError is a JSON-RPC error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Server accepts WebSocket connections and hands established ones to a
// ConnHandler.
type Server struct {
	httpServer *http.Server
	handler    ConnHandler
	cfg        *Config
	log        *slog.Logger
}

// NewServer creates a Server bound to cfg.Bind.
func NewServer(handler ConnHandler, cfg *Config, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	srv := &Server{handler: handler, cfg: cfg, log: log}
	srv.httpServer = &http.Server{Addr: cfg.Bind, Handler: srv}
	return srv
}

// ListenAndServe serves on cfg.Bind until Shutdown is called.
func (s *Server) ListenAndServe() error {
	return s.httpServer.ListenAndServe()
}

// Serve serves on an already-open listener. Tests use this with port 0 so they
// never collide with a running daemon or with each other.
func (s *Server) Serve(l net.Listener) error {
	return s.httpServer.Serve(l)
}

// Shutdown gracefully shuts the server down.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// ServeHTTP accepts WebSocket upgrades and refuses everything else with 426.
// Non-loopback connections must present the configured bearer token.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !isWebSocketUpgrade(r) {
		w.Header().Set("Connection", "Upgrade")
		w.Header().Set("Upgrade", "websocket")
		http.Error(w, "Upgrade Required", http.StatusUpgradeRequired)
		return
	}

	// Spec 5: connections from non-loopback addresses must present a bearer
	// token. With none configured there is nothing to present, so the
	// connection is refused rather than admitted unauthenticated. The daemon
	// runs shell commands in the owner's repositories, and an open port on a
	// shared network is the worst default available.
	if !isLoopback(r.RemoteAddr) {
		if s.cfg.Token == "" {
			s.log.Warn("refused a remote connection: no daemon token is configured",
				"remote", r.RemoteAddr)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+s.cfg.Token {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.log.Error("websocket accept failed", "error", err)
		return
	}
	defer c.CloseNow()

	ctx := r.Context()
	s.log.Debug("connection accepted", "remote", r.RemoteAddr)

	if err := enforceHello(ctx, c); err != nil {
		s.log.Warn("hello refused", "remote", r.RemoteAddr, "error", err)
		return
	}

	// A client closing cleanly, or going away, is not an error worth logging
	// at that level: it is the ordinary end of every connection.
	if err := s.handler.ServeConn(ctx, newWSConn(c)); err != nil && !normalClose(err) {
		s.log.Error("connection ended with error", "error", err)
	}
}

// enforceHello requires the first message to be nabu.hello with a compatible
// protocol version. Only the MAJOR component must match; a differing MINOR is
// accepted because clients ignore unknown fields.
func enforceHello(ctx context.Context, c *websocket.Conn) error {
	var req helloRequest
	if err := wsjson.Read(ctx, c, &req); err != nil {
		return err
	}

	refuse := func(msg string) error {
		_ = wsjson.Write(ctx, c, helloResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &rpcError{
				Code:    protocol.CodeProtocolMismatch,
				Message: protocol.ErrorNames[protocol.CodeProtocolMismatch] + ": " + msg,
			},
		})
		return fmt.Errorf("%s", msg)
	}

	if req.Method != "nabu.hello" {
		return refuse("first message must be nabu.hello, got " + req.Method)
	}

	clientVer, _ := req.Params["protocol_version"].(string)
	clientMajor, err := majorVersion(clientVer)
	if err != nil {
		return refuse(fmt.Sprintf("unparseable protocol_version %q", clientVer))
	}
	daemonMajor, err := majorVersion(protocol.Version)
	if err != nil {
		return fmt.Errorf("daemon protocol version %q is unparseable: %w", protocol.Version, err)
	}
	if clientMajor != daemonMajor {
		return refuse(fmt.Sprintf("protocol version %s is incompatible with daemon %s",
			clientVer, protocol.Version))
	}

	return wsjson.Write(ctx, c, helloResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"daemon_version":   DaemonVersion,
			"protocol_version": protocol.Version,
			"capabilities":     []string{"delta", "tasks", "goal"},
		},
	})
}

// majorVersion extracts the MAJOR component of a "MAJOR.MINOR" version.
func majorVersion(v string) (int, error) {
	major, _, ok := strings.Cut(v, ".")
	if !ok {
		return 0, fmt.Errorf("version %q has no MAJOR.MINOR form", v)
	}
	return strconv.Atoi(major)
}

// normalClose reports whether err is the ordinary end of a connection rather
// than a fault.
func normalClose(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return true
	}
	switch websocket.CloseStatus(err) {
	case websocket.StatusNormalClosure, websocket.StatusGoingAway, websocket.StatusNoStatusRcvd:
		return true
	}
	return false
}

// isLoopback reports whether addr, with or without a port, is loopback.
func isLoopback(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isWebSocketUpgrade reports whether r carries the WebSocket handshake headers.
func isWebSocketUpgrade(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") &&
		strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// wsConn adapts a coder/websocket connection to Conn.
type wsConn struct {
	*websocket.Conn
}

func newWSConn(c *websocket.Conn) Conn { return wsConn{c} }

func (c wsConn) ReadJSON(ctx context.Context, v any) error {
	return wsjson.Read(ctx, c.Conn, v)
}

func (c wsConn) WriteJSON(ctx context.Context, v any) error {
	return wsjson.Write(ctx, c.Conn, v)
}

func (c wsConn) Close(code websocket.StatusCode, reason string) {
	_ = c.Conn.Close(code, reason)
}

func (c wsConn) CloseNow() { _ = c.Conn.CloseNow() }
