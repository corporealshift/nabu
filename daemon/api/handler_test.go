package api

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/corporealshift/nabu/daemon/agent"
	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/daemon/session"
	"github.com/corporealshift/nabu/daemon/tools"
	"github.com/corporealshift/nabu/daemon/workspace"
	"github.com/corporealshift/nabu/protocol"
)

// harness wires a Handler over a temp dir and a scripted provider.
type harness struct {
	h     *Handler
	cs    *connState
	m     *agent.Manager
	fake  *provider.Fake
	store *session.Store
	dir   string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	store, err := session.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	pr := provider.NewRegistry()
	fake := &provider.Fake{}
	pr.Add(provider.Config{Name: "fake", ContextWindow: 8000}, fake, true)
	builtins := &tools.Builtins{}
	mr := module.NewRegistry([]module.Module{builtins}, module.Options{Log: discardLogger()})

	m, err := agent.New(agent.Deps{
		Store: store, Providers: pr, Modules: mr, Builtins: builtins,
		Root: dir, Log: discardLogger(),
	}, agent.Config{DefaultModel: "fake/m"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	hn := &harness{h: NewHandler(m, store, discardLogger()), m: m, fake: fake, store: store, dir: dir}
	hn.cs = &connState{conn: &scriptedConn{}, ctx: context.Background()}
	return hn
}

// call dispatches one request and returns the response envelope.
func (hn *harness) call(t *testing.T, id any, method string, params any) *jsonrpcResponse {
	t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != nil {
		req["id"] = id
	}
	if params != nil {
		req["params"] = params
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return hn.h.dispatch(context.Background(), hn.cs, raw)
}

// result re-decodes a response result into v.
func result(t *testing.T, resp *jsonrpcResponse, v any) {
	t.Helper()
	if resp == nil {
		t.Fatal("no response")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected rpc error: %+v", resp.Error)
	}
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatal(err)
	}
}

func (hn *harness) mustCreate(t *testing.T) string {
	t.Helper()
	resp := hn.call(t, 1, "nabu.session.create", map[string]any{"workspace": hn.dir})
	var out struct {
		SessionID string         `json:"session_id"`
		Event     protocol.Event `json:"event"`
	}
	result(t, resp, &out)
	if out.SessionID == "" {
		t.Fatal("create returned no session_id")
	}
	return out.SessionID
}

func TestMalformedJSONReturnsParseError(t *testing.T) {
	hn := newHarness(t)
	resp := hn.h.dispatch(context.Background(), hn.cs, json.RawMessage(`{not json`))
	if resp == nil || resp.Error == nil {
		t.Fatal("want a parse error response")
	}
	if resp.Error.Code != protocol.CodeParseError {
		t.Errorf("code: got %d, want %d", resp.Error.Code, protocol.CodeParseError)
	}
}

func TestRejectsWrongJSONRPCVersion(t *testing.T) {
	hn := newHarness(t)
	resp := hn.h.dispatch(context.Background(), hn.cs,
		json.RawMessage(`{"jsonrpc":"1.0","id":1,"method":"nabu.session.list"}`))
	if resp == nil || resp.Error == nil {
		t.Fatal("want an invalid-request response")
	}
	if resp.Error.Code != protocol.CodeInvalidRequest {
		t.Errorf("code: got %d, want %d", resp.Error.Code, protocol.CodeInvalidRequest)
	}
}

func TestUnknownMethod(t *testing.T) {
	hn := newHarness(t)
	resp := hn.call(t, 9, "nabu.session.teleport", nil)
	if resp == nil || resp.Error == nil {
		t.Fatal("want a method-not-found response")
	}
	if resp.Error.Code != protocol.CodeMethodNotFound {
		t.Errorf("code: got %d, want %d", resp.Error.Code, protocol.CodeMethodNotFound)
	}
}

func TestIDEchoing(t *testing.T) {
	hn := newHarness(t)
	for _, id := range []any{float64(42), "abc"} {
		resp := hn.call(t, id, "nabu.session.list", nil)
		if resp == nil {
			t.Fatalf("id %v: no response", id)
		}
		if resp.ID != id {
			t.Errorf("id: got %#v, want %#v", resp.ID, id)
		}
		if resp.JSONRPC != "2.0" {
			t.Errorf("jsonrpc: got %q", resp.JSONRPC)
		}
	}
}

// A request with no id is a notification and gets no reply.
func TestNotificationGetsNoResponse(t *testing.T) {
	hn := newHarness(t)
	if resp := hn.call(t, nil, "nabu.session.list", nil); resp != nil {
		t.Fatalf("notification must not be answered, got %+v", resp)
	}
}

func TestSessionListEmpty(t *testing.T) {
	hn := newHarness(t)
	var out struct {
		Sessions []session.Summary `json:"sessions"`
	}
	result(t, hn.call(t, 1, "nabu.session.list", nil), &out)
	if out.Sessions == nil {
		t.Fatal("sessions must be an empty array, not null")
	}
	if len(out.Sessions) != 0 {
		t.Fatalf("sessions: got %d, want 0", len(out.Sessions))
	}
}

func TestSessionListWithSessions(t *testing.T) {
	hn := newHarness(t)
	first := hn.mustCreate(t)
	second := hn.mustCreate(t)

	var out struct {
		Sessions []session.Summary `json:"sessions"`
	}
	result(t, hn.call(t, 2, "nabu.session.list", nil), &out)
	if len(out.Sessions) != 2 {
		t.Fatalf("sessions: got %d, want 2", len(out.Sessions))
	}
	seen := map[string]bool{}
	for _, s := range out.Sessions {
		seen[s.SessionID] = true
	}
	if !seen[first] || !seen[second] {
		t.Fatalf("both sessions must be listed, got %v", seen)
	}
}

func TestSessionCreate(t *testing.T) {
	hn := newHarness(t)
	resp := hn.call(t, 1, "nabu.session.create", map[string]any{
		"workspace": hn.dir,
		"options":   map[string]any{"permission_mode": "bypass"},
	})
	var out struct {
		SessionID string         `json:"session_id"`
		Event     protocol.Event `json:"event"`
	}
	result(t, resp, &out)

	if out.SessionID == "" {
		t.Fatal("session_id is empty")
	}
	if out.Event.Type != protocol.EventSession {
		t.Errorf("first event type: got %q, want %q", out.Event.Type, protocol.EventSession)
	}
	st, rpcErr := hn.h.getSession(out.SessionID)
	if rpcErr != nil {
		t.Fatalf("created session not retrievable: %+v", rpcErr)
	}
	if got := st.State().Options.PermissionMode; got != protocol.PermissionBypass {
		t.Errorf("permission_mode: got %q, want %q", got, protocol.PermissionBypass)
	}
}

func TestSessionCreateMissingWorkspace(t *testing.T) {
	hn := newHarness(t)
	for _, params := range []any{
		map[string]any{},
		map[string]any{"workspace": "   "},
	} {
		resp := hn.call(t, 1, "nabu.session.create", params)
		if resp == nil || resp.Error == nil {
			t.Fatalf("params %v: want an invalid-params error", params)
		}
		if resp.Error.Code != protocol.CodeInvalidParams {
			t.Errorf("code: got %d, want %d", resp.Error.Code, protocol.CodeInvalidParams)
		}
	}
}

func TestSessionNotFound(t *testing.T) {
	hn := newHarness(t)
	for _, method := range []string{"nabu.session.state", "nabu.session.events_after", "nabu.session.compact"} {
		resp := hn.call(t, 1, method, map[string]any{"session_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
		if resp == nil || resp.Error == nil {
			t.Fatalf("%s: want a session-not-found error", method)
		}
		if resp.Error.Code != protocol.CodeSessionNotFound {
			t.Errorf("%s code: got %d, want %d", method, resp.Error.Code, protocol.CodeSessionNotFound)
		}
	}
}

func TestSessionIDRequired(t *testing.T) {
	hn := newHarness(t)
	for _, method := range []string{"nabu.session.state", "nabu.session.events_after", "nabu.session.compact"} {
		resp := hn.call(t, 1, method, map[string]any{})
		if resp == nil || resp.Error == nil {
			t.Fatalf("%s: want an invalid-params error", method)
		}
		if resp.Error.Code != protocol.CodeInvalidParams {
			t.Errorf("%s code: got %d, want %d", method, resp.Error.Code, protocol.CodeInvalidParams)
		}
	}
}

func TestEventsAfter(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)

	var all struct {
		Events []protocol.Event `json:"events"`
		Synced bool             `json:"synced"`
	}
	result(t, hn.call(t, 1, "nabu.session.events_after",
		map[string]any{"session_id": id}), &all)
	if len(all.Events) == 0 {
		t.Fatal("a nil cursor must return the whole log")
	}
	if !all.Synced {
		t.Error("synced should be true for a nil cursor")
	}

	last := all.Events[len(all.Events)-1].ID
	var tail struct {
		Events []protocol.Event `json:"events"`
		Synced bool             `json:"synced"`
	}
	result(t, hn.call(t, 2, "nabu.session.events_after",
		map[string]any{"session_id": id, "last_event_id": last}), &tail)
	if tail.Events == nil {
		t.Fatal("events must be an empty array, not null")
	}
	if len(tail.Events) != 0 {
		t.Fatalf("cursor at the tail must return nothing, got %d", len(tail.Events))
	}
}

func TestEventsAfterUnknownCursor(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)
	resp := hn.call(t, 1, "nabu.session.events_after", map[string]any{
		"session_id":    id,
		"last_event_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV",
	})
	if resp == nil || resp.Error == nil {
		t.Fatal("an unknown cursor must be an error")
	}
	if resp.Error.Code != protocol.CodeCursorUnknown {
		t.Errorf("code: got %d, want %d", resp.Error.Code, protocol.CodeCursorUnknown)
	}
}

func TestSessionState(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)

	var st protocol.State
	result(t, hn.call(t, 1, "nabu.session.state", map[string]any{"session_id": id}), &st)
	if st.State != protocol.StateIdle {
		t.Errorf("state: got %q, want %q", st.State, protocol.StateIdle)
	}
	if st.Tasks == nil {
		t.Error("tasks must be an empty array, not null")
	}
	if st.LastEventID == "" {
		t.Error("last_event_id should be set after creation")
	}
}

// ServeConn must answer requests in order and return when the peer goes away.
func TestServeConnLoop(t *testing.T) {
	hn := newHarness(t)
	c := &scriptedConn{in: []string{
		`{"jsonrpc":"2.0","id":1,"method":"nabu.session.list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"nabu.session.list"}`,
	}}
	if err := hn.h.ServeConn(context.Background(), c); err != io.EOF {
		t.Fatalf("ServeConn should end with the read error, got %v", err)
	}
	if len(c.out) != 2 {
		t.Fatalf("responses: got %d, want 2", len(c.out))
	}
	for i, want := range []float64{1, 2} {
		if c.out[i].ID != want {
			t.Errorf("response %d id: got %#v, want %v", i, c.out[i].ID, want)
		}
	}
}

// scriptedConn replays canned requests and records responses.
type scriptedConn struct {
	in  []string
	out []*jsonrpcResponse
}

func (c *scriptedConn) ReadJSON(_ context.Context, v any) error {
	if len(c.in) == 0 {
		return io.EOF
	}
	next := c.in[0]
	c.in = c.in[1:]
	return json.NewDecoder(strings.NewReader(next)).Decode(v)
}

func (c *scriptedConn) WriteJSON(_ context.Context, v any) error {
	resp, ok := v.(*jsonrpcResponse)
	if !ok {
		return nil
	}
	c.out = append(c.out, resp)
	return nil
}

func (c *scriptedConn) Close(websocket.StatusCode, string) {}
func (c *scriptedConn) CloseNow()                          {}

// The picker on a phone cannot see the daemon's filesystem, so browsing is how
// it finds a directory at all.
func TestWorkspaceBrowseOverRPC(t *testing.T) {
	hn := newHarness(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "proj", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	hn.h.SetBrowseRoots([]string{root})

	t.Run("no path lists the roots", func(t *testing.T) {
		var out workspace.Listing
		result(t, hn.call(t, 1, "nabu.workspace.browse", map[string]any{}), &out)
		if len(out.Entries) != 1 || out.Entries[0].Path != filepath.ToSlash(root) {
			t.Fatalf("entries = %+v, want the one root", out.Entries)
		}
		if out.Parent != nil {
			t.Error("the top level has no parent")
		}
	})

	t.Run("a path lists its directories and marks repos", func(t *testing.T) {
		var out workspace.Listing
		result(t, hn.call(t, 2, "nabu.workspace.browse", map[string]any{"path": root}), &out)
		if len(out.Entries) != 1 {
			t.Fatalf("entries = %+v, want just proj (node_modules is noise)", out.Entries)
		}
		if out.Entries[0].Name != "proj" || !out.Entries[0].IsRepo {
			t.Errorf("entry = %+v, want proj marked as a repo", out.Entries[0])
		}
	})

	t.Run("outside the roots is invalid params", func(t *testing.T) {
		resp := hn.call(t, 3, "nabu.workspace.browse", map[string]any{"path": t.TempDir()})
		if resp == nil || resp.Error == nil {
			t.Fatalf("browsing outside the roots should fail, got %+v", resp)
		}
		if resp.Error.Code != protocol.CodeInvalidParams {
			t.Errorf("code = %d, want invalid params", resp.Error.Code)
		}
	})
}

// Browsing to a directory and then creating a session there is the whole point,
// so the two halves have to fit together.
func TestBrowseThenCreateSessionThere(t *testing.T) {
	hn := newHarness(t)
	root := t.TempDir()
	target := filepath.Join(root, "proj")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	hn.h.SetBrowseRoots([]string{root})

	var listing workspace.Listing
	result(t, hn.call(t, 1, "nabu.workspace.browse", map[string]any{"path": root}), &listing)
	if len(listing.Entries) != 1 {
		t.Fatalf("entries = %+v", listing.Entries)
	}

	// The path the listing gave is handed straight back to create, with no
	// massaging: a client should not have to rewrite it.
	resp := hn.call(t, 2, "nabu.session.create", map[string]any{"workspace": listing.Entries[0].Path})
	if resp == nil || resp.Error != nil {
		t.Fatalf("create with a browsed path failed: %+v", resp)
	}
}

// Creating a directory is the one thing a client makes the daemon write, so the
// checks on both halves matter more than the happy path.
func TestWorkspaceCreateDirectoryOverRPC(t *testing.T) {
	hn := newHarness(t)
	root := t.TempDir()
	parent := filepath.Join(root, "projects")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	hn.h.SetBrowseRoots([]string{root})

	t.Run("creates and returns the new directory", func(t *testing.T) {
		var out workspace.Listing
		result(t, hn.call(t, 1, "nabu.workspace.create_directory",
			map[string]any{"parent": parent, "name": "fresh"}), &out)

		want := filepath.ToSlash(filepath.Join(parent, "fresh"))
		if out.Path != want {
			t.Fatalf("path = %q, want %q", out.Path, want)
		}
		if info, err := os.Stat(filepath.Join(parent, "fresh")); err != nil || !info.IsDir() {
			t.Fatalf("not created: %v", err)
		}
	})

	// And the path it returns is one session.create accepts unchanged, which is
	// the whole flow: make a directory, start work in it.
	t.Run("a session starts in what was just created", func(t *testing.T) {
		var out workspace.Listing
		result(t, hn.call(t, 2, "nabu.workspace.create_directory",
			map[string]any{"parent": parent, "name": "brand-new"}), &out)

		resp := hn.call(t, 3, "nabu.session.create", map[string]any{"workspace": out.Path})
		if resp == nil || resp.Error != nil {
			t.Fatalf("create in the new directory failed: %+v", resp)
		}
	})

	t.Run("a name that is a path is refused", func(t *testing.T) {
		for _, name := range []string{"a/b", "../escape", "..", ".hidden", ""} {
			resp := hn.call(t, 4, "nabu.workspace.create_directory",
				map[string]any{"parent": parent, "name": name})
			if resp == nil || resp.Error == nil {
				t.Fatalf("name %q should have been refused", name)
			}
			if resp.Error.Code != protocol.CodeInvalidParams {
				t.Errorf("name %q: code = %d, want invalid params", name, resp.Error.Code)
			}
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape")); err == nil {
			t.Error("a refused name still wrote outside the root")
		}
	})

	t.Run("a parent outside the roots is refused", func(t *testing.T) {
		outside := t.TempDir()
		resp := hn.call(t, 5, "nabu.workspace.create_directory",
			map[string]any{"parent": outside, "name": "nope"})
		if resp == nil || resp.Error == nil {
			t.Fatal("a parent outside the roots should be refused")
		}
		if _, err := os.Stat(filepath.Join(outside, "nope")); err == nil {
			t.Error("it was created anyway")
		}
	})

	t.Run("a missing parent is refused", func(t *testing.T) {
		resp := hn.call(t, 6, "nabu.workspace.create_directory", map[string]any{"name": "x"})
		if resp == nil || resp.Error == nil {
			t.Fatal("no parent should be refused")
		}
	})
}

// Spec 7.15. The wire contract is the pair: an id a client can look up, and the
// mode that actually ran — which is not always the one that was asked for.
func TestCompactReturnsTheEventIDAndTheModeThatRan(t *testing.T) {
	hn := newHarness(t)
	hn.fake.Script = []provider.Response{
		{Content: "done"},
		{Content: "THE SUMMARY"}, // the summariser call
	}
	id := hn.mustCreate(t)
	if _, err := hn.m.Prompt(context.Background(), id, "go"); err != nil {
		t.Fatal(err)
	}
	hn.m.WaitIdle(id)

	resp := hn.call(t, 2, "nabu.session.compact", map[string]any{"session_id": id})
	var out struct {
		EventID string `json:"event_id"`
		Mode    string `json:"mode"`
	}
	result(t, resp, &out)
	if out.Mode != string(protocol.CompactionSummarize) {
		t.Fatalf("mode: %q", out.Mode)
	}

	s, err := hn.store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range s.Events() {
		if e.ID == out.EventID {
			found = true
			if e.Type != protocol.EventCompaction {
				t.Fatalf("event %s is a %s", e.ID, e.Type)
			}
		}
	}
	if !found {
		t.Fatalf("event_id %q is not in the log", out.EventID)
	}
}

// Refused while running, with the code a client can branch on rather than a
// message it would have to read (spec 7.15).
func TestCompactIsRefusedWhileTheSessionIsRunning(t *testing.T) {
	hn := newHarness(t)
	block := make(chan struct{})
	hn.fake.Script = []provider.Response{{Content: "done"}}
	hn.fake.BlockOn = block
	id := hn.mustCreate(t)
	if _, err := hn.m.Prompt(context.Background(), id, "go"); err != nil {
		t.Fatal(err)
	}
	s, err := hn.store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; s.State().State != protocol.StateRunning; i++ {
		if i > 2000 {
			t.Fatalf("never started running (state %s)", s.State().State)
		}
		time.Sleep(2 * time.Millisecond)
	}

	resp := hn.call(t, 2, "nabu.session.compact", map[string]any{"session_id": id})
	if resp == nil || resp.Error == nil {
		t.Fatal("want a refusal")
	}
	if resp.Error.Code != protocol.CodeInvalidTransition {
		t.Errorf("code: got %d, want %d", resp.Error.Code, protocol.CodeInvalidTransition)
	}

	close(block)
	hn.m.WaitIdle(id)
}

// Spec 7.19-7.20. Archived sessions leave the list and come back on request.
func TestArchiveAndRestoreOverRPC(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)

	result(t, hn.call(t, 2, "nabu.session.archive", map[string]any{"session_id": id}), &struct{}{})

	var active, archived struct {
		Sessions []struct {
			SessionID string `json:"session_id"`
			Archived  bool   `json:"archived"`
		} `json:"sessions"`
	}
	result(t, hn.call(t, 3, "nabu.session.list", nil), &active)
	if len(active.Sessions) != 0 {
		t.Fatalf("an archived session is still listed: %+v", active.Sessions)
	}
	result(t, hn.call(t, 4, "nabu.session.list", map[string]any{"archived": true}), &archived)
	if len(archived.Sessions) != 1 || archived.Sessions[0].SessionID != id || !archived.Sessions[0].Archived {
		t.Fatalf("archived list = %+v", archived.Sessions)
	}

	result(t, hn.call(t, 5, "nabu.session.restore", map[string]any{"session_id": id}), &struct{}{})
	result(t, hn.call(t, 6, "nabu.session.list", nil), &active)
	if len(active.Sessions) != 1 {
		t.Fatalf("a restored session should list again: %+v", active.Sessions)
	}

	resp := hn.call(t, 7, "nabu.session.restore", map[string]any{"session_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
	if resp == nil || resp.Error == nil || resp.Error.Code != protocol.CodeSessionNotFound {
		t.Fatalf("restoring an unknown session: %+v", resp)
	}
}

// dialHello opens a real websocket to the handler and completes the hello.
// Everything after it is read by a goroutine into the returned channel, which
// is what lets the test ping: a websocket answers control frames only while
// someone is reading.
func dialHello(t *testing.T, hn *harness) (*websocket.Conn, <-chan map[string]any) {
	t.Helper()
	url := startServer(t, hn.h, &Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	hello := map[string]any{"jsonrpc": "2.0", "id": 0, "method": "nabu.hello", "params": map[string]any{
		"client": "test", "client_version": "0", "protocol_version": protocol.Version}}
	if err := wsjson.Write(ctx, c, hello); err != nil {
		t.Fatal(err)
	}
	var reply map[string]any
	if err := wsjson.Read(ctx, c, &reply); err != nil {
		t.Fatal(err)
	}
	in := make(chan map[string]any, 16)
	go func() {
		for {
			var m map[string]any
			if err := wsjson.Read(context.Background(), c, &m); err != nil {
				close(in)
				return
			}
			in <- m
		}
	}()
	return c, in
}

// replies collects responses by id, so a test can wait for them in any order.
type replies struct {
	in  <-chan map[string]any
	got map[float64]map[string]any
}

// of waits for the response carrying id, keeping any others that arrive first.
func (r *replies) of(t *testing.T, id float64) map[string]any {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for r.got[id] == nil {
		select {
		case m, ok := <-r.in:
			if !ok {
				t.Fatalf("connection closed waiting for reply %v", id)
			}
			if n, isReply := m["id"].(float64); isReply {
				r.got[n] = m
			}
		case <-timeout:
			t.Fatalf("no reply to %v", id)
		}
	}
	return r.got[id]
}

// A compaction is minutes on a local model. The daemon used to run each call
// on the connection's read loop, and a websocket answers pings only while it
// is read — so a phone's keepalive went unanswered and it dropped the
// connection one ping interval into the summary (issue 87).
func TestTheConnectionStaysAnsweredWhileACompactionRuns(t *testing.T) {
	hn := newHarness(t)
	hn.fake.Script = []provider.Response{{Content: "done"}}
	id := hn.mustCreate(t)
	if _, err := hn.m.Prompt(context.Background(), id, "go"); err != nil {
		t.Fatal(err)
	}
	hn.m.WaitIdle(id)
	hn.fake.BlockOn = make(chan struct{}) // the summariser never answers
	summariser := hn.fake.CallCount()

	c, in := dialHello(t, hn)
	rs := &replies{in: in, got: map[float64]map[string]any{}}
	ctx := context.Background()
	if err := wsjson.Write(ctx, c, map[string]any{"jsonrpc": "2.0", "id": 1,
		"method": "nabu.session.compact", "params": map[string]any{"session_id": id}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; hn.fake.CallCount() == summariser; i++ {
		if i > 2500 {
			t.Fatal("the summariser was never called")
		}
		time.Sleep(2 * time.Millisecond)
	}

	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := c.Ping(pctx); err != nil {
		t.Fatalf("ping unanswered while compacting: %v", err)
	}

	// Interrupt is how a person gets out of a compaction, so it is answered
	// now rather than queued behind the call it exists to end.
	if err := wsjson.Write(ctx, c, map[string]any{"jsonrpc": "2.0", "id": 2,
		"method": "nabu.session.interrupt", "params": map[string]any{"session_id": id}}); err != nil {
		t.Fatal(err)
	}
	if r := rs.of(t, 2); r["error"] != nil {
		t.Fatalf("interrupt: %v", r["error"])
	}
	r := rs.of(t, 1)
	e, _ := r["error"].(map[string]any)
	if e == nil || e["code"] != float64(protocol.CodeInvalidTransition) {
		t.Fatalf("an interrupted compaction should say so: %v", r)
	}
}

// Spec 7.21-7.22 (issue 38): how much work a session was, and per day.
func TestStatsAndUsageOverRPC(t *testing.T) {
	hn := newHarness(t)
	hn.fake.Script = []provider.Response{{Content: "done", Usage: protocol.Usage{InputTokens: 120, OutputTokens: 7}}}
	id := hn.mustCreate(t)
	if _, err := hn.m.Prompt(context.Background(), id, "go"); err != nil {
		t.Fatal(err)
	}
	hn.m.WaitIdle(id)

	var s struct {
		Turns   int `json:"turns"`
		Prompts int `json:"prompts"`
		Tokens  struct {
			Input  int `json:"input"`
			Output int `json:"output"`
		} `json:"tokens"`
		PerTurn []any `json:"per_turn"`
	}
	result(t, hn.call(t, 2, "nabu.session.stats", map[string]any{"session_id": id}), &s)
	if s.Turns != 1 || s.Prompts != 1 || s.Tokens.Input != 120 || s.Tokens.Output != 7 || len(s.PerTurn) != 1 {
		t.Fatalf("stats = %+v", s)
	}

	var u struct {
		Days []struct {
			Date  string `json:"date"`
			Turns int    `json:"turns"`
			Input int    `json:"input"`
		} `json:"days"`
	}
	result(t, hn.call(t, 3, "nabu.usage", map[string]any{"days": 3}), &u)
	if len(u.Days) != 3 {
		t.Fatalf("want 3 days, got %+v", u.Days)
	}
	if today := u.Days[2]; today.Turns != 1 || today.Input != 120 {
		t.Errorf("today = %+v", today)
	}

	resp := hn.call(t, 4, "nabu.session.stats", map[string]any{"session_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
	if resp == nil || resp.Error == nil || resp.Error.Code != protocol.CodeSessionNotFound {
		t.Fatalf("stats for an unknown session: %+v", resp)
	}
}

// Archiving a session does not take its work out of the history: it happened.
func TestUsageCountsArchivedSessions(t *testing.T) {
	hn := newHarness(t)
	hn.fake.Script = []provider.Response{{Content: "done", Usage: protocol.Usage{InputTokens: 120, OutputTokens: 7}}}
	id := hn.mustCreate(t)
	if _, err := hn.m.Prompt(context.Background(), id, "go"); err != nil {
		t.Fatal(err)
	}
	hn.m.WaitIdle(id)
	result(t, hn.call(t, 2, "nabu.session.archive", map[string]any{"session_id": id}), &struct{}{})

	var u struct {
		Days []struct {
			Turns int `json:"turns"`
			Input int `json:"input"`
		} `json:"days"`
	}
	result(t, hn.call(t, 3, "nabu.usage", map[string]any{"days": 1}), &u)
	if len(u.Days) != 1 || u.Days[0].Turns != 1 || u.Days[0].Input != 120 {
		t.Fatalf("an archived session's turn is missing from today: %+v", u.Days)
	}
}
