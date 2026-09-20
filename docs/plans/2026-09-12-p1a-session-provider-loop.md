# nabu P1a — Session Store, Provider, Tools, Agent Loop Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** COMPLETE, 2026-09-12. All 17 tasks done; `go build ./... && go vet ./... &&
go test ./...` is green, and the live smoke test passed against Qwen3.6-35B-A3B on
llama.cpp (the model called `read`, got the file, loop returned to idle in two turns).
Findings recorded in "Decisions settled" below: `Store.Close`, `protocol.NewULIDAfter`,
`agent.CreateOptions`, and the stage-1 compaction range.

**Goal:** The agent loop runs end to end inside the daemon process — persisted
append-only sessions, a streaming OpenAI-compatible provider, built-in tools, the
stop gate, budget, and both compaction stages — proven against a scripted fake
provider and a live smoke test on the local Qwen server. No networking API, no CLI,
no policy modules yet (those are P1b and P1c).

**Architecture:** Every state transition is an appended `protocol.Event`
(`daemon/session`). The loop (`daemon/agent`) builds each request with
`protocol.Assemble`, streams from `daemon/provider`, gates and runs tools through
`module.Registry`, and asks the stop gate before stopping. Built-in tools are an
ordinary module (`daemon/tools`). Modules see the daemon only through
`module.Host` / `module.Session`, implemented in `daemon/agent`.

**Tech Stack:** Go 1.26, standard library only. `net/http` + hand-rolled SSE for the
provider. `httptest` for provider tests. `go test ./...` is the gate.

**Spec:** `docs/specs/2026-09-11-nabu-architecture-design.md` §4–§6,
§8–§10, §12, §14. Wire contract: `protocol/spec.md`.

**P1 is split into three plans** because the spec's P1 covers three independently
testable subsystems. P1a (this plan) is the engine. P1b is the WebSocket API, config
loading, daemon lifecycle and the CLI. P1c is the policy modules (`skills`, `guard`,
`verify`, `report`). The milestone is complete, and Claude Code can delegate to nabu,
only when all three land. Each ends with working, tested software.

**Task 13 is the largest task** in this plan: it builds the Manager, which is where
every other piece meets. Read its test file first, then implement in the order the
steps give. Every other task fits comfortably in one pass.

---

## Decisions settled before writing this plan

- **Turn end → `idle`, `stop` → `completed` + report.** The loop never decides a
  session is "completed"; it returns the session to `idle` when no module vetoes.
  `nabu.session.stop` (P1b's CLI calls it when a headless run goes idle) appends
  `completed` and the report. `blocked`, `paused`, `error` are set by the loop and
  also get a report. This keeps "headless vs interactive" out of the daemon.
- **Turn = one assistant `message` event**, including tool-call-only turns whose
  content is empty. `protocol.State.Turns` counts them; budget compares against it.
- **Assistant message before tool calls.** Per model round trip the loop appends the
  assistant `message` (content may be `""`), then one `tool_call` per call, then one
  `tool_result` per call after execution. Request building folds
  `message` + following `tool_call`s into one provider assistant message with
  `tool_calls`, and each `tool_result` into a `tool` role message.
- **Model string is `provider/model` or bare `model`** (default provider). Config
  names providers; the session option `model` carries the string.
- **Permission modes in P1a:** `bypass` turns `Ask` verdicts into `Allow`; `Deny`
  still denies. `ask` routes `Ask` to an `Asker`; P1a ships a test asker only.
  `auto` is identical to `ask` until the guard module exists (P1c).
- **Compaction thresholds** use the last assistant message's `usage.input_tokens`
  against the provider's configured `context_window`: stage 0 at 70 %, stage 1 at
  85 %. Stage 0 clears tool results older than the last 4 turns. Stage 1 summarizes
  with a model call whose prompt includes `BeforeCompaction` preserve strings.
- **Host is per module.** `module.Registry.Init` takes `hostFor(name) Host` so tool
  calls made by a module are logged with `source: module:<name>`. Small change to the
  `module` package (Task 1).
- **`RenderTasks` moves into `protocol`** so the `task.update` tool result and the
  Current state block render tasks identically (Task 1).
- **Windows process trees:** `bash` uses `exec.CommandContext` with `WaitDelay`. Job
  objects (killing grandchildren on timeout) are a P1b task, not this plan.
- **No `index.json` yet.** `Store.List` scans `sessions/*.jsonl`; the index is an
  optimization for when logs are large.
- **`Store.Close` exists** and closes every open session file. The daemon needs it at
  shutdown, and on Windows an open handle blocks directory removal, so every test
  that opens a store must close it (the `openAt` helper in `session_test.go` does).
- **`Manager.Create` takes `agent.CreateOptions`, not `protocol.Options`.** The
  protocol type's `compaction_enabled` is a required bool, so its zero value reads as
  "compaction off" — the opposite of the spec's default, and silently so. CreateOptions
  makes that one field a `*bool`: nil means on, and false is an explicit choice. Model
  and permission mode keep plain string fields because "" is unambiguous.
- **Ordering needs `protocol.NewULIDAfter`, not `NewULID`.** Plain ULIDs order only by
  millisecond; two generated in the same millisecond order by their random bits, which
  is not creation order. Both sequences that must stay ordered — a session's events and
  a store's session ids — generate through `NewULIDAfter(prev)`. Found when two
  sessions created in one millisecond listed out of order.

## File structure

| Path | Responsibility |
|---|---|
| `protocol/render.go` | **Modify.** Add `RenderTasks([]Task) string`; `RenderCurrentState` uses it. |
| `daemon/module/registry.go` | **Modify.** `Init(hostFor func(string) Host, configFor func(string) Config)`. |
| `daemon/module/registry_test.go` | **Modify.** Update the Init test. |
| `daemon/workspace/workspace.go` | **New.** `Resolve(path) (Workspace, error)`: absolute path, git common dir, stable key. |
| `daemon/workspace/workspace_test.go` | **New.** |
| `daemon/session/store.go` | **New.** `Store`: root dir, `Create`, `Get`, `List`, `RecoverInterrupted`. |
| `daemon/session/session.go` | **New.** `Session`: append (stamp, validate, persist, broadcast), read, projection, subscribe. |
| `daemon/session/session_test.go`, `store_test.go` | **New.** |
| `daemon/provider/provider.go` | **New.** Types: `Message`, `ToolCall`, `ToolSpec`, `Request`, `Response`, `Provider` interface, `Config`. |
| `daemon/provider/fake.go` | **New.** Scripted provider for tests. |
| `daemon/provider/openai.go` | **New.** Streaming chat-completions client: SSE parse, tool-call accumulation, usage. |
| `daemon/provider/limiter.go` | **New.** `max_in_flight` semaphore and retry-with-backoff wrapper. |
| `daemon/provider/registry.go` | **New.** `Registry.Resolve(model) (Provider, modelName, Config, error)`. |
| `daemon/provider/*_test.go` | **New.** |
| `daemon/tools/files.go` | **New.** read, write, edit, glob, grep. |
| `daemon/tools/bash.go` | **New.** bash with timeout and output truncation. |
| `daemon/tools/tasks.go` | **New.** `task.update` and the `TaskStore` interface. |
| `daemon/tools/builtins.go` | **New.** `Builtins` module implementing `module.ToolProvider`. |
| `daemon/tools/*_test.go` | **New.** |
| `daemon/agent/config.go` | **New.** `Config`, `CompactionConfig`, defaults, the static system prompt. |
| `daemon/agent/request.go` | **New.** `buildRequest(log, systemPrompt, tools) provider.Request` from `protocol.Assemble`. |
| `daemon/agent/handle.go` | **New.** `sessionHandle` (implements `module.Session`), `host` (implements `module.Host`). |
| `daemon/agent/manager.go` | **New.** `Manager`: create/prompt/goal/option/tasks/budget/interrupt/stop/resume/shutdown. |
| `daemon/agent/runner.go` | **New.** The loop: request, stream, tools, stop gate, budget, report. |
| `daemon/agent/compaction.go` | **New.** Threshold checks, stage 0 and stage 1. |
| `daemon/agent/*_test.go` | **New.** Fake-provider loop tests; env-gated live test. |

## Conventions

- Commit style: `area: lowercase summary`. Stage named files.
- Run the full gate before every commit: `go build ./... && go vet ./... && go test ./...`
- Tests use `t.TempDir()` for any on-disk state. Never touch `~/.nabu`.
- All paths in this plan are relative to the repo root `C:\Users\corpo\Documents\projects\nabu`.

---

### Task 1: `protocol.RenderTasks` and per-module hosts

**Files:**
- Modify: `protocol/render.go`
- Modify: `daemon/module/registry.go` (the `Init` method)
- Modify: `daemon/module/registry_test.go` (`TestInitHonoursEnabledFlagAndSurvivesFailure`)

- [x] **Step 1: Write the failing test for RenderTasks**

Append to `protocol/protocol_test.go`:

```go
func TestRenderTasksMatchesStateBlock(t *testing.T) {
	tasks := []Task{
		{ID: "t1", Title: "A", Status: TaskDone, BlockedBy: []string{}},
		{ID: "t2", Title: "B", Status: TaskBlocked, BlockedBy: []string{}, Note: "waiting"},
	}
	want := "Tasks (1/2 done):\n- [x] t1 A\n- [!] t2 B — blocked: waiting"
	if got := RenderTasks(tasks); got != want {
		t.Fatalf("RenderTasks:\n%s\nwant:\n%s", got, want)
	}
	if got := RenderTasks(nil); got != "Tasks: none" {
		t.Fatalf("empty: %q", got)
	}
}
```

- [x] **Step 2: Run it to see it fail**

Run: `go test ./protocol/ -run TestRenderTasks`
Expected: compile error `undefined: RenderTasks`.

- [x] **Step 3: Implement RenderTasks and reuse it**

In `protocol/render.go`, replace the tasks section of `RenderCurrentState` and add the function:

```go
// RenderTasks renders a task list in the same form the Current state block
// uses, so tool results and the state block never disagree.
func RenderTasks(tasks []Task) string {
	if len(tasks) == 0 {
		return "Tasks: none"
	}
	done := 0
	for _, t := range tasks {
		if t.Status == TaskDone {
			done++
		}
	}
	lines := []string{fmt.Sprintf("Tasks (%d/%d done):", done, len(tasks))}
	for _, t := range tasks {
		lines = append(lines, renderTaskLine(t))
	}
	return strings.Join(lines, "\n")
}
```

and in `RenderCurrentState` replace

```go
	if len(s.Tasks) > 0 {
		lines = append(lines, fmt.Sprintf("Tasks (%d/%d done):", s.DoneTasks(), len(s.Tasks)))
		for _, t := range s.Tasks {
			lines = append(lines, renderTaskLine(t))
		}
	}
```

with

```go
	if len(s.Tasks) > 0 {
		lines = append(lines, RenderTasks(s.Tasks))
	}
```

- [x] **Step 4: Run protocol tests (vectors must still pass)**

Run: `go test ./protocol/ -count=1`
Expected: `ok`.

- [x] **Step 5: Change `Registry.Init` to take a host factory**

In `daemon/module/registry.go` replace the `Init` signature and the call to `m.Init`:

```go
// Init initialises every module. hostFor returns the Host for a module (each
// module gets its own so its tool calls carry source module:<name>);
// configFor returns its config section. A module whose config says
// enabled=false is not initialised. Init failures are recorded, not fatal.
func (r *Registry) Init(hostFor func(name string) Host, configFor func(name string) Config) {
	for _, m := range r.modules {
		cfg := configFor(m.Name())
		if !cfg.Enabled() {
			r.dead[m.Name()] = fmt.Errorf("disabled by config")
			continue
		}
		func() {
			defer func() {
				if p := recover(); p != nil {
					r.dead[m.Name()] = fmt.Errorf("panic in Init: %v", p)
				}
			}()
			if err := m.Init(hostFor(m.Name()), cfg); err != nil {
				r.dead[m.Name()] = err
			}
		}()
		if err, bad := r.dead[m.Name()]; bad {
			r.opts.Log.Error("module init failed", "module", m.Name(), "err", err)
		}
	}
}
```

In `daemon/module/registry_test.go` change the call in `TestInitHonoursEnabledFlagAndSurvivesFailure`:

```go
	r.Init(func(string) Host { return nil }, func(name string) Config {
		if name == "off" {
			return Config{"enabled": false}
		}
		return Config{}
	})
```

- [x] **Step 6: Run the gate**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: all `ok`.

- [x] **Step 7: Commit**

```bash
git add protocol/render.go protocol/protocol_test.go daemon/module/registry.go daemon/module/registry_test.go
git commit -m "protocol: RenderTasks; module: per-module hosts at Init"
```

---

### Task 2: Workspace resolution and key

**Files:**
- Create: `daemon/workspace/workspace.go`
- Create: `daemon/workspace/workspace_test.go`

- [x] **Step 1: Write the failing tests**

`daemon/workspace/workspace_test.go`:

```go
package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestResolveNonGitUsesAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	ws, err := Resolve(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Path != dir || ws.GitRoot != "" {
		t.Fatalf("ws: %+v", ws)
	}
	if ws.Key == "" || len(ws.Key) > 64 {
		t.Fatalf("key: %q", ws.Key)
	}
	// Same path → same key; different path → different key.
	ws2, _ := Resolve(dir)
	if ws2.Key != ws.Key {
		t.Fatal("key must be stable")
	}
	other, _ := Resolve(t.TempDir())
	if other.Key == ws.Key {
		t.Fatal("different paths must not collide")
	}
}

func TestResolveGitSharesKeyAcrossSubdirs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	top, err := Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	nested, err := Resolve(sub)
	if err != nil {
		t.Fatal(err)
	}
	if top.Key != nested.Key {
		t.Fatalf("keys differ: %q vs %q", top.Key, nested.Key)
	}
	if nested.Path != sub {
		t.Fatalf("path must be the working dir, got %q", nested.Path)
	}
	if top.GitRoot == "" {
		t.Fatal("GitRoot must be set inside a repo")
	}
}

func TestSlug(t *testing.T) {
	if got := slug("My Repo!!"); got != "my-repo" {
		t.Fatalf("slug: %q", got)
	}
}
```

- [x] **Step 2: Run to see it fail**

Run: `go test ./daemon/workspace/`
Expected: compile errors, `undefined: Resolve`, `undefined: slug`.

- [x] **Step 3: Implement**

`daemon/workspace/workspace.go`:

```go
package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
)

// Workspace identifies where a session runs. Key is stable across worktrees
// and nested directories of one repository (spec §11.2): slug of the repo
// directory name plus a short hash of the git common dir; outside git, slug
// plus hash of the absolute path.
type Workspace struct {
	Path    string // absolute working directory
	Key     string
	GitRoot string // absolute path of the git common dir's parent, or ""
}

// Resolve computes the Workspace for path.
func Resolve(path string) (Workspace, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Workspace{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Workspace{}, err
	}
	if !info.IsDir() {
		return Workspace{}, fmt.Errorf("workspace %s is not a directory", abs)
	}
	ws := Workspace{Path: abs}
	if common := gitCommonDir(abs); common != "" {
		ws.GitRoot = filepath.Dir(common)
		ws.Key = slug(filepath.Base(ws.GitRoot)) + "-" + shortHash(common)
		return ws, nil
	}
	ws.Key = slug(filepath.Base(abs)) + "-" + shortHash(abs)
	return ws, nil
}

// gitCommonDir returns the absolute git common dir for dir, or "" when dir is
// not inside a repository or git is unavailable. Worktrees share a common dir,
// which is exactly why it is the identity.
func gitCommonDir(dir string) string {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--git-common-dir")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(string(out))
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	return filepath.Clean(abs)
}

func shortHash(s string) string {
	// Normalise separators so the same repo hashes the same from bash and cmd.
	n := strings.ToLower(filepath.ToSlash(s))
	sum := sha256.Sum256([]byte(n))
	return hex.EncodeToString(sum[:])[:8]
}

// slug lowercases, keeps [a-z0-9], collapses everything else to single
// hyphens, and trims hyphens.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) && r < 128 || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}
```

Also replace the placeholder `daemon/workspace/doc.go` comment so it describes only what exists: keep the file, it already says "resolves the working directory, computes the workspace key … tracks trust" — trust is P1b; leave the comment.

- [x] **Step 4: Run the tests**

Run: `go test ./daemon/workspace/ -count=1 -v`
Expected: three PASS lines (the git test may SKIP if git is missing; on this machine it must PASS).

- [x] **Step 5: Commit**

```bash
git add daemon/workspace/workspace.go daemon/workspace/workspace_test.go
git commit -m "workspace: resolve path and stable key from git common dir"
```

---

### Task 3: Session log — append, persist, reload

**Files:**
- Create: `daemon/session/session.go`
- Create: `daemon/session/store.go`
- Create: `daemon/session/session_test.go`

- [x] **Step 1: Write the failing tests**

`daemon/session/session_test.go`:

```go
package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/protocol"
)

var opts = protocol.Options{Model: "local/qwen", CompactionEnabled: true, PermissionMode: protocol.PermissionAsk}

func newStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestCreateWritesSessionEvent(t *testing.T) {
	st := newStore(t)
	s, err := st.Create("C:/w", "w-1", opts)
	if err != nil {
		t.Fatal(err)
	}
	ev := s.Events()
	if len(ev) != 1 || ev[0].Type != protocol.EventSession || ev[0].ParentID != nil {
		t.Fatalf("events: %+v", ev)
	}
	b, err := os.ReadFile(filepath.Join(st.root, s.ID()+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), "\n") != 1 {
		t.Fatalf("file should hold one line: %q", b)
	}
	path, key := s.Workspace()
	if path != "C:/w" || key != "w-1" {
		t.Fatalf("workspace: %s %s", path, key)
	}
}

func TestAppendChainsAndPersists(t *testing.T) {
	st := newStore(t)
	s, _ := st.Create("C:/w", "w-1", opts)
	e1, err := s.Append(protocol.EventMessage, protocol.MessageData{Role: "user", Content: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	e2, _ := s.Append(protocol.EventStateChange, protocol.StateChangeData{To: protocol.StateRunning, Reason: "prompt"})
	if *e2.ParentID != e1.ID || !(e1.ID < e2.ID) {
		t.Fatalf("chain/order: %s -> %s", e1.ID, e2.ID)
	}
	// Reload from disk through a fresh store.
	st2, _ := Open(filepath.Dir(st.root))
	r, err := st2.Get(s.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Events(); len(got) != 3 || got[2].ID != e2.ID {
		t.Fatalf("reloaded: %d events", len(got))
	}
	if r.State().State != protocol.StateRunning {
		t.Fatalf("state: %s", r.State().State)
	}
	// Appending to the reloaded session continues the chain.
	e3, err := r.Append(protocol.EventNotice, protocol.NoticeData{Source: "daemon", Level: "info", Message: "x"})
	if err != nil || *e3.ParentID != e2.ID {
		t.Fatalf("append after reload: %v %+v", err, e3)
	}
}

func TestAppendRejectsInvalidAndWritesNothing(t *testing.T) {
	st := newStore(t)
	s, _ := st.Create("C:/w", "w-1", opts)
	_, err := s.Append(protocol.EventMessage, protocol.MessageData{Role: "system", Content: "no"})
	if err == nil || !strings.Contains(err.Error(), `role "system" invalid`) {
		t.Fatalf("want validation error, got %v", err)
	}
	if len(s.Events()) != 1 {
		t.Fatal("invalid event must not be appended")
	}
	b, _ := os.ReadFile(filepath.Join(st.root, s.ID()+".jsonl"))
	if strings.Count(string(b), "\n") != 1 {
		t.Fatal("invalid event must not be written")
	}
}

func TestEventsAfterUnknownCursor(t *testing.T) {
	st := newStore(t)
	s, _ := st.Create("C:/w", "w-1", opts)
	bad := "01JEVENT000000000000000099"
	_, _, err := s.EventsAfter(&bad)
	var rpc *protocol.RPCError
	if !errors.As(err, &rpc) || rpc.Code != protocol.CodeCursorUnknown {
		t.Fatalf("want cursor_unknown, got %v", err)
	}
	last := s.Events()[0].ID
	got, synced, err := s.EventsAfter(&last)
	if err != nil || len(got) != 0 || !synced {
		t.Fatalf("at last: %v %d %v", err, len(got), synced)
	}
}

func TestGetUnknownAndCorrupt(t *testing.T) {
	st := newStore(t)
	if _, err := st.Get("01JEVENT000000000000000001"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := st.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-ULID id must be not found, got %v", err)
	}
	// A file whose chain is broken must fail loudly, not load silently.
	s, _ := st.Create("C:/w", "w-1", opts)
	s.Append(protocol.EventMessage, protocol.MessageData{Role: "user", Content: "hi"})
	path := filepath.Join(st.root, s.ID()+".jsonl")
	b, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	corrupt := strings.Replace(lines[1], `"parent_id":"`+s.Events()[0].ID+`"`, `"parent_id":"01JEVENT000000000000000077"`, 1)
	os.WriteFile(path, []byte(lines[0]+"\n"+corrupt+"\n"), 0o644)
	st2, _ := Open(filepath.Dir(st.root))
	if _, err := st2.Get(s.ID()); err == nil || !strings.Contains(err.Error(), "broken chain") {
		t.Fatalf("want broken chain, got %v", err)
	}
}
```

- [x] **Step 2: Run to see it fail**

Run: `go test ./daemon/session/`
Expected: compile errors (`undefined: Open`, `ErrNotFound`, …).

- [x] **Step 3: Implement `session.go`**

```go
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

// Session is one append-only log: an in-memory slice mirrored to a JSONL file.
// Append is the only writer; every other method reads a snapshot.
type Session struct {
	id   string
	path string

	mu     sync.RWMutex
	f      *os.File
	events []protocol.Event
	closed bool

	subMu   sync.Mutex
	subs    map[int]chan protocol.Event
	nextSub int
}

// ErrClosed is returned by Append after Close.
var ErrClosed = errors.New("session closed")

// ID returns the session id (a ULID).
func (s *Session) ID() string { return s.id }

// Workspace returns the path and key recorded in the session event.
func (s *Session) Workspace() (path, key string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d := protocol.MustData[protocol.SessionData](s.events[0])
	return d.Workspace, d.WorkspaceKey
}

// Append stamps, validates, persists, and broadcasts one event. data is the
// protocol data struct for t (or any JSON-marshalable equivalent). Nothing is
// written unless the event validates against the spec.
func (s *Session) Append(t protocol.EventType, data any) (protocol.Event, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return protocol.Event{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return protocol.Event{}, ErrClosed
	}
	e := protocol.Event{
		ID:        protocol.NewULID(),
		Timestamp: time.Now().UTC().Truncate(time.Millisecond),
		Type:      t,
		Data:      raw,
	}
	if n := len(s.events); n > 0 {
		last := s.events[n-1]
		pid := last.ID
		e.ParentID = &pid
		// Ids must sort in log order even within one millisecond.
		for e.ID <= last.ID {
			lt, _ := protocol.ULIDTime(last.ID)
			e.ID = protocol.NewULIDAt(lt.Add(time.Millisecond))
		}
	} else if t != protocol.EventSession {
		return protocol.Event{}, fmt.Errorf("first event must be session, got %s", t)
	}
	if err := protocol.ValidateEvent(e); err != nil {
		return protocol.Event{}, err
	}
	line, err := json.Marshal(e)
	if err != nil {
		return protocol.Event{}, err
	}
	line = append(line, '\n')
	if _, err := s.f.Write(line); err != nil {
		return protocol.Event{}, fmt.Errorf("session %s: write: %w", s.id, err)
	}
	s.events = append(s.events, e)
	s.broadcast(e)
	return e, nil
}

// Events returns a copy of the whole log.
func (s *Session) Events() []protocol.Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]protocol.Event(nil), s.events...)
}

// EventsAfter implements the cursor contract (protocol spec §4).
func (s *Session) EventsAfter(last *string) ([]protocol.Event, bool, error) {
	return protocol.EventsAfter(s.Events(), last)
}

// State returns the current projection.
func (s *Session) State() protocol.State {
	return protocol.Project(s.Events())
}

// Len returns the event count.
func (s *Session) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.events)
}

// Close releases the file. Further appends fail with ErrClosed.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.subMu.Lock()
	for id, ch := range s.subs {
		close(ch)
		delete(s.subs, id)
	}
	s.subMu.Unlock()
	return s.f.Close()
}

// broadcast is called with s.mu held. A subscriber whose buffer is full is
// dropped (its channel closed); it must resync from its cursor.
func (s *Session) broadcast(e protocol.Event) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for id, ch := range s.subs {
		select {
		case ch <- e:
		default:
			close(ch)
			delete(s.subs, id)
		}
	}
}
```

- [x] **Step 4: Implement `store.go`**

```go
package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

// ErrNotFound is returned for unknown session ids.
var ErrNotFound = errors.New("session not found")

// Store owns <root>/sessions/: one <id>.jsonl per session.
type Store struct {
	root string
	mu   sync.Mutex
	open map[string]*Session
}

// Open creates <root>/sessions if needed and returns a Store.
func Open(root string) (*Store, error) {
	dir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{root: dir, open: map[string]*Session{}}, nil
}

// Dir returns the sessions directory.
func (st *Store) Dir() string { return st.root }

// Create starts a new session whose first event records the workspace and
// options.
func (st *Store) Create(workspace, key string, opts protocol.Options) (*Session, error) {
	id := protocol.NewULID()
	path := filepath.Join(st.root, id+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	s := &Session{id: id, path: path, f: f, subs: map[int]chan protocol.Event{}}
	if _, err := s.Append(protocol.EventSession, protocol.SessionData{Workspace: workspace, WorkspaceKey: key, Options: opts}); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	st.mu.Lock()
	st.open[id] = s
	st.mu.Unlock()
	return s, nil
}

// Get returns an open session, loading and validating it from disk on first
// access. Unknown or non-ULID ids yield ErrNotFound; a corrupt log is an error.
func (st *Store) Get(id string) (*Session, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if s, ok := st.open[id]; ok {
		return s, nil
	}
	if protocol.ValidateULID(id) != nil {
		return nil, ErrNotFound
	}
	s, err := load(id, filepath.Join(st.root, id+".jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	st.open[id] = s
	return s, nil
}

func load(id, path string) (*Session, error) {
	rf, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	var events []protocol.Event
	sc := bufio.NewScanner(rf)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e protocol.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			rf.Close()
			return nil, fmt.Errorf("session %s: line %d: %w", id, lineNo, err)
		}
		events = append(events, e)
	}
	rf.Close()
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("session %s: %w", id, err)
	}
	if err := protocol.ValidateLog(events); err != nil {
		return nil, fmt.Errorf("session %s: %w", id, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &Session{id: id, path: path, f: f, events: events, subs: map[int]chan protocol.Event{}}, nil
}

// Summary is what nabu.session.list returns per session.
type Summary struct {
	SessionID    string                `json:"session_id"`
	Workspace    string                `json:"workspace"`
	WorkspaceKey string                `json:"workspace_key"`
	State        protocol.SessionState `json:"state"`
	EventCount   int                   `json:"event_count"`
	CreatedAt    time.Time             `json:"created_at"`
	UpdatedAt    time.Time             `json:"updated_at"`
	Goal         *protocol.GoalData    `json:"goal,omitempty"`
	TasksTotal   int                   `json:"tasks_total"`
	TasksDone    int                   `json:"tasks_done"`
}

// Summary projects the session for listings.
func (s *Session) Summary() Summary {
	ev := s.Events()
	st := protocol.Project(ev)
	path, key := s.Workspace()
	return Summary{
		SessionID: s.id, Workspace: path, WorkspaceKey: key, State: st.State,
		EventCount: len(ev), CreatedAt: ev[0].Timestamp, UpdatedAt: ev[len(ev)-1].Timestamp,
		Goal: st.Goal, TasksTotal: len(st.Tasks), TasksDone: st.DoneTasks(),
	}
}

// List returns every session on disk, oldest first. Corrupt logs are skipped
// and reported together in the returned error so one bad file cannot hide the
// rest.
func (st *Store) List() ([]Summary, error) {
	entries, err := os.ReadDir(st.root)
	if err != nil {
		return nil, err
	}
	var out []Summary
	var errs []error
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		s, err := st.Get(strings.TrimSuffix(name, ".jsonl"))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, s.Summary())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out, errors.Join(errs...)
}

// RecoverInterrupted pauses every session that was running when the daemon
// last stopped (spec §8 graceful restart) and returns their ids.
func (st *Store) RecoverInterrupted() ([]string, error) {
	sums, err := st.List()
	var paused []string
	for _, sm := range sums {
		if sm.State != protocol.StateRunning {
			continue
		}
		s, gerr := st.Get(sm.SessionID)
		if gerr != nil {
			err = errors.Join(err, gerr)
			continue
		}
		if _, aerr := s.Append(protocol.EventNotice, protocol.NoticeData{Source: "daemon", Level: "warn", Message: "session was running when the daemon stopped; paused"}); aerr != nil {
			err = errors.Join(err, aerr)
			continue
		}
		from := protocol.StateRunning
		if _, aerr := s.Append(protocol.EventStateChange, protocol.StateChangeData{From: &from, To: protocol.StatePaused, Reason: "daemon_restart"}); aerr != nil {
			err = errors.Join(err, aerr)
			continue
		}
		paused = append(paused, sm.SessionID)
	}
	return paused, err
}
```



- [x] **Step 5: Run the tests**

Run: `go test ./daemon/session/ -count=1 -v`
Expected: five PASS.

- [x] **Step 6: Commit**

```bash
git add daemon/session/session.go daemon/session/store.go daemon/session/session_test.go
git commit -m "session: append-only JSONL store with validation and reload"
```

---

### Task 4: Session subscriptions, listing, restart recovery

**Files:**
- Modify: `daemon/session/session.go` (add `Subscribe`)
- Create: `daemon/session/store_test.go`

- [x] **Step 1: Write the failing tests**

`daemon/session/store_test.go`:

```go
package session

import (
	"testing"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

func TestSubscribeReceivesAppends(t *testing.T) {
	st := newStore(t)
	s, _ := st.Create("C:/w", "w-1", opts)
	ch, cancel := s.Subscribe(8)
	defer cancel()
	e, _ := s.Append(protocol.EventMessage, protocol.MessageData{Role: "user", Content: "hi"})
	select {
	case got := <-ch:
		if got.ID != e.ID {
			t.Fatalf("got %s want %s", got.ID, e.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("no event delivered")
	}
}

func TestSlowSubscriberIsDropped(t *testing.T) {
	st := newStore(t)
	s, _ := st.Create("C:/w", "w-1", opts)
	ch, cancel := s.Subscribe(1)
	defer cancel()
	s.Append(protocol.EventMessage, protocol.MessageData{Role: "user", Content: "1"})
	s.Append(protocol.EventMessage, protocol.MessageData{Role: "user", Content: "2"}) // overflows
	<-ch // the first event
	if _, ok := <-ch; ok {
		t.Fatal("overflowed subscriber must be closed")
	}
}

func TestListAndRecover(t *testing.T) {
	st := newStore(t)
	a, _ := st.Create("C:/a", "a-1", opts)
	b, _ := st.Create("C:/b", "b-1", opts)
	b.Append(protocol.EventMessage, protocol.MessageData{Role: "user", Content: "go"})
	b.Append(protocol.EventStateChange, protocol.StateChangeData{To: protocol.StateRunning, Reason: "prompt"})
	b.Append(protocol.EventGoal, protocol.GoalData{Condition: "tests pass", State: "set", Source: "client"})

	sums, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 2 || sums[0].SessionID != a.ID() || sums[1].SessionID != b.ID() {
		t.Fatalf("list: %+v", sums)
	}
	if sums[1].State != protocol.StateRunning || sums[1].EventCount != 4 || sums[1].Goal == nil {
		t.Fatalf("summary: %+v", sums[1])
	}

	paused, err := st.RecoverInterrupted()
	if err != nil {
		t.Fatal(err)
	}
	if len(paused) != 1 || paused[0] != b.ID() {
		t.Fatalf("paused: %v", paused)
	}
	if b.State().State != protocol.StatePaused {
		t.Fatalf("state: %s", b.State().State)
	}
	if a.State().State != protocol.StateIdle {
		t.Fatal("idle session must be untouched")
	}
	ev := b.Events()
	if ev[len(ev)-2].Type != protocol.EventNotice {
		t.Fatal("recovery must append a notice before the state change")
	}
}
```

- [x] **Step 2: Run to see it fail**

Run: `go test ./daemon/session/ -run 'Subscribe|Slow|ListAndRecover'`
Expected: `undefined: (*Session).Subscribe` compile error.

- [x] **Step 3: Add `Subscribe`**

Append to `daemon/session/session.go`:

```go
// Subscribe returns a channel that receives every event appended from now on.
// buf is the channel capacity; a subscriber that falls behind is closed and
// must resync via EventsAfter. cancel removes the subscription.
func (s *Session) Subscribe(buf int) (<-chan protocol.Event, func()) {
	if buf < 1 {
		buf = 1
	}
	ch := make(chan protocol.Event, buf)
	s.subMu.Lock()
	id := s.nextSub
	s.nextSub++
	s.subs[id] = ch
	s.subMu.Unlock()
	cancel := func() {
		s.subMu.Lock()
		if c, ok := s.subs[id]; ok {
			delete(s.subs, id)
			close(c)
		}
		s.subMu.Unlock()
	}
	return ch, cancel
}
```

- [x] **Step 4: Run the package tests**

Run: `go test ./daemon/session/ -count=1`
Expected: `ok`.

- [x] **Step 5: Commit**

```bash
git add daemon/session/session.go daemon/session/store_test.go
git commit -m "session: subscriptions, listing, restart recovery"
```

---

### Task 5: Provider types, fake provider, registry

**Files:**
- Create: `daemon/provider/provider.go`
- Create: `daemon/provider/fake.go`
- Create: `daemon/provider/registry.go`
- Create: `daemon/provider/provider_test.go`

- [x] **Step 1: Write the failing tests**

`daemon/provider/provider_test.go`:

```go
package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/protocol"
)

func TestFakePopsScriptInOrder(t *testing.T) {
	f := &Fake{Script: []Response{
		{Content: "first"},
		{ToolCalls: []ToolCall{{ID: "c1", Name: "bash", Arguments: []byte(`{"command":"ls"}`)}}},
	}}
	var deltas []string
	r1, err := f.Complete(context.Background(), Request{Model: "m"}, func(s string) { deltas = append(deltas, s) })
	if err != nil || r1.Content != "first" || len(deltas) != 1 {
		t.Fatalf("r1: %+v %v deltas=%v", r1, err, deltas)
	}
	r2, _ := f.Complete(context.Background(), Request{Model: "m"}, nil)
	if len(r2.ToolCalls) != 1 || r2.ToolCalls[0].Name != "bash" {
		t.Fatalf("r2: %+v", r2)
	}
	if _, err := f.Complete(context.Background(), Request{}, nil); err == nil || !strings.Contains(err.Error(), "no scripted response") {
		t.Fatalf("exhausted: %v", err)
	}
	if len(f.Calls) != 3 {
		t.Fatalf("calls recorded: %d", len(f.Calls))
	}
	if r1.Usage.InputTokens == 0 {
		t.Fatal("fake must report non-zero usage by default")
	}
}

func TestFakeBlocksUntilReleasedOrCancelled(t *testing.T) {
	f := &Fake{Script: []Response{{Content: "x"}}, BlockOn: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Complete(ctx, Request{}, nil); err != context.Canceled {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestRegistryResolve(t *testing.T) {
	r := NewRegistry()
	local := &Fake{}
	hosted := &Fake{}
	r.Add(Config{Name: "local", ContextWindow: 32768}, local, true)
	r.Add(Config{Name: "openrouter"}, hosted, false)

	p, model, cfg, err := r.Resolve("qwen3.6-35b-a3b")
	if err != nil || p != local || model != "qwen3.6-35b-a3b" || cfg.ContextWindow != 32768 {
		t.Fatalf("bare model: %v %v %q", p, err, model)
	}
	p, model, _, err = r.Resolve("openrouter/deepseek/deepseek-v4")
	if err != nil || p != hosted || model != "deepseek/deepseek-v4" {
		t.Fatalf("prefixed: %v %v %q", p, err, model)
	}
	p, model, _, err = r.Resolve("unknown/thing")
	if err != nil || p != local || model != "unknown/thing" {
		t.Fatalf("unknown prefix falls through to default: %v %v %q", p, err, model)
	}
	if _, _, _, err := NewRegistry().Resolve("m"); err == nil {
		t.Fatal("no default provider must be an error")
	}
	_ = protocol.Usage{}
}
```

- [x] **Step 2: Run to see it fail**

Run: `go test ./daemon/provider/`
Expected: compile errors (`undefined: Fake`, `NewRegistry`, …).

- [x] **Step 3: Implement `provider.go`**

```go
package provider

import (
	"context"
	"encoding/json"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

// Message is one chat message in provider-neutral form.
type Message struct {
	Role       string     // "system" | "user" | "assistant" | "tool"
	Content    string
	ToolCalls  []ToolCall // assistant messages only
	ToolCallID string     // tool messages only
}

// ToolCall is a model-requested tool invocation. Arguments is always a JSON
// object; malformed model output is wrapped as {"_malformed": "<raw>"} so the
// tool fails visibly instead of the loop crashing.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// ToolSpec describes a tool to the model.
type ToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema object
}

// Request is one model round trip.
type Request struct {
	Model     string
	Messages  []Message
	Tools     []ToolSpec
	MaxTokens int // 0 = provider default
}

// Response is the completed assistant turn.
type Response struct {
	Content      string
	ToolCalls    []ToolCall
	Usage        protocol.Usage
	FinishReason string // "stop" | "tool_calls" | "length" | provider-specific
}

// Provider streams one completion. onDelta (may be nil) receives content text
// as it arrives; the returned Response holds the full content.
type Provider interface {
	Complete(ctx context.Context, req Request, onDelta func(string)) (Response, error)
}

// Config describes one configured provider (spec §4, §8 configuration).
type Config struct {
	Name          string
	BaseURL       string // e.g. http://localhost:8033/v1
	APIKey        string
	MaxInFlight   int           // 0 = 1
	ContextWindow int           // tokens; 0 = unknown (compaction disabled by size)
	Timeout       time.Duration // per attempt; 0 = 10 minutes
	Retries       int           // retries after the first attempt; default 2
}

func (c Config) withDefaults() Config {
	if c.MaxInFlight < 1 {
		c.MaxInFlight = 1
	}
	if c.Timeout == 0 {
		c.Timeout = 10 * time.Minute
	}
	if c.Retries == 0 {
		c.Retries = 2
	}
	return c
}
```

- [x] **Step 4: Implement `fake.go`**

```go
package provider

import (
	"context"
	"fmt"
	"sync"

	"github.com/corporealshift/nabu/protocol"
)

// Fake is a scripted Provider for tests. Each Complete pops the next Response
// (or the error scheduled for that call index), records the Request, and
// streams Content through onDelta in one piece.
type Fake struct {
	Script  []Response
	Errors  map[int]error   // by zero-based call index
	BlockOn chan struct{}   // when non-nil, Complete waits for close or ctx
	mu      sync.Mutex
	Calls   []Request
}

// Complete implements Provider.
func (f *Fake) Complete(ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	f.mu.Lock()
	i := len(f.Calls)
	f.Calls = append(f.Calls, req)
	f.mu.Unlock()
	if f.BlockOn != nil {
		select {
		case <-f.BlockOn:
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	if err := f.Errors[i]; err != nil {
		return Response{}, err
	}
	if i >= len(f.Script) {
		return Response{}, fmt.Errorf("fake provider: no scripted response for call %d", i+1)
	}
	r := f.Script[i]
	if onDelta != nil && r.Content != "" {
		onDelta(r.Content)
	}
	if r.Usage == (protocol.Usage{}) {
		r.Usage = protocol.Usage{InputTokens: 100, OutputTokens: 10}
	}
	if r.FinishReason == "" {
		if len(r.ToolCalls) > 0 {
			r.FinishReason = "tool_calls"
		} else {
			r.FinishReason = "stop"
		}
	}
	return r, nil
}

// CallCount returns how many times Complete ran.
func (f *Fake) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Calls)
}
```

- [x] **Step 5: Implement `registry.go`**

```go
package provider

import (
	"fmt"
	"strings"
)

// Registry maps configured provider names to Providers and resolves a
// session's model string: "name/model" selects a provider by name; a bare
// model, or an unknown prefix, goes to the default provider unchanged.
type Registry struct {
	def       string
	providers map[string]Provider
	cfgs      map[string]Config
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{providers: map[string]Provider{}, cfgs: map[string]Config{}}
}

// Add registers a provider. The first added, or any added with isDefault,
// becomes the default.
func (r *Registry) Add(cfg Config, p Provider, isDefault bool) {
	r.providers[cfg.Name] = p
	r.cfgs[cfg.Name] = cfg.withDefaults()
	if isDefault || r.def == "" {
		r.def = cfg.Name
	}
}

// Resolve returns the provider, the model name to send it, and its config.
func (r *Registry) Resolve(model string) (Provider, string, Config, error) {
	if name, rest, ok := strings.Cut(model, "/"); ok {
		if p, found := r.providers[name]; found {
			return p, rest, r.cfgs[name], nil
		}
	}
	if r.def == "" {
		return nil, "", Config{}, fmt.Errorf("no providers configured")
	}
	return r.providers[r.def], model, r.cfgs[r.def], nil
}

// Names lists configured provider names.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.providers))
	for n := range r.providers {
		out = append(out, n)
	}
	return out
}
```

- [x] **Step 6: Run the tests**

Run: `go test ./daemon/provider/ -count=1`
Expected: `ok`.

- [x] **Step 7: Commit**

```bash
git add daemon/provider/provider.go daemon/provider/fake.go daemon/provider/registry.go daemon/provider/provider_test.go
git commit -m "provider: request/response types, scripted fake, registry"
```

---

### Task 6: OpenAI-compatible streaming client

**Files:**
- Create: `daemon/provider/openai.go`
- Create: `daemon/provider/openai_test.go`

- [x] **Step 1: Write the failing test**

`daemon/provider/openai_test.go`:

```go
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sse writes one SSE data line.
func sse(w http.ResponseWriter, v string) {
	fmt.Fprintf(w, "data: %s\n\n", v)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func TestOpenAIStreamsContentToolCallsAndUsage(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("auth header: %q", r.Header.Get("Authorization"))
		}
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, `{"choices":[{"delta":{"role":"assistant","content":"Hel"}}]}`)
		sse(w, `{"choices":[{"delta":{"content":"lo"}}]}`)
		sse(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"bash","arguments":"{\"comm"}}]}}]}`)
		sse(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"and\":\"ls\"}"}}]}}]}`)
		sse(w, `{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_2","function":{"name":"read","arguments":"not json"}}]}}]}`)
		sse(w, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
		sse(w, `{"choices":[],"usage":{"prompt_tokens":120,"completion_tokens":9,"prompt_tokens_details":{"cached_tokens":100}}}`)
		sse(w, "[DONE]")
	}))
	defer srv.Close()

	p := NewOpenAI(Config{Name: "t", BaseURL: srv.URL + "/v1", APIKey: "sk-test"}, srv.Client())
	var deltas []string
	resp, err := p.Complete(context.Background(), Request{
		Model: "m",
		Messages: []Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "c0", Name: "bash", Arguments: []byte(`{"command":"pwd"}`)}}},
			{Role: "tool", ToolCallID: "c0", Content: "/w"},
		},
		Tools:     []ToolSpec{{Name: "bash", Description: "run", Parameters: []byte(`{"type":"object"}`)}},
		MaxTokens: 256,
	}, func(s string) { deltas = append(deltas, s) })
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "Hello" || strings.Join(deltas, "|") != "Hel|lo" {
		t.Fatalf("content: %q deltas: %v", resp.Content, deltas)
	}
	if len(resp.ToolCalls) != 2 || resp.ToolCalls[0].ID != "call_1" || string(resp.ToolCalls[0].Arguments) != `{"command":"ls"}` {
		t.Fatalf("tool calls: %+v", resp.ToolCalls)
	}
	if !strings.Contains(string(resp.ToolCalls[1].Arguments), `"_malformed"`) {
		t.Fatalf("malformed args must be wrapped: %s", resp.ToolCalls[1].Arguments)
	}
	if resp.FinishReason != "tool_calls" || resp.Usage.InputTokens != 120 || resp.Usage.OutputTokens != 9 || resp.Usage.CachedTokens != 100 {
		t.Fatalf("finish/usage: %+v", resp)
	}
	// Request wire shape.
	if gotBody["stream"] != true || gotBody["max_tokens"].(float64) != 256 || gotBody["model"] != "m" {
		t.Fatalf("body: %v", gotBody)
	}
	msgs := gotBody["messages"].([]any)
	asst := msgs[2].(map[string]any)
	tc := asst["tool_calls"].([]any)[0].(map[string]any)
	if tc["type"] != "function" || tc["function"].(map[string]any)["arguments"] != `{"command":"pwd"}` {
		t.Fatalf("assistant tool_calls wire shape: %v", asst)
	}
	tool := msgs[3].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "c0" {
		t.Fatalf("tool message: %v", tool)
	}
	tools := gotBody["tools"].([]any)[0].(map[string]any)
	if tools["type"] != "function" || tools["function"].(map[string]any)["name"] != "bash" {
		t.Fatalf("tools: %v", tools)
	}
}

func TestOpenAINonStreamErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"message":"bad tool schema"}}`)
	}))
	defer srv.Close()
	p := NewOpenAI(Config{Name: "t", BaseURL: srv.URL + "/v1", Retries: -1}, srv.Client())
	_, err := p.Complete(context.Background(), Request{Model: "m"}, nil)
	if err == nil || !strings.Contains(err.Error(), "bad tool schema") || !strings.Contains(err.Error(), "400") {
		t.Fatalf("err: %v", err)
	}
}

func TestOpenAIErrorEventMidStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, `{"choices":[{"delta":{"content":"par"}}]}`)
		sse(w, `{"error":{"message":"context length exceeded"}}`)
	}))
	defer srv.Close()
	p := NewOpenAI(Config{Name: "t", BaseURL: srv.URL + "/v1"}, srv.Client())
	_, err := p.Complete(context.Background(), Request{Model: "m"}, nil)
	if err == nil || !strings.Contains(err.Error(), "context length exceeded") {
		t.Fatalf("err: %v", err)
	}
}
```

- [x] **Step 2: Run to see it fail**

Run: `go test ./daemon/provider/ -run OpenAI`
Expected: `undefined: NewOpenAI`.

- [x] **Step 3: Implement `openai.go`**

```go
package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

// OpenAI speaks the OpenAI chat-completions streaming API, which llama.cpp,
// Ollama, vLLM, OpenRouter, Groq, DeepSeek and Together all implement.
type OpenAI struct {
	cfg Config
	hc  *http.Client
	sem chan struct{}
}

// NewOpenAI builds a client. hc may be nil.
func NewOpenAI(cfg Config, hc *http.Client) *OpenAI {
	cfg = cfg.withDefaults()
	if hc == nil {
		hc = &http.Client{}
	}
	return &OpenAI{cfg: cfg, hc: hc, sem: make(chan struct{}, cfg.MaxInFlight)}
}

// Config returns the effective configuration.
func (p *OpenAI) Config() Config { return p.cfg }

// httpError is a non-2xx response.
type httpError struct {
	Status int
	Body   string
}

func (e *httpError) Error() string { return fmt.Sprintf("provider returned %d: %s", e.Status, e.Body) }

func (e *httpError) retryable() bool {
	return e.Status == 429 || e.Status >= 500
}

// Complete implements Provider: acquire a slot, then attempt with retries.
// A failure after the first delta was delivered is never retried, because
// the caller has already seen partial output.
func (p *OpenAI) Complete(ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	select {
	case p.sem <- struct{}{}:
		defer func() { <-p.sem }()
	case <-ctx.Done():
		return Response{}, ctx.Err()
	}
	attempts := p.cfg.Retries + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		started := false
		wrapped := func(s string) {
			started = true
			if onDelta != nil {
				onDelta(s)
			}
		}
		resp, err := p.once(ctx, req, wrapped)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if ctx.Err() != nil || started || !isRetryable(err) || attempt == attempts-1 {
			break
		}
		backoff := time.Duration(1<<attempt) * time.Second
		if backoff > 8*time.Second {
			backoff = 8 * time.Second
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
	}
	return Response{}, lastErr
}

func isRetryable(err error) bool {
	var he *httpError
	if errors.As(err, &he) {
		return he.retryable()
	}
	// Connection-level failures before any byte arrived.
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

// ---- wire types -----------------------------------------------------------

type wireToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type wireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type wireRequest struct {
	Model         string        `json:"model"`
	Messages      []wireMessage `json:"messages"`
	Tools         []wireTool    `json:"tools,omitempty"`
	Stream        bool          `json:"stream"`
	StreamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
	MaxTokens int `json:"max_tokens,omitempty"`
}

type wireChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func toWire(req Request) wireRequest {
	w := wireRequest{Model: req.Model, Stream: true, MaxTokens: req.MaxTokens}
	w.StreamOptions.IncludeUsage = true
	for _, m := range req.Messages {
		wm := wireMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			var wtc wireToolCall
			wtc.ID, wtc.Type = tc.ID, "function"
			wtc.Function.Name = tc.Name
			wtc.Function.Arguments = string(tc.Arguments)
			if wtc.Function.Arguments == "" {
				wtc.Function.Arguments = "{}"
			}
			wm.ToolCalls = append(wm.ToolCalls, wtc)
		}
		w.Messages = append(w.Messages, wm)
	}
	for _, t := range req.Tools {
		var wt wireTool
		wt.Type = "function"
		wt.Function.Name, wt.Function.Description = t.Name, t.Description
		wt.Function.Parameters = t.Parameters
		if len(wt.Function.Parameters) == 0 {
			wt.Function.Parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		w.Tools = append(w.Tools, wt)
	}
	return w
}

// once performs a single streaming attempt.
func (p *OpenAI) once(ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()
	body, err := json.Marshal(toWire(req))
	if err != nil {
		return Response{}, err
	}
	url := strings.TrimRight(p.cfg.BaseURL, "/") + "/chat/completions"
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "text/event-stream")
	if p.cfg.APIKey != "" {
		hreq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}
	hresp, err := p.hc.Do(hreq)
	if err != nil {
		return Response{}, fmt.Errorf("provider %s: %w", p.cfg.Name, err)
	}
	defer hresp.Body.Close()
	if hresp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(hresp.Body, 4096))
		return Response{}, &httpError{Status: hresp.StatusCode, Body: strings.TrimSpace(string(b))}
	}
	return readStream(hresp.Body, onDelta)
}

type partialCall struct {
	id, name string
	args     strings.Builder
}

// readStream parses SSE chunks into a Response.
func readStream(r io.Reader, onDelta func(string)) (Response, error) {
	var resp Response
	var content strings.Builder
	var calls []*partialCall
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	done := false
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			done = true
			break
		}
		var ch wireChunk
		if err := json.Unmarshal([]byte(payload), &ch); err != nil {
			return Response{}, fmt.Errorf("provider stream: bad chunk: %w", err)
		}
		if ch.Error != nil {
			return Response{}, fmt.Errorf("provider error: %s", ch.Error.Message)
		}
		if ch.Usage != nil {
			resp.Usage = protocol.Usage{InputTokens: ch.Usage.PromptTokens, OutputTokens: ch.Usage.CompletionTokens}
			if ch.Usage.PromptTokensDetails != nil {
				resp.Usage.CachedTokens = ch.Usage.PromptTokensDetails.CachedTokens
			}
		}
		for _, c := range ch.Choices {
			if c.Delta.Content != "" {
				content.WriteString(c.Delta.Content)
				onDelta(c.Delta.Content)
			}
			for _, tc := range c.Delta.ToolCalls {
				for len(calls) <= tc.Index {
					calls = append(calls, &partialCall{})
				}
				pc := calls[tc.Index]
				if tc.ID != "" {
					pc.id = tc.ID
				}
				if tc.Function.Name != "" {
					pc.name += tc.Function.Name
				}
				pc.args.WriteString(tc.Function.Arguments)
			}
			if c.FinishReason != nil && *c.FinishReason != "" {
				resp.FinishReason = *c.FinishReason
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Response{}, fmt.Errorf("provider stream: %w", err)
	}
	if !done && resp.FinishReason == "" && content.Len() == 0 && len(calls) == 0 {
		return Response{}, errors.New("provider stream ended without data")
	}
	resp.Content = content.String()
	for i, pc := range calls {
		id := pc.id
		if id == "" {
			id = fmt.Sprintf("call_%d", i+1)
		}
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: id, Name: pc.name, Arguments: normalizeArgs(pc.args.String())})
	}
	if resp.FinishReason == "" {
		if len(resp.ToolCalls) > 0 {
			resp.FinishReason = "tool_calls"
		} else {
			resp.FinishReason = "stop"
		}
	}
	return resp, nil
}

// normalizeArgs guarantees a JSON object.
func normalizeArgs(s string) json.RawMessage {
	s = strings.TrimSpace(s)
	if s == "" {
		return json.RawMessage(`{}`)
	}
	if json.Valid([]byte(s)) && strings.HasPrefix(s, "{") {
		return json.RawMessage(s)
	}
	b, _ := json.Marshal(map[string]string{"_malformed": s})
	return b
}
```

- [x] **Step 4: Run the tests**

Run: `go test ./daemon/provider/ -count=1 -run OpenAI -v`
Expected: three PASS. Note `TestOpenAINonStreamErrorBody` sets `Retries: -1` so a 400 is not retried; the retry path is Task 7.

- [x] **Step 5: Commit**

```bash
git add daemon/provider/openai.go daemon/provider/openai_test.go
git commit -m "provider: openai-compatible streaming client with tool-call accumulation"
```

---

### Task 7: Retries and the in-flight limiter

**Files:**
- Create: `daemon/provider/limiter_test.go`

The retry loop and the semaphore already live in `openai.go` (Task 6). This task
proves them.

- [x] **Step 1: Write the tests**

`daemon/provider/limiter_test.go`:

```go
package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetriesOn503ThenSucceeds(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
		sse(w, "[DONE]")
	}))
	defer srv.Close()
	p := NewOpenAI(Config{Name: "t", BaseURL: srv.URL + "/v1", Retries: 1}, srv.Client())
	start := time.Now()
	resp, err := p.Complete(context.Background(), Request{Model: "m"}, nil)
	if err != nil || resp.Content != "ok" {
		t.Fatalf("resp: %+v err: %v", resp, err)
	}
	if atomic.LoadInt32(&n) != 2 {
		t.Fatalf("attempts: %d", n)
	}
	if time.Since(start) < time.Second {
		t.Fatal("expected a 1s backoff before the retry")
	}
}

func TestNoRetryAfterFirstDelta(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, `{"choices":[{"delta":{"content":"partial"}}]}`)
		sse(w, `{"error":{"message":"server died"}}`)
	}))
	defer srv.Close()
	p := NewOpenAI(Config{Name: "t", BaseURL: srv.URL + "/v1", Retries: 3}, srv.Client())
	_, err := p.Complete(context.Background(), Request{Model: "m"}, nil)
	if err == nil || atomic.LoadInt32(&n) != 1 {
		t.Fatalf("must not retry after output started: attempts=%d err=%v", n, err)
	}
}

func TestMaxInFlightSerialises(t *testing.T) {
	var inflight, peak int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&inflight, 1)
		for {
			old := atomic.LoadInt32(&peak)
			if cur <= old || atomic.CompareAndSwapInt32(&peak, old, cur) {
				break
			}
		}
		time.Sleep(80 * time.Millisecond)
		atomic.AddInt32(&inflight, -1)
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, `{"choices":[{"delta":{"content":"x"},"finish_reason":"stop"}]}`)
		sse(w, "[DONE]")
	}))
	defer srv.Close()
	p := NewOpenAI(Config{Name: "t", BaseURL: srv.URL + "/v1", MaxInFlight: 1}, srv.Client())
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Complete(context.Background(), Request{Model: "m"}, nil); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if peak != 1 {
		t.Fatalf("peak concurrency %d, want 1", peak)
	}
}
```

- [x] **Step 2: Run them**

Run: `go test ./daemon/provider/ -count=1 -v`
Expected: all PASS (the retry test takes about a second).

- [x] **Step 3: Commit**

```bash
git add daemon/provider/limiter_test.go
git commit -m "provider: prove retry-with-backoff and max_in_flight"
```

---

### Task 8: Built-in file tools (read, write, edit, glob, grep) as a module

**Files:**
- Create: `daemon/tools/builtins.go`
- Create: `daemon/tools/files.go`
- Create: `daemon/tools/files_test.go`

- [x] **Step 1: Write the failing tests**

`daemon/tools/files_test.go`:

```go
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// fakeSession gives tools a workspace and nothing else.
type fakeSession struct{ dir string }

func (f fakeSession) ID() string                                       { return "s" }
func (f fakeSession) Workspace() module.Workspace                      { return module.Workspace{Path: f.dir, Key: "k"} }
func (f fakeSession) Events(*string) ([]protocol.Event, error)         { return nil, nil }
func (f fakeSession) State() protocol.State                            { return protocol.State{} }
func (f fakeSession) Append(protocol.EventType, any) (protocol.Event, error) {
	return protocol.Event{}, nil
}

func run(t *testing.T, b *Builtins, s module.Session, name string, args string) (string, error) {
	t.Helper()
	for _, tool := range b.Tools() {
		if tool.Name == name {
			return tool.Run(context.Background(), s, json.RawMessage(args))
		}
	}
	t.Fatalf("no tool %q", name)
	return "", nil
}

func TestWriteReadEdit(t *testing.T) {
	dir := t.TempDir()
	s := fakeSession{dir}
	b := &Builtins{}
	out, err := run(t, b, s, "write", `{"path":"a/b.txt","content":"one\ntwo\nthree\n"}`)
	if err != nil || !strings.Contains(out, "a/b.txt") {
		t.Fatalf("write: %q %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a", "b.txt")); err != nil {
		t.Fatal("write must create parent dirs")
	}
	out, err = run(t, b, s, "read", `{"path":"a/b.txt"}`)
	if err != nil || out != "1\tone\n2\ttwo\n3\tthree\n" {
		t.Fatalf("read: %q %v", out, err)
	}
	out, _ = run(t, b, s, "read", `{"path":"a/b.txt","offset":2,"limit":1}`)
	if out != "2\ttwo\n" {
		t.Fatalf("read window: %q", out)
	}
	if _, err := run(t, b, s, "read", `{"path":"missing.txt"}`); err == nil {
		t.Fatal("missing file must error")
	}
	out, err = run(t, b, s, "edit", `{"path":"a/b.txt","old":"two","new":"2"}`)
	if err != nil || !strings.Contains(out, "1 occurrence") {
		t.Fatalf("edit: %q %v", out, err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a", "b.txt"))
	if string(got) != "one\n2\nthree\n" {
		t.Fatalf("after edit: %q", got)
	}
	if _, err := run(t, b, s, "edit", `{"path":"a/b.txt","old":"nope","new":"x"}`); err == nil {
		t.Fatal("edit of absent text must error")
	}
	os.WriteFile(filepath.Join(dir, "dup.txt"), []byte("x x"), 0o644)
	if _, err := run(t, b, s, "edit", `{"path":"dup.txt","old":"x","new":"y"}`); err == nil || !strings.Contains(err.Error(), "2 times") {
		t.Fatalf("ambiguous edit must error: %v", err)
	}
	out, _ = run(t, b, s, "edit", `{"path":"dup.txt","old":"x","new":"y","replace_all":true}`)
	if !strings.Contains(out, "2 occurrence") {
		t.Fatalf("replace_all: %q", out)
	}
}

func TestGlobAndGrep(t *testing.T) {
	dir := t.TempDir()
	s := fakeSession{dir}
	b := &Builtins{}
	os.MkdirAll(filepath.Join(dir, "src", "deep"), 0o755)
	os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "a.go"), []byte("package a\nfunc Foo() {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "src", "deep", "b.go"), []byte("package deep\n// foo bar\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "src", "c.txt"), []byte("foo\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".git", "x.go"), []byte("ignored"), 0o644)
	os.WriteFile(filepath.Join(dir, "bin.dat"), append([]byte("foo"), 0, 1, 2), 0o644)

	out, err := run(t, b, s, "glob", `{"pattern":"**/*.go"}`)
	if err != nil || out != "src/a.go\nsrc/deep/b.go" {
		t.Fatalf("glob: %q %v", out, err)
	}
	out, _ = run(t, b, s, "glob", `{"pattern":"*.go","path":"src"}`)
	if out != "src/a.go" {
		t.Fatalf("glob in subdir: %q", out)
	}
	out, err = run(t, b, s, "grep", `{"pattern":"foo","ignore_case":true}`)
	if err != nil {
		t.Fatal(err)
	}
	want := "src/a.go:2: func Foo() {}\nsrc/c.txt:1: foo\nsrc/deep/b.go:2: // foo bar"
	if out != want {
		t.Fatalf("grep:\n%s\nwant:\n%s", out, want)
	}
	out, _ = run(t, b, s, "grep", `{"pattern":"foo","glob":"*.go"}`)
	if out != "src/deep/b.go:2: // foo bar" {
		t.Fatalf("grep with glob: %q", out)
	}
	out, _ = run(t, b, s, "grep", `{"pattern":"zzz"}`)
	if out != "no matches" {
		t.Fatalf("no matches: %q", out)
	}
}

func TestGlobToRegexp(t *testing.T) {
	cases := map[string][]string{
		"**/*.go":     {"a.go", "x/y/z.go"},
		"src/*.go":    {"src/a.go"},
		"*.txt":       {"a.txt"},
		"src/**":      {"src/a", "src/x/y"},
		"file?.md":    {"file1.md"},
	}
	negatives := map[string][]string{
		"src/*.go": {"src/x/a.go", "a.go"},
		"*.txt":    {"x/a.txt"},
	}
	for pat, oks := range cases {
		re := globToRegexp(pat)
		for _, p := range oks {
			if !re.MatchString(p) {
				t.Errorf("%q should match %q (%s)", pat, p, re)
			}
		}
	}
	for pat, bads := range negatives {
		re := globToRegexp(pat)
		for _, p := range bads {
			if re.MatchString(p) {
				t.Errorf("%q should not match %q", pat, p)
			}
		}
	}
}
```

- [x] **Step 2: Run to see it fail**

Run: `go test ./daemon/tools/`
Expected: compile errors (`undefined: Builtins`, `globToRegexp`).

- [x] **Step 3: Implement `builtins.go`**

```go
package tools

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
)

// Builtins is the module that provides nabu's built-in tools. It registers
// through module.ToolProvider like any other module: there is no privileged
// path (spec §14.5).
type Builtins struct {
	// Tasks records task.update snapshots. nil disables the task tool.
	Tasks TaskStore
	// Shell runs bash commands; "" auto-detects (bash, sh, then cmd).
	Shell string
	// BashTimeout bounds a command unless the call overrides it. 0 = 120s.
	BashTimeout time.Duration
	// MaxOutput caps tool output bytes. 0 = 32 KiB.
	MaxOutput int
}

// Name implements module.Module.
func (b *Builtins) Name() string { return "builtins" }

// Init implements module.Module; reads shell, bash_timeout_seconds, max_output.
func (b *Builtins) Init(_ module.Host, cfg module.Config) error {
	if b.Shell == "" {
		b.Shell = cfg.String("shell", "")
	}
	if b.BashTimeout == 0 {
		b.BashTimeout = time.Duration(cfg.Int("bash_timeout_seconds", 120)) * time.Second
	}
	if b.MaxOutput == 0 {
		b.MaxOutput = cfg.Int("max_output", 32<<10)
	}
	return nil
}

// Tools implements module.ToolProvider.
func (b *Builtins) Tools() []module.Tool {
	tools := []module.Tool{b.readTool(), b.writeTool(), b.editTool(), b.globTool(), b.grepTool(), b.bashTool()}
	if b.Tasks != nil {
		tools = append(tools, b.taskTool())
	}
	return tools
}

func (b *Builtins) maxOutput() int {
	if b.MaxOutput > 0 {
		return b.MaxOutput
	}
	return 32 << 10
}

// decode unmarshals tool arguments leniently (unknown fields ignored).
func decode[T any](args json.RawMessage) (T, error) {
	var v T
	if len(args) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(args, &v); err != nil {
		return v, fmt.Errorf("invalid arguments: %w", err)
	}
	return v, nil
}

// resolve turns a tool path into an absolute path under the workspace unless
// it is already absolute.
func resolve(s module.Session, p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("path is required")
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	return filepath.Join(s.Workspace().Path, p), nil
}

// rel renders a path relative to the workspace with forward slashes.
func rel(s module.Session, abs string) string {
	if r, err := filepath.Rel(s.Workspace().Path, abs); err == nil {
		return filepath.ToSlash(r)
	}
	return filepath.ToSlash(abs)
}

// truncate keeps the head and tail of oversized output with a marker.
func truncate(out string, max int) string {
	if len(out) <= max {
		return out
	}
	head := max * 3 / 4
	tail := max - head
	return out[:head] + fmt.Sprintf("\n[... %d bytes truncated ...]\n", len(out)-max) + out[len(out)-tail:]
}

func schema(s string) json.RawMessage { return json.RawMessage(s) }
```

- [x] **Step 4: Implement `files.go`**

```go
package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/corporealshift/nabu/daemon/module"
)

// skipDir names directories never walked by glob and grep.
var skipDir = map[string]bool{".git": true, "node_modules": true}

func (b *Builtins) readTool() module.Tool {
	type args struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	return module.Tool{
		Name:        "read",
		Description: "Read a text file. Returns numbered lines. offset is the 1-based first line, limit the number of lines (default 2000).",
		Schema: schema(`{"type":"object","required":["path"],"properties":{
			"path":{"type":"string"},"offset":{"type":"integer"},"limit":{"type":"integer"}}}`),
		Run: func(ctx context.Context, s module.Session, raw json.RawMessage) (string, error) {
			a, err := decode[args](raw)
			if err != nil {
				return "", err
			}
			p, err := resolve(s, a.Path)
			if err != nil {
				return "", err
			}
			f, err := os.Open(p)
			if err != nil {
				return "", err
			}
			defer f.Close()
			if a.Offset < 1 {
				a.Offset = 1
			}
			if a.Limit < 1 {
				a.Limit = 2000
			}
			var sb strings.Builder
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
			n := 0
			shown := 0
			for sc.Scan() {
				n++
				if n < a.Offset {
					continue
				}
				if shown >= a.Limit {
					fmt.Fprintf(&sb, "[... more lines; continue with offset %d ...]\n", n)
					break
				}
				fmt.Fprintf(&sb, "%d\t%s\n", n, sc.Text())
				shown++
			}
			if err := sc.Err(); err != nil {
				return "", err
			}
			if shown == 0 && n == 0 {
				return "(empty file)", nil
			}
			return truncate(sb.String(), b.maxOutput()), nil
		},
	}
}

func (b *Builtins) writeTool() module.Tool {
	type args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	return module.Tool{
		Name:        "write",
		Description: "Create or overwrite a file with the given content, creating parent directories.",
		Schema: schema(`{"type":"object","required":["path","content"],"properties":{
			"path":{"type":"string"},"content":{"type":"string"}}}`),
		Run: func(ctx context.Context, s module.Session, raw json.RawMessage) (string, error) {
			a, err := decode[args](raw)
			if err != nil {
				return "", err
			}
			p, err := resolve(s, a.Path)
			if err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(p, []byte(a.Content), 0o644); err != nil {
				return "", err
			}
			return fmt.Sprintf("wrote %d bytes to %s", len(a.Content), rel(s, p)), nil
		},
	}
}

func (b *Builtins) editTool() module.Tool {
	type args struct {
		Path       string `json:"path"`
		Old        string `json:"old"`
		New        string `json:"new"`
		ReplaceAll bool   `json:"replace_all"`
	}
	return module.Tool{
		Name:        "edit",
		Description: "Replace text in a file. old must occur exactly once unless replace_all is true.",
		Schema: schema(`{"type":"object","required":["path","old","new"],"properties":{
			"path":{"type":"string"},"old":{"type":"string"},"new":{"type":"string"},"replace_all":{"type":"boolean"}}}`),
		Run: func(ctx context.Context, s module.Session, raw json.RawMessage) (string, error) {
			a, err := decode[args](raw)
			if err != nil {
				return "", err
			}
			if a.Old == "" {
				return "", fmt.Errorf("old must not be empty")
			}
			p, err := resolve(s, a.Path)
			if err != nil {
				return "", err
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return "", err
			}
			text := string(data)
			n := strings.Count(text, a.Old)
			switch {
			case n == 0:
				return "", fmt.Errorf("old text not found in %s", rel(s, p))
			case n > 1 && !a.ReplaceAll:
				return "", fmt.Errorf("old text occurs %d times in %s; make it unique or set replace_all", n, rel(s, p))
			}
			if !a.ReplaceAll {
				n = 1
			}
			text = strings.Replace(text, a.Old, a.New, n)
			if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
				return "", err
			}
			return fmt.Sprintf("replaced %d occurrence(s) in %s", n, rel(s, p)), nil
		},
	}
}

func (b *Builtins) globTool() module.Tool {
	type args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	return module.Tool{
		Name:        "glob",
		Description: "Find files by glob pattern (supports **). path is the directory to search, default the workspace.",
		Schema: schema(`{"type":"object","required":["pattern"],"properties":{
			"pattern":{"type":"string"},"path":{"type":"string"}}}`),
		Run: func(ctx context.Context, s module.Session, raw json.RawMessage) (string, error) {
			a, err := decode[args](raw)
			if err != nil {
				return "", err
			}
			if a.Pattern == "" {
				return "", fmt.Errorf("pattern is required")
			}
			root := s.Workspace().Path
			if a.Path != "" {
				if root, err = resolve(s, a.Path); err != nil {
					return "", err
				}
			}
			re := globToRegexp(a.Pattern)
			var hits []string
			err = walk(ctx, root, func(abs string, d fs.DirEntry) error {
				r, _ := filepath.Rel(root, abs)
				if re.MatchString(filepath.ToSlash(r)) {
					hits = append(hits, rel(s, abs))
				}
				return nil
			})
			if err != nil {
				return "", err
			}
			sort.Strings(hits)
			if len(hits) > 500 {
				hits = append(hits[:500], fmt.Sprintf("[... %d more ...]", len(hits)-500))
			}
			if len(hits) == 0 {
				return "no matches", nil
			}
			return strings.Join(hits, "\n"), nil
		},
	}
}

func (b *Builtins) grepTool() module.Tool {
	type args struct {
		Pattern    string `json:"pattern"`
		Path       string `json:"path"`
		Glob       string `json:"glob"`
		IgnoreCase bool   `json:"ignore_case"`
		Max        int    `json:"max"`
	}
	return module.Tool{
		Name:        "grep",
		Description: "Search file contents with a Go regular expression. Output is path:line: text. glob filters file names; max caps matching lines (default 200).",
		Schema: schema(`{"type":"object","required":["pattern"],"properties":{
			"pattern":{"type":"string"},"path":{"type":"string"},"glob":{"type":"string"},
			"ignore_case":{"type":"boolean"},"max":{"type":"integer"}}}`),
		Run: func(ctx context.Context, s module.Session, raw json.RawMessage) (string, error) {
			a, err := decode[args](raw)
			if err != nil {
				return "", err
			}
			if a.Pattern == "" {
				return "", fmt.Errorf("pattern is required")
			}
			pat := a.Pattern
			if a.IgnoreCase {
				pat = "(?i)" + pat
			}
			re, err := regexp.Compile(pat)
			if err != nil {
				return "", fmt.Errorf("bad pattern: %w", err)
			}
			var nameRe *regexp.Regexp
			if a.Glob != "" {
				nameRe = globToRegexp(a.Glob)
			}
			if a.Max < 1 {
				a.Max = 200
			}
			root := s.Workspace().Path
			if a.Path != "" {
				if root, err = resolve(s, a.Path); err != nil {
					return "", err
				}
			}
			var lines []string
			err = walk(ctx, root, func(abs string, d fs.DirEntry) error {
				if nameRe != nil && !nameRe.MatchString(filepath.Base(abs)) && !nameRe.MatchString(filepath.ToSlash(mustRel(root, abs))) {
					return nil
				}
				data, err := os.ReadFile(abs)
				if err != nil || isBinary(data) {
					return nil
				}
				n := 0
				for _, line := range bytes.Split(data, []byte("\n")) {
					n++
					if re.Match(line) {
						lines = append(lines, fmt.Sprintf("%s:%d: %s", rel(s, abs), n, strings.TrimRight(string(line), "\r")))
						if len(lines) >= a.Max {
							return fs.SkipAll
						}
					}
				}
				return nil
			})
			if err != nil {
				return "", err
			}
			if len(lines) == 0 {
				return "no matches", nil
			}
			return truncate(strings.Join(lines, "\n"), b.maxOutput()), nil
		},
	}
}

func mustRel(root, abs string) string {
	r, err := filepath.Rel(root, abs)
	if err != nil {
		return abs
	}
	return r
}

// walk visits regular files under root in lexical order, skipping skipDir
// entries and honouring ctx.
func walk(ctx context.Context, root string, fn func(abs string, d fs.DirEntry) error) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if p != root && skipDir[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return fn(p, d)
	})
}

// isBinary reports a NUL byte in the first 512 bytes.
func isBinary(b []byte) bool {
	if len(b) > 512 {
		b = b[:512]
	}
	return bytes.IndexByte(b, 0) >= 0
}

// globToRegexp converts a glob with *, ?, ** into an anchored regexp over
// slash-separated relative paths.
func globToRegexp(glob string) *regexp.Regexp {
	var sb strings.Builder
	sb.WriteString("^")
	g := filepath.ToSlash(glob)
	for i := 0; i < len(g); i++ {
		c := g[i]
		switch {
		case c == '*' && i+1 < len(g) && g[i+1] == '*':
			i++
			if i+1 < len(g) && g[i+1] == '/' {
				i++
				sb.WriteString("(?:.*/)?")
			} else {
				sb.WriteString(".*")
			}
		case c == '*':
			sb.WriteString("[^/]*")
		case c == '?':
			sb.WriteString("[^/]")
		default:
			sb.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	sb.WriteString("$")
	return regexp.MustCompile(sb.String())
}
```

`bashTool` and `taskTool` do not exist yet; add temporary stubs at the bottom of
`builtins.go` so this task compiles, and remove them in Tasks 9 and 10:

```go
func (b *Builtins) bashTool() module.Tool { return module.Tool{Name: "bash"} }
func (b *Builtins) taskTool() module.Tool { return module.Tool{Name: "task.update"} }

// TaskStore is defined in Task 10; this placeholder keeps Task 8 compiling.
type TaskStore interface{}
```

- [x] **Step 5: Run the tests**

Run: `go test ./daemon/tools/ -count=1 -v`
Expected: `TestWriteReadEdit`, `TestGlobAndGrep`, `TestGlobToRegexp` PASS.

- [x] **Step 6: Commit**

```bash
git add daemon/tools/builtins.go daemon/tools/files.go daemon/tools/files_test.go
git commit -m "tools: read, write, edit, glob, grep as a module"
```

---

### Task 9: `bash` tool

**Files:**
- Create: `daemon/tools/bash.go`
- Create: `daemon/tools/bash_test.go`
- Modify: `daemon/tools/builtins.go` (delete the `bashTool` stub)

- [x] **Step 1: Write the failing tests**

`daemon/tools/bash_test.go`:

```go
package tools

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func requireShell(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
}

func TestBashRunsInWorkspaceAndReportsExit(t *testing.T) {
	requireShell(t)
	dir := t.TempDir()
	s := fakeSession{dir}
	b := &Builtins{}
	out, err := run(t, b, s, "bash", `{"command":"echo hello && pwd"}`)
	if err != nil {
		t.Fatalf("err: %v out: %q", err, out)
	}
	if !strings.HasPrefix(out, "hello\n") {
		t.Fatalf("out: %q", out)
	}
	out, err = run(t, b, s, "bash", `{"command":"echo oops >&2; exit 3"}`)
	if err == nil || !strings.Contains(out, "oops") || !strings.Contains(out, "[exit status 3]") {
		t.Fatalf("non-zero exit: err=%v out=%q", err, out)
	}
}

func TestBashTimeout(t *testing.T) {
	requireShell(t)
	b := &Builtins{BashTimeout: 300 * time.Millisecond}
	start := time.Now()
	out, err := run(t, b, fakeSession{t.TempDir()}, "bash", `{"command":"sleep 5"}`)
	if err == nil || !strings.Contains(out, "timed out") {
		t.Fatalf("want timeout, got err=%v out=%q", err, out)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("timeout not enforced promptly")
	}
}

func TestBashOutputTruncated(t *testing.T) {
	requireShell(t)
	b := &Builtins{MaxOutput: 200}
	out, _ := run(t, b, fakeSession{t.TempDir()}, "bash", `{"command":"seq 1 1000"}`)
	if len(out) > 300 || !strings.Contains(out, "truncated") {
		t.Fatalf("len=%d out=%q", len(out), out)
	}
}
```

- [x] **Step 2: Run to see it fail**

Run: `go test ./daemon/tools/ -run Bash`
Expected: FAIL — the stub tool has a nil `Run` (panic) or wrong output.

- [x] **Step 3: Implement `bash.go` and delete the stub**

`daemon/tools/bash.go`:

```go
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
)

// shellCommand returns the interpreter and the flag that takes a command.
func (b *Builtins) shellCommand() (string, string) {
	if b.Shell != "" {
		if b.Shell == "cmd" || b.Shell == "cmd.exe" {
			return b.Shell, "/C"
		}
		return b.Shell, "-c"
	}
	for _, sh := range []string{"bash", "sh"} {
		if p, err := exec.LookPath(sh); err == nil {
			return p, "-c"
		}
	}
	if runtime.GOOS == "windows" {
		return "cmd", "/C"
	}
	return "sh", "-c"
}

func (b *Builtins) bashTool() module.Tool {
	type args struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	return module.Tool{
		Name:        "bash",
		Description: "Run a shell command in the workspace. Returns combined stdout and stderr; a non-zero exit is reported as [exit status N]. timeout_seconds defaults to the configured limit.",
		Schema: schema(`{"type":"object","required":["command"],"properties":{
			"command":{"type":"string"},"timeout_seconds":{"type":"integer"}}}`),
		Run: func(ctx context.Context, s module.Session, raw json.RawMessage) (string, error) {
			a, err := decode[args](raw)
			if err != nil {
				return "", err
			}
			if a.Command == "" {
				return "", fmt.Errorf("command is required")
			}
			timeout := b.BashTimeout
			if timeout == 0 {
				timeout = 120 * time.Second
			}
			if a.TimeoutSeconds > 0 {
				timeout = time.Duration(a.TimeoutSeconds) * time.Second
			}
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			sh, flag := b.shellCommand()
			cmd := exec.CommandContext(cctx, sh, flag, a.Command)
			cmd.Dir = s.Workspace().Path
			cmd.WaitDelay = 5 * time.Second
			var buf bytes.Buffer
			cmd.Stdout = &buf
			cmd.Stderr = &buf
			runErr := cmd.Run()
			out := truncate(buf.String(), b.maxOutput())
			if cctx.Err() == context.DeadlineExceeded {
				out += fmt.Sprintf("\n[timed out after %s]", timeout)
				return out, fmt.Errorf("command timed out after %s", timeout)
			}
			var exitErr *exec.ExitError
			if errors.As(runErr, &exitErr) {
				out += fmt.Sprintf("\n[exit status %d]", exitErr.ExitCode())
				return out, fmt.Errorf("exit status %d", exitErr.ExitCode())
			}
			if runErr != nil {
				return out, runErr
			}
			return out, nil
		},
	}
}
```

Delete `func (b *Builtins) bashTool() module.Tool { return module.Tool{Name: "bash"} }` from `builtins.go`.

- [x] **Step 4: Run the tests**

Run: `go test ./daemon/tools/ -count=1 -run Bash -v`
Expected: three PASS.

- [x] **Step 5: Commit**

```bash
git add daemon/tools/bash.go daemon/tools/bash_test.go daemon/tools/builtins.go
git commit -m "tools: bash with timeout, exit status, and output cap"
```

---

### Task 10: `task.update` tool and the snapshot merge

**Files:**
- Create: `daemon/tools/tasks.go`
- Create: `daemon/tools/tasks_test.go`
- Modify: `daemon/tools/builtins.go` (delete the `taskTool` stub and the placeholder `TaskStore`)

- [x] **Step 1: Write the failing tests**

`daemon/tools/tasks_test.go`:

```go
package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

func ev(t protocol.EventType, data any) protocol.Event {
	b, _ := json.Marshal(data)
	return protocol.Event{ID: protocol.NewULID(), Type: t, Data: b}
}

func TestMergeAssignsIdsAndRevision(t *testing.T) {
	out, err := Merge(protocol.TasksData{}, []protocol.Task{
		{Title: "First", Status: protocol.TaskPending},
		{ID: "t7", Title: "Named", Status: protocol.TaskInProgress},
		{Title: "Third", Status: protocol.TaskPending},
	}, nil, "model")
	if err != nil {
		t.Fatal(err)
	}
	if out.Revision != 1 || out.Source != "model" {
		t.Fatalf("meta: %+v", out)
	}
	if out.Tasks[0].ID != "t8" || out.Tasks[1].ID != "t7" || out.Tasks[2].ID != "t9" {
		t.Fatalf("ids: %s %s %s", out.Tasks[0].ID, out.Tasks[1].ID, out.Tasks[2].ID)
	}
	for _, task := range out.Tasks {
		if task.BlockedBy == nil {
			t.Fatal("blocked_by must be non-nil")
		}
	}
}

func TestMergeRejectsBadInput(t *testing.T) {
	if _, err := Merge(protocol.TasksData{}, []protocol.Task{{Title: "x", Status: "doing"}}, nil, "model"); err == nil || !strings.Contains(err.Error(), "status") {
		t.Fatalf("bad status: %v", err)
	}
	if _, err := Merge(protocol.TasksData{}, []protocol.Task{{ID: "a", Title: "x", Status: "pending"}, {ID: "a", Title: "y", Status: "pending"}}, nil, "model"); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("dup: %v", err)
	}
	if _, err := Merge(protocol.TasksData{}, []protocol.Task{{Status: "pending"}}, nil, "model"); err == nil || !strings.Contains(err.Error(), "title") {
		t.Fatalf("title: %v", err)
	}
}

func TestMergeCarriesFieldsAndSetsEvidence(t *testing.T) {
	prev := protocol.TasksData{Revision: 3, Tasks: []protocol.Task{
		{ID: "t1", Title: "A", Status: protocol.TaskInProgress, DoneWhen: "tests pass", Check: "go test ./...", BlockedBy: []string{}},
		{ID: "t2", Title: "B", Status: protocol.TaskDone, BlockedBy: []string{}, Evidence: "01JEVENT000000000000000005"},
	}}
	pass := ev(protocol.EventCheck, protocol.CheckData{Name: "task:t1", Kind: "command", TaskID: "t1", Status: "pass", Summary: "exit 0"})
	log := []protocol.Event{
		ev(protocol.EventCheck, protocol.CheckData{Name: "task:t1", Kind: "command", TaskID: "t1", Status: "fail", Summary: "exit 1"}),
		pass,
	}
	out, err := Merge(prev, []protocol.Task{
		{ID: "t1", Title: "A", Status: protocol.TaskDone, Evidence: "01JEVENT000000000000000099"}, // model-supplied evidence ignored
		{ID: "t2", Title: "B", Status: protocol.TaskDone},
		{ID: "t3", Title: "C", Status: protocol.TaskPending, DoneWhen: "x"},
	}, log, "model")
	if err != nil {
		t.Fatal(err)
	}
	if out.Revision != 4 {
		t.Fatalf("revision: %d", out.Revision)
	}
	t1 := out.Tasks[0]
	if t1.DoneWhen != "tests pass" || t1.Check != "go test ./..." {
		t.Fatalf("fields must carry over when omitted: %+v", t1)
	}
	if t1.Evidence != pass.ID {
		t.Fatalf("evidence must be the latest passing check: %q", t1.Evidence)
	}
	if out.Tasks[1].Evidence != "01JEVENT000000000000000005" {
		t.Fatal("already-done task keeps its evidence")
	}
	if out.Tasks[2].Evidence != "" {
		t.Fatal("pending task has no evidence")
	}
}

type recordingStore struct{ got protocol.TasksData }

func (r *recordingStore) UpdateTasks(_ context.Context, _ module.Session, incoming []protocol.Task, source string) (protocol.TasksData, error) {
	d, err := Merge(protocol.TasksData{}, incoming, nil, source)
	r.got = d
	return d, err
}

func TestTaskUpdateToolRendersList(t *testing.T) {
	store := &recordingStore{}
	b := &Builtins{Tasks: store}
	out, err := run(t, b, fakeSession{t.TempDir()}, "task.update",
		`{"tasks":[{"title":"Write test","status":"in_progress","done_when":"test fails"},{"title":"Make pass","status":"pending"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "Tasks (0/2 done):\n- [>] t1 Write test\n- [ ] t2 Make pass" {
		t.Fatalf("out: %q", out)
	}
	if store.got.Source != "model" {
		t.Fatalf("source: %q", store.got.Source)
	}
	if _, err := run(t, b, fakeSession{t.TempDir()}, "task.update", `{"tasks":[]}`); err == nil {
		t.Fatal("empty list must be rejected: use cancelled/done statuses instead")
	}
	if len((&Builtins{}).Tools()) != 6 {
		t.Fatal("without a TaskStore the task tool must not be offered")
	}
}
```

- [x] **Step 2: Run to see it fail**

Run: `go test ./daemon/tools/ -run 'Merge|TaskUpdate'`
Expected: compile errors (`undefined: Merge`; `TaskStore` has no `UpdateTasks`).

- [x] **Step 3: Implement `tasks.go`; delete the stubs in `builtins.go`**

Remove from `builtins.go`:

```go
func (b *Builtins) taskTool() module.Tool { return module.Tool{Name: "task.update"} }

// TaskStore is defined in Task 10; this placeholder keeps Task 8 compiling.
type TaskStore interface{}
```

`daemon/tools/tasks.go`:

```go
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// TaskStore records a task snapshot for a session. The agent implements it:
// it runs Merge against the session's log and appends the `tasks` event.
type TaskStore interface {
	UpdateTasks(ctx context.Context, s module.Session, incoming []protocol.Task, source string) (protocol.TasksData, error)
}

// Merge applies a whole-list snapshot (spec §9.2): stable ids, ids assigned
// where missing, done_when/check/note carried over when omitted, revision
// incremented, and evidence set mechanically from the latest passing check
// event for the task. Model-supplied evidence is ignored.
func Merge(prev protocol.TasksData, incoming []protocol.Task, log []protocol.Event, source string) (protocol.TasksData, error) {
	if len(incoming) == 0 {
		return protocol.TasksData{}, fmt.Errorf("tasks must not be empty; mark tasks done or cancelled instead of removing them")
	}
	prevByID := map[string]protocol.Task{}
	next := 0
	for _, t := range prev.Tasks {
		prevByID[t.ID] = t
		next = maxSeq(next, t.ID)
	}
	seen := map[string]bool{}
	for _, t := range incoming {
		if t.ID != "" {
			if seen[t.ID] {
				return protocol.TasksData{}, fmt.Errorf("duplicate task id %q", t.ID)
			}
			seen[t.ID] = true
			next = maxSeq(next, t.ID)
		}
	}
	out := protocol.TasksData{Revision: prev.Revision + 1, Source: source}
	for _, t := range incoming {
		if strings.TrimSpace(t.Title) == "" {
			return protocol.TasksData{}, fmt.Errorf("every task needs a title")
		}
		switch t.Status {
		case protocol.TaskPending, protocol.TaskInProgress, protocol.TaskBlocked, protocol.TaskDone, protocol.TaskFailed, protocol.TaskCancelled:
		default:
			return protocol.TasksData{}, fmt.Errorf("task %q: status %q invalid (pending|in_progress|blocked|done|failed|cancelled)", t.Title, t.Status)
		}
		if t.ID == "" {
			next++
			t.ID = "t" + strconv.Itoa(next)
		}
		if t.BlockedBy == nil {
			t.BlockedBy = []string{}
		}
		p, had := prevByID[t.ID]
		if had {
			if t.DoneWhen == "" {
				t.DoneWhen = p.DoneWhen
			}
			if t.Check == "" {
				t.Check = p.Check
			}
			if t.Note == "" {
				t.Note = p.Note
			}
		}
		t.Evidence = ""
		if t.Status == protocol.TaskDone {
			if had && p.Status == protocol.TaskDone && p.Evidence != "" {
				t.Evidence = p.Evidence
			} else {
				t.Evidence = latestPassingCheck(log, t.ID)
			}
		}
		out.Tasks = append(out.Tasks, t)
	}
	return out, nil
}

// maxSeq returns the larger of cur and the numeric suffix of an id like "t12".
func maxSeq(cur int, id string) int {
	if strings.HasPrefix(id, "t") {
		if n, err := strconv.Atoi(id[1:]); err == nil && n > cur {
			return n
		}
	}
	return cur
}

func latestPassingCheck(log []protocol.Event, taskID string) string {
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Type != protocol.EventCheck {
			continue
		}
		var c protocol.CheckData
		if json.Unmarshal(log[i].Data, &c) == nil && c.TaskID == taskID && c.Status == "pass" {
			return log[i].ID
		}
	}
	return ""
}

func (b *Builtins) taskTool() module.Tool {
	type args struct {
		Tasks []protocol.Task `json:"tasks"`
	}
	return module.Tool{
		Name: "task.update",
		Description: "Replace the session's task list with this snapshot. Send every task each time (whole-list replace); keep ids stable; " +
			"give each task a done_when (the observable condition that proves it finished) and, where possible, a check command. " +
			"Statuses: pending, in_progress, blocked (needs a note), done, failed, cancelled.",
		Schema: schema(`{"type":"object","required":["tasks"],"properties":{"tasks":{"type":"array","items":{
			"type":"object","required":["title","status"],"properties":{
			"id":{"type":"string"},"title":{"type":"string"},
			"status":{"type":"string","enum":["pending","in_progress","blocked","done","failed","cancelled"]},
			"done_when":{"type":"string"},"check":{"type":"string"},
			"blocked_by":{"type":"array","items":{"type":"string"}},"note":{"type":"string"}}}}}}`),
		Run: func(ctx context.Context, s module.Session, raw json.RawMessage) (string, error) {
			a, err := decode[args](raw)
			if err != nil {
				return "", err
			}
			data, err := b.Tasks.UpdateTasks(ctx, s, a.Tasks, "model")
			if err != nil {
				return "", err
			}
			return protocol.RenderTasks(data.Tasks), nil
		},
	}
}
```

- [x] **Step 4: Run the whole tools package**

Run: `go test ./daemon/tools/ -count=1`
Expected: `ok`.

- [x] **Step 5: Commit**

```bash
git add daemon/tools/tasks.go daemon/tools/tasks_test.go daemon/tools/builtins.go
git commit -m "tools: task.update with whole-list merge and mechanical evidence"
```

---

### Task 11: Agent config and request building

**Files:**
- Create: `daemon/agent/config.go`
- Create: `daemon/agent/request.go`
- Create: `daemon/agent/request_test.go`

- [x] **Step 1: Write the failing test**

`daemon/agent/request_test.go`:

```go
package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/protocol"
)

// mklog builds a chained log from (type, data) pairs for tests.
func mklog(t *testing.T, items ...any) []protocol.Event {
	t.Helper()
	var log []protocol.Event
	var last *string
	for i := 0; i+1 < len(items); i += 2 {
		typ := items[i].(protocol.EventType)
		b, _ := json.Marshal(items[i+1])
		e := protocol.Event{ID: protocol.NewULID(), ParentID: last, Type: typ, Data: b}
		for last != nil && e.ID <= *last {
			e.ID = protocol.NewULID()
		}
		log = append(log, e)
		id := e.ID
		last = &id
	}
	if err := protocol.ValidateLog(log); err != nil {
		t.Fatal(err)
	}
	return log
}

var sess = protocol.SessionData{Workspace: "C:/w", WorkspaceKey: "w-1",
	Options: protocol.Options{Model: "m", CompactionEnabled: true, PermissionMode: protocol.PermissionAsk}}

func TestBuildRequestFoldsToolCallsAndContexts(t *testing.T) {
	log := mklog(t,
		protocol.EventSession, sess,
		protocol.EventContext, protocol.ContextData{Source: "module:skills", Slot: "prefix", Content: "# Skills\n- x"},
		protocol.EventMessage, protocol.MessageData{Role: "user", Content: "list files"},
		protocol.EventMessage, protocol.MessageData{Role: "assistant", Content: ""},
		protocol.EventToolCall, protocol.ToolCallData{CallID: "c1", Tool: "bash", Arguments: json.RawMessage(`{"command":"ls"}`), Source: "model"},
		protocol.EventToolResult, protocol.ToolResultData{CallID: "c1", Tool: "bash", Content: "a.go", Status: "ok"},
		protocol.EventMessage, protocol.MessageData{Role: "assistant", Content: "One file."},
		protocol.EventContext, protocol.ContextData{Source: "module:memory", Slot: "suffix", Content: "maybe relevant: foo"},
		protocol.EventStopVeto, protocol.StopVetoData{Module: "verify", Reason: "tree dirty"},
	)
	req := buildRequest(log, "SYS", "qwen", []provider.ToolSpec{{Name: "bash"}}, 0)
	if req.Model != "qwen" || len(req.Tools) != 1 {
		t.Fatalf("model/tools: %+v", req)
	}
	m := req.Messages
	if m[0].Role != "system" || !strings.HasPrefix(m[0].Content, "SYS") || !strings.Contains(m[0].Content, "# Skills") {
		t.Fatalf("system: %+v", m[0])
	}
	if m[1].Role != "user" || m[1].Content != "list files" {
		t.Fatalf("m1: %+v", m[1])
	}
	if m[2].Role != "assistant" || len(m[2].ToolCalls) != 1 || m[2].ToolCalls[0].ID != "c1" || m[2].ToolCalls[0].Name != "bash" {
		t.Fatalf("m2 must fold the tool call: %+v", m[2])
	}
	if m[3].Role != "tool" || m[3].ToolCallID != "c1" || m[3].Content != "a.go" {
		t.Fatalf("m3: %+v", m[3])
	}
	if m[4].Role != "assistant" || m[4].Content != "One file." {
		t.Fatalf("m4: %+v", m[4])
	}
	last := m[len(m)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "maybe relevant: foo") || !strings.Contains(last.Content, "Before stopping") || !strings.Contains(last.Content, "(verify) tree dirty") {
		t.Fatalf("suffix+vetoes must be one trailing user message: %+v", last)
	}
	if len(m) != 6 {
		t.Fatalf("message count %d: %+v", len(m), m)
	}
}

func TestBuildRequestAfterSummarize(t *testing.T) {
	base := mklog(t,
		protocol.EventSession, sess,
		protocol.EventContext, protocol.ContextData{Source: "module:skills", Slot: "prefix", Content: "OLD PREFIX"},
		protocol.EventMessage, protocol.MessageData{Role: "user", Content: "old"},
		protocol.EventMessage, protocol.MessageData{Role: "assistant", Content: "old reply"},
	)
	log := mklog(t,
		protocol.EventSession, sess,
		protocol.EventContext, protocol.ContextData{Source: "module:skills", Slot: "prefix", Content: "OLD PREFIX"},
		protocol.EventMessage, protocol.MessageData{Role: "user", Content: "old"},
		protocol.EventMessage, protocol.MessageData{Role: "assistant", Content: "old reply"},
		protocol.EventTasks, protocol.TasksData{Revision: 1, Source: "model", Tasks: []protocol.Task{{ID: "t1", Title: "Do it", Status: protocol.TaskInProgress, BlockedBy: []string{}}}},
		protocol.EventCompaction, protocol.CompactionData{Mode: protocol.CompactionSummarize, RangeStart: base[1].ID, RangeEnd: base[3].ID, Summary: "THE SUMMARY"},
		protocol.EventContext, protocol.ContextData{Source: "module:skills", Slot: "prefix", Content: "NEW PREFIX"},
		protocol.EventMessage, protocol.MessageData{Role: "user", Content: "continue"},
	)
	// Fix the range ids to this log's own ids (mklog regenerates ids).
	cd := protocol.CompactionData{Mode: protocol.CompactionSummarize, RangeStart: log[1].ID, RangeEnd: log[3].ID, Summary: "THE SUMMARY"}
	log[5].Data, _ = json.Marshal(cd)

	req := buildRequest(log, "SYS", "m", nil, 0)
	sys := req.Messages[0].Content
	if strings.Contains(sys, "OLD PREFIX") || !strings.Contains(sys, "NEW PREFIX") {
		t.Fatalf("prefix must be the post-compaction one: %q", sys)
	}
	if !strings.Contains(sys, "THE SUMMARY") || !strings.Contains(sys, "## Current state") || !strings.Contains(sys, "- [>] t1 Do it") {
		t.Fatalf("summary and state block missing: %q", sys)
	}
	if len(req.Messages) != 2 || req.Messages[1].Content != "continue" {
		t.Fatalf("only post-compaction messages: %+v", req.Messages)
	}
}
```

- [x] **Step 2: Run to see it fail**

Run: `go test ./daemon/agent/`
Expected: `undefined: buildRequest`.

- [x] **Step 3: Implement `config.go`**

```go
package agent

// CompactionConfig sets when and how the loop shrinks the request (spec §6).
type CompactionConfig struct {
	// ClearAt is the fraction of the context window at which stage 0
	// (tool-result clearing) runs. Default 0.70.
	ClearAt float64
	// SummarizeAt is the fraction at which stage 1 (summarize) runs. Default 0.85.
	SummarizeAt float64
	// KeepTurns is how many recent assistant turns keep their tool results
	// intact through stage 0. Default 4.
	KeepTurns int
}

// Config tunes the loop. Zero values take the defaults below.
type Config struct {
	SystemPrompt         string
	MaxConsecutiveVetoes int // default 5
	NoProgressTurns      int // default 3
	MaxTokens            int // per response; 0 = provider default
	Compaction           CompactionConfig
	// TasksEnabled offers task.update to the model. Default true.
	TasksEnabled *bool
}

func (c Config) withDefaults() Config {
	if c.SystemPrompt == "" {
		c.SystemPrompt = DefaultSystemPrompt
	}
	if c.MaxConsecutiveVetoes == 0 {
		c.MaxConsecutiveVetoes = 5
	}
	if c.NoProgressTurns == 0 {
		c.NoProgressTurns = 3
	}
	if c.Compaction.ClearAt == 0 {
		c.Compaction.ClearAt = 0.70
	}
	if c.Compaction.SummarizeAt == 0 {
		c.Compaction.SummarizeAt = 0.85
	}
	if c.Compaction.KeepTurns == 0 {
		c.Compaction.KeepTurns = 4
	}
	if c.TasksEnabled == nil {
		t := true
		c.TasksEnabled = &t
	}
	return c
}

// DefaultSystemPrompt is the static prefix of every request. It states
// mechanism, not policy: how tools work and how a turn ends. Policy (what to
// gate, when to require tasks) comes from modules as context blocks.
const DefaultSystemPrompt = `You are nabu, a coding agent running inside the user's workspace.

Rules:
- Use the tools to inspect and change the workspace. Paths are relative to the workspace unless absolute.
- For work with more than one step, call task.update first with every step, each with a done_when: the observable condition that proves that step is finished. Keep the list current as you go.
- Verify before you claim: run the relevant test or command and read its output before marking a task done or saying the work is finished.
- When the work is finished, reply with a short summary and no tool calls. If you cannot proceed without the user, say exactly what you need.`
```

- [x] **Step 4: Implement `request.go`**

```go
package agent

import (
	"strings"

	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/protocol"
)

// buildRequest turns the log into a provider request (protocol spec §6).
// The system message is systemPrompt + prefix context blocks + (after a
// summarize) the summary and Current state block. Message/tool events follow
// with tool calls folded into their assistant message. Suffix blocks and
// outstanding vetoes become one trailing user message.
func buildRequest(log []protocol.Event, systemPrompt, model string, tools []provider.ToolSpec, maxTokens int) provider.Request {
	byID := make(map[string]protocol.Event, len(log))
	for _, e := range log {
		byID[e.ID] = e
	}
	segs := protocol.Assemble(log)

	var sys strings.Builder
	sys.WriteString(systemPrompt)
	if d, ok := firstOf[protocol.SessionData](log); ok {
		sys.WriteString("\n\nWorkspace: " + d.Workspace)
	}
	var msgs []provider.Message
	var pending *provider.Message // assistant message collecting tool calls
	var trailing []string
	flush := func() {
		if pending != nil {
			msgs = append(msgs, *pending)
			pending = nil
		}
	}
	for _, s := range segs {
		switch s.Kind {
		case protocol.SegPrefixContext:
			sys.WriteString("\n\n" + s.Text)
		case protocol.SegSummary:
			sys.WriteString("\n\n## Earlier conversation (summarized)\n" + s.Text)
		case protocol.SegState:
			sys.WriteString("\n\n" + s.Text)
		case protocol.SegMessage:
			flush()
			d := protocol.MustData[protocol.MessageData](byID[s.ID])
			m := provider.Message{Role: d.Role, Content: d.Content}
			if d.Role == "assistant" {
				if d.Interrupted && m.Content == "" {
					m.Content = "(interrupted)"
				}
				pending = &m
			} else {
				msgs = append(msgs, m)
			}
		case protocol.SegToolCall:
			d := protocol.MustData[protocol.ToolCallData](byID[s.ID])
			if pending == nil {
				pending = &provider.Message{Role: "assistant"}
			}
			pending.ToolCalls = append(pending.ToolCalls, provider.ToolCall{ID: d.CallID, Name: d.Tool, Arguments: d.Arguments})
		case protocol.SegToolResult:
			flush()
			d := protocol.MustData[protocol.ToolResultData](byID[s.ID])
			msgs = append(msgs, provider.Message{Role: "tool", ToolCallID: d.CallID, Content: d.Content})
		case protocol.SegToolResultCleared:
			flush()
			d := protocol.MustData[protocol.ToolResultData](byID[s.ID])
			msgs = append(msgs, provider.Message{Role: "tool", ToolCallID: d.CallID, Content: s.Text})
		case protocol.SegSuffixContext:
			trailing = append(trailing, s.Text)
		case protocol.SegVetoes:
			trailing = append(trailing, s.Text)
		}
	}
	flush()
	if len(trailing) > 0 {
		msgs = append(msgs, provider.Message{Role: "user", Content: strings.Join(trailing, "\n\n")})
	}
	all := append([]provider.Message{{Role: "system", Content: sys.String()}}, msgs...)
	return provider.Request{Model: model, Messages: all, Tools: tools, MaxTokens: maxTokens}
}

// firstOf decodes the first event of the given data type.
func firstOf[T any](log []protocol.Event) (*T, bool) {
	for _, e := range log {
		if v, err := protocol.DecodeData(e); err == nil {
			if t, ok := v.(*T); ok {
				return t, true
			}
		}
	}
	return nil, false
}
```

- [x] **Step 5: Run the tests**

Run: `go test ./daemon/agent/ -count=1 -v`
Expected: two PASS.

- [x] **Step 6: Commit**

```bash
git add daemon/agent/config.go daemon/agent/request.go daemon/agent/request_test.go
git commit -m "agent: config defaults and request building from the log"
```

---

### Task 12: Module-facing session handle and host

**Files:**
- Create: `daemon/agent/handle.go`
- Create: `daemon/agent/handle_test.go`

The Manager does not exist yet; this task defines the handle and host against a
small `core` interface that Task 13's Manager will satisfy, so the handle can be
tested on its own.

- [x] **Step 1: Write the failing test**

`daemon/agent/handle_test.go`:

```go
package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/session"
	"github.com/corporealshift/nabu/protocol"
)

type fakeCore struct {
	calls []protocol.ToolCallData
}

func (f *fakeCore) invokeTool(ctx context.Context, h *sessionHandle, call protocol.ToolCallData) protocol.ToolResultData {
	f.calls = append(f.calls, call)
	return protocol.ToolResultData{CallID: call.CallID, Tool: call.Tool, Content: "ran", Status: "ok"}
}
func (f *fakeCore) complete(ctx context.Context, h *sessionHandle, req module.CompletionRequest) (module.CompletionResponse, error) {
	return module.CompletionResponse{Content: "judged"}, nil
}
func (f *fakeCore) ask(ctx context.Context, h *sessionHandle, q string, choices []string) (string, error) {
	return "yes", nil
}
func (f *fakeCore) dataDir(name string) (string, error) { return "/tmp/" + name, nil }

func newHandle(t *testing.T) (*sessionHandle, *fakeCore) {
	t.Helper()
	st, _ := session.Open(t.TempDir())
	s, err := st.Create("C:/w", "w-1", protocol.Options{Model: "m", CompactionEnabled: true, PermissionMode: protocol.PermissionAsk})
	if err != nil {
		t.Fatal(err)
	}
	core := &fakeCore{}
	return &sessionHandle{core: core, s: s, ws: module.Workspace{Path: "C:/w", Key: "w-1"}}, core
}

func TestHandleAppendAllowsOnlyModuleEvents(t *testing.T) {
	h, _ := newHandle(t)
	if _, err := h.Append(protocol.EventCheck, protocol.CheckData{Name: "x", Kind: "module", Status: "pass", Summary: "ok"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Append(protocol.EventNotice, protocol.NoticeData{Source: "module:t", Level: "info", Message: "hi"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Append(protocol.EventGoal, protocol.GoalData{Condition: "c", State: "met", Source: "module:t"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Append(protocol.EventGoal, protocol.GoalData{Condition: "c", State: "set", Source: "module:t"}); err == nil {
		t.Fatal("modules may not set goals")
	}
	if _, err := h.Append(protocol.EventMessage, protocol.MessageData{Role: "user", Content: "forged"}); err == nil || !strings.Contains(err.Error(), "may not append") {
		t.Fatalf("modules may not append messages: %v", err)
	}
	if h.State().Goal == nil || h.State().Goal.State != "met" {
		t.Fatal("state must reflect the appended goal")
	}
	ev, _ := h.Events(nil)
	if len(ev) != 4 {
		t.Fatalf("events: %d", len(ev))
	}
}

func TestHostToolCallCarriesModuleSource(t *testing.T) {
	h, core := newHandle(t)
	host := newHost(core, "verify", nil)
	res, err := host.Tools().Call(context.Background(), h, "bash", json.RawMessage(`{"command":"go test ./..."}`))
	if err != nil || res.Content != "ran" {
		t.Fatalf("call: %+v %v", res, err)
	}
	if len(core.calls) != 1 || core.calls[0].Source != "module:verify" || core.calls[0].Tool != "bash" || core.calls[0].CallID == "" {
		t.Fatalf("recorded call: %+v", core.calls)
	}
	out, err := host.Model().Complete(context.Background(), h, module.CompletionRequest{Messages: []module.Message{{Role: "user", Content: "?"}}})
	if err != nil || out.Content != "judged" {
		t.Fatalf("complete: %+v %v", out, err)
	}
	dir, _ := host.DataDir("verify")
	if !strings.HasSuffix(dir, "verify") {
		t.Fatalf("data dir: %s", dir)
	}
	if host.Log() == nil {
		t.Fatal("logger must not be nil")
	}
}
```

- [x] **Step 2: Run to see it fail**

Run: `go test ./daemon/agent/ -run 'Handle|Host'`
Expected: compile errors (`undefined: sessionHandle`, `newHost`).

- [x] **Step 3: Implement `handle.go`**

```go
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/session"
	"github.com/corporealshift/nabu/protocol"
)

// core is what the handle and host need from the Manager. Task 13's Manager
// implements it; tests use a fake.
type core interface {
	invokeTool(ctx context.Context, h *sessionHandle, call protocol.ToolCallData) protocol.ToolResultData
	complete(ctx context.Context, h *sessionHandle, req module.CompletionRequest) (module.CompletionResponse, error)
	ask(ctx context.Context, h *sessionHandle, question string, choices []string) (string, error)
	dataDir(module string) (string, error)
}

// sessionHandle is the module.Session a module receives. It restricts what a
// module may append: check, notice, and goal verdicts (met|unmet|impossible).
type sessionHandle struct {
	core core
	s    *session.Session
	ws   module.Workspace
}

func (h *sessionHandle) ID() string                 { return h.s.ID() }
func (h *sessionHandle) Workspace() module.Workspace { return h.ws }
func (h *sessionHandle) State() protocol.State       { return h.s.State() }

func (h *sessionHandle) Events(after *string) ([]protocol.Event, error) {
	ev, _, err := h.s.EventsAfter(after)
	return ev, err
}

func (h *sessionHandle) Append(t protocol.EventType, data any) (protocol.Event, error) {
	switch t {
	case protocol.EventCheck, protocol.EventNotice:
	case protocol.EventGoal:
		b, err := json.Marshal(data)
		if err != nil {
			return protocol.Event{}, err
		}
		var g protocol.GoalData
		if err := json.Unmarshal(b, &g); err != nil {
			return protocol.Event{}, err
		}
		switch g.State {
		case "met", "unmet", "impossible":
		default:
			return protocol.Event{}, fmt.Errorf("modules may only append goal verdicts (met|unmet|impossible), not %q", g.State)
		}
	default:
		return protocol.Event{}, fmt.Errorf("modules may not append %s events", t)
	}
	return h.s.Append(t, data)
}

// host is the per-module module.Host.
type host struct {
	core core
	name string
	log  *slog.Logger
}

func newHost(c core, name string, log *slog.Logger) *host {
	if log == nil {
		log = slog.Default()
	}
	return &host{core: c, name: name, log: log.With("module", name)}
}

func (h *host) Model() module.Model                 { return modelAPI{h} }
func (h *host) Tools() module.ToolCaller            { return toolCaller{h} }
func (h *host) UI() module.UI                       { return uiAPI{h} }
func (h *host) Log() *slog.Logger                   { return h.log }
func (h *host) DataDir(name string) (string, error) { return h.core.dataDir(name) }

type modelAPI struct{ h *host }

func (m modelAPI) Complete(ctx context.Context, s module.Session, req module.CompletionRequest) (module.CompletionResponse, error) {
	sh, ok := s.(*sessionHandle)
	if !ok {
		return module.CompletionResponse{}, fmt.Errorf("module %s: foreign session handle", m.h.name)
	}
	return m.h.core.complete(ctx, sh, req)
}

type toolCaller struct{ h *host }

func (t toolCaller) Call(ctx context.Context, s module.Session, tool string, args json.RawMessage) (protocol.ToolResultData, error) {
	sh, ok := s.(*sessionHandle)
	if !ok {
		return protocol.ToolResultData{}, fmt.Errorf("module %s: foreign session handle", t.h.name)
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	call := protocol.ToolCallData{
		CallID:    "mod_" + protocol.NewULID(),
		Tool:      tool,
		Arguments: args,
		Source:    "module:" + t.h.name,
	}
	res := t.h.core.invokeTool(ctx, sh, call)
	if res.Status == "error" {
		return res, fmt.Errorf("%s: %s", tool, res.Content)
	}
	return res, nil
}

type uiAPI struct{ h *host }

func (u uiAPI) Ask(ctx context.Context, s module.Session, question string, choices []string) (string, error) {
	sh, ok := s.(*sessionHandle)
	if !ok {
		return "", fmt.Errorf("module %s: foreign session handle", u.h.name)
	}
	return u.h.core.ask(ctx, sh, question, choices)
}
```

- [x] **Step 4: Run the tests**

Run: `go test ./daemon/agent/ -count=1`
Expected: `ok`.

- [x] **Step 5: Commit**

```bash
git add daemon/agent/handle.go daemon/agent/handle_test.go
git commit -m "agent: module session handle and per-module host"
```

---

### Task 13: Manager — construction, tool invocation, lifecycle

**Files:**
- Create: `daemon/agent/manager.go`
- Create: `daemon/agent/manager_test.go`
- Modify: `daemon/agent/config.go` (add `DefaultModel`)

This task builds everything around the loop. `runLoop` is a stub that immediately
returns the session to `idle`; Task 14 fills it in.

- [x] **Step 1: Add `DefaultModel` to Config**

In `daemon/agent/config.go`, add to the `Config` struct after `SystemPrompt`:

```go
	// DefaultModel is used when a session is created without one.
	DefaultModel string
```

and in `withDefaults`, after the `SystemPrompt` block:

```go
	if c.DefaultModel == "" {
		c.DefaultModel = "default"
	}
```

- [x] **Step 2: Write the failing test**

`daemon/agent/manager_test.go`:

```go
package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/daemon/session"
	"github.com/corporealshift/nabu/daemon/tools"
	"github.com/corporealshift/nabu/protocol"
)

// harness wires a Manager over a temp dir and a fake provider.
type harness struct {
	m     *Manager
	fake  *provider.Fake
	store *session.Store
	dir   string
}

func newHarness(t *testing.T, mods []module.Module, script []provider.Response) *harness {
	t.Helper()
	dir := t.TempDir()
	store, err := session.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	fake := &provider.Fake{Script: script}
	pr := provider.NewRegistry()
	pr.Add(provider.Config{Name: "fake", ContextWindow: 8000}, fake, true)
	builtins := &tools.Builtins{}
	mr := module.NewRegistry(append([]module.Module{builtins}, mods...), module.Options{Log: testLogger()})
	m, err := New(Deps{Store: store, Providers: pr, Modules: mr, Builtins: builtins, Root: dir, Log: testLogger()},
		Config{DefaultModel: "fake/m"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Shutdown(context.Background()) })
	return &harness{m: m, fake: fake, store: store, dir: dir}
}

func (h *harness) create(t *testing.T) *session.Session {
	t.Helper()
	s, err := h.m.Create(context.Background(), h.dir, protocol.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// ctxModule injects a prefix block at session start.
type ctxModule struct{ text string }

func (ctxModule) Name() string                    { return "ctxmod" }
func (ctxModule) Init(module.Host, module.Config) error { return nil }
func (c ctxModule) SessionStart(context.Context, module.Session) ([]module.ContextBlock, error) {
	return []module.ContextBlock{{Slot: "prefix", Content: c.text}}, nil
}

func TestCreateResolvesWorkspaceAndRunsSessionStart(t *testing.T) {
	h := newHarness(t, []module.Module{ctxModule{"# Skills"}}, nil)
	s := h.create(t)
	ev := s.Events()
	if len(ev) != 2 || ev[1].Type != protocol.EventContext {
		t.Fatalf("events: %+v", ev)
	}
	c := protocol.MustData[protocol.ContextData](ev[1])
	if c.Source != "module:ctxmod" || c.Slot != "prefix" || c.Content != "# Skills" {
		t.Fatalf("context: %+v", c)
	}
	sd := protocol.MustData[protocol.SessionData](ev[0])
	if sd.WorkspaceKey == "" || sd.Options.Model != "fake/m" || sd.Options.PermissionMode != protocol.PermissionAsk {
		t.Fatalf("session data: %+v", sd)
	}
}

func TestInvokeToolLogsCallAndResult(t *testing.T) {
	h := newHarness(t, nil, nil)
	s := h.create(t)
	handle, err := h.m.handle(s.ID())
	if err != nil {
		t.Fatal(err)
	}
	res := h.m.invokeTool(context.Background(), handle, protocol.ToolCallData{
		CallID: "c1", Tool: "write", Arguments: json.RawMessage(`{"path":"x.txt","content":"hi"}`), Source: "model"})
	if res.Status != "ok" || !strings.Contains(res.Content, "x.txt") {
		t.Fatalf("result: %+v", res)
	}
	ev := s.Events()
	if ev[len(ev)-2].Type != protocol.EventToolCall || ev[len(ev)-1].Type != protocol.EventToolResult {
		t.Fatalf("must log call then result: %+v", ev)
	}
}

func TestUnknownToolIsAnErrorResultNotACrash(t *testing.T) {
	h := newHarness(t, nil, nil)
	s := h.create(t)
	handle, _ := h.m.handle(s.ID())
	res := h.m.invokeTool(context.Background(), handle, protocol.ToolCallData{CallID: "c1", Tool: "nope", Arguments: json.RawMessage(`{}`), Source: "model"})
	if res.Status != "error" || !strings.Contains(res.Content, "unknown tool") {
		t.Fatalf("result: %+v", res)
	}
}

// denier denies every bash call.
type denier struct{}

func (denier) Name() string                          { return "denier" }
func (denier) Init(module.Host, module.Config) error { return nil }
func (denier) GateTool(_ context.Context, _ module.Session, c protocol.ToolCallData) module.Verdict {
	if c.Tool == "bash" {
		return module.Verdict{Decision: module.Deny, Reason: "no shell today"}
	}
	return module.Verdict{Decision: module.Allow}
}

func TestDeniedToolBecomesAnErrorResult(t *testing.T) {
	h := newHarness(t, []module.Module{denier{}}, nil)
	s := h.create(t)
	handle, _ := h.m.handle(s.ID())
	res := h.m.invokeTool(context.Background(), handle, protocol.ToolCallData{CallID: "c1", Tool: "bash", Arguments: json.RawMessage(`{"command":"ls"}`), Source: "model"})
	if res.Status != "error" || !strings.Contains(res.Content, "no shell today") {
		t.Fatalf("result: %+v", res)
	}
	// The denial is still logged as a call and a result.
	ev := s.Events()
	if ev[len(ev)-1].Type != protocol.EventToolResult {
		t.Fatal("denied calls must still be logged")
	}
}

// asker approves or denies on demand.
type fakeAsker struct {
	approve  bool
	asked    int
	question string
}

func (a *fakeAsker) Permission(_ context.Context, _ string, _ protocol.ToolCallData, _, _ string) (bool, string) {
	a.asked++
	if a.approve {
		return true, ""
	}
	return false, "user said no"
}
func (a *fakeAsker) Ask(_ context.Context, _, q string, _ []string) (string, error) {
	a.question = q
	return "an answer", nil
}

// asksModule requests confirmation for every call.
type asksModule struct{}

func (asksModule) Name() string                          { return "asks" }
func (asksModule) Init(module.Host, module.Config) error { return nil }
func (asksModule) GateTool(context.Context, module.Session, protocol.ToolCallData) module.Verdict {
	return module.Verdict{Decision: module.Ask, Summary: "write a file", Risk: "medium"}
}

func TestAskGoesToTheAskerAndBypassSkipsIt(t *testing.T) {
	asker := &fakeAsker{approve: false}
	h := newHarness(t, []module.Module{asksModule{}}, nil)
	h.m.deps.Asker = asker
	s := h.create(t)
	handle, _ := h.m.handle(s.ID())
	call := protocol.ToolCallData{CallID: "c1", Tool: "write", Arguments: json.RawMessage(`{"path":"a.txt","content":"x"}`), Source: "model"}
	res := h.m.invokeTool(context.Background(), handle, call)
	if res.Status != "error" || !strings.Contains(res.Content, "user said no") || asker.asked != 1 {
		t.Fatalf("deny path: %+v asked=%d", res, asker.asked)
	}
	// bypass mode skips the ask entirely.
	if _, err := h.m.SetOption(context.Background(), s.ID(), "permission_mode", "bypass"); err != nil {
		t.Fatal(err)
	}
	call.CallID = "c2"
	res = h.m.invokeTool(context.Background(), handle, call)
	if res.Status != "ok" || asker.asked != 1 {
		t.Fatalf("bypass path: %+v asked=%d", res, asker.asked)
	}
}

func TestModuleToolCallIsAttributedToTheModule(t *testing.T) {
	h := newHarness(t, nil, nil)
	s := h.create(t)
	handle, _ := h.m.handle(s.ID())
	host := newHost(h.m, "verify", nil)
	if _, err := host.Tools().Call(context.Background(), handle, "write", json.RawMessage(`{"path":"m.txt","content":"x"}`)); err != nil {
		t.Fatal(err)
	}
	ev := s.Events()
	call := protocol.MustData[protocol.ToolCallData](ev[len(ev)-2])
	if call.Source != "module:verify" {
		t.Fatalf("source: %q", call.Source)
	}
}

func TestUpdateTasksAppendsSnapshot(t *testing.T) {
	h := newHarness(t, nil, nil)
	s := h.create(t)
	handle, _ := h.m.handle(s.ID())
	d, err := h.m.UpdateTasks(context.Background(), handle, []protocol.Task{
		{Title: "One", Status: protocol.TaskPending, DoneWhen: "x"},
	}, "model")
	if err != nil || d.Revision != 1 || d.Tasks[0].ID != "t1" {
		t.Fatalf("first: %+v %v", d, err)
	}
	d, err = h.m.UpdateTasks(context.Background(), handle, []protocol.Task{
		{ID: "t1", Title: "One", Status: protocol.TaskDone},
	}, "client")
	if err != nil || d.Revision != 2 || d.Source != "client" {
		t.Fatalf("second: %+v %v", d, err)
	}
	if got := s.State().Tasks; len(got) != 1 || got[0].Status != protocol.TaskDone {
		t.Fatalf("projection: %+v", got)
	}
}

func TestGoalAndBudgetMethods(t *testing.T) {
	h := newHarness(t, nil, nil)
	s := h.create(t)
	if _, err := h.m.SetGoal(context.Background(), s.ID(), "tests pass"); err != nil {
		t.Fatal(err)
	}
	if g := s.State().Goal; g == nil || g.State != "set" || g.Source != "client" {
		t.Fatalf("goal: %+v", g)
	}
	if _, err := h.m.SetBudget(context.Background(), s.ID(), protocol.BudgetData{MaxTurns: 10, Source: "client"}); err != nil {
		t.Fatal(err)
	}
	if s.State().Budget.MaxTurns != 10 {
		t.Fatal("budget not recorded")
	}
	if _, err := h.m.ClearGoal(context.Background(), s.ID()); err != nil {
		t.Fatal(err)
	}
	if s.State().GoalActive() {
		t.Fatal("goal must be inactive after clear")
	}
	if _, err := h.m.ClearGoal(context.Background(), s.ID()); err == nil {
		t.Fatal("clearing twice must be an invalid transition")
	}
}

func TestPromptOnTerminalSessionIsRejected(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{{Content: "done"}})
	s := h.create(t)
	if _, err := h.m.Prompt(context.Background(), s.ID(), "hi"); err != nil {
		t.Fatal(err)
	}
	h.m.WaitIdle(s.ID())
	if err := h.m.Stop(context.Background(), s.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.Prompt(context.Background(), s.ID(), "again"); err == nil {
		t.Fatal("prompting a completed session must fail")
	}
	st := s.State()
	if st.State != protocol.StateCompleted {
		t.Fatalf("state: %s", st.State)
	}
	ev := s.Events()
	if ev[len(ev)-1].Type != protocol.EventReport {
		t.Fatal("stop must emit a report last")
	}
	r := protocol.MustData[protocol.ReportData](ev[len(ev)-1])
	if r.ExitStatus != protocol.StateCompleted || r.Checks == nil || r.Tasks.Open == nil {
		t.Fatalf("report: %+v", r)
	}
}
```

Add a shared test logger — create `daemon/agent/testlog_test.go`:

```go
package agent

import "log/slog"

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discard{}, &slog.HandlerOptions{Level: slog.LevelError}))
}
```

- [x] **Step 3: Run to see it fail**

Run: `go test ./daemon/agent/ -run Manager`
Expected: compile errors (`undefined: New`, `Deps`, …).

- [x] **Step 4: Implement `manager.go`**

```go
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/daemon/session"
	"github.com/corporealshift/nabu/daemon/tools"
	"github.com/corporealshift/nabu/daemon/workspace"
	"github.com/corporealshift/nabu/protocol"
)

// Asker answers questions that need a human: permission for a gated tool call
// and module ui.ask. P1b supplies the WebSocket implementation; a nil Asker
// denies permission and errors on ask.
type Asker interface {
	Permission(ctx context.Context, sessionID string, call protocol.ToolCallData, summary, risk string) (approved bool, reason string)
	Ask(ctx context.Context, sessionID, question string, choices []string) (string, error)
}

// DeltaSink receives streaming text. Deltas are ephemeral: never logged,
// never replayed (protocol spec §7.6).
type DeltaSink func(sessionID, turnID, text string)

// Deps are the collaborators a Manager needs.
type Deps struct {
	Store     *session.Store
	Providers *provider.Registry
	Modules   *module.Registry
	Builtins  *tools.Builtins
	Root      string // ~/.nabu
	Log       *slog.Logger
	Asker     Asker
	Deltas    DeltaSink
}

// Manager owns every live session: it creates them, runs their loops, and is
// the one implementation of the module Host facilities.
type Manager struct {
	deps Deps
	cfg  Config
	log  *slog.Logger

	toolsByName map[string]module.Tool
	toolSpecs   []provider.ToolSpec

	mu      sync.Mutex
	rt      map[string]*runState
	closed  bool
	wg      sync.WaitGroup
}

// runState is a session's in-memory runtime.
type runState struct {
	handle  *sessionHandle
	running bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// New builds a Manager, initialises modules, and collects the tool registry.
// Built-in tools register through the same ToolProvider path as module tools.
func New(deps Deps, cfg Config) (*Manager, error) {
	if deps.Store == nil || deps.Providers == nil || deps.Modules == nil {
		return nil, fmt.Errorf("agent: Store, Providers and Modules are required")
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	if deps.Root == "" {
		deps.Root = "."
	}
	m := &Manager{deps: deps, cfg: cfg.withDefaults(), log: deps.Log, rt: map[string]*runState{}}
	if deps.Builtins != nil {
		if *m.cfg.TasksEnabled {
			deps.Builtins.Tasks = m
		}
	}
	deps.Modules.Init(
		func(name string) module.Host { return newHost(m, name, deps.Log) },
		func(name string) module.Config { return cfg.ModuleConfig(name) },
	)
	list, err := deps.Modules.Tools()
	if err != nil {
		return nil, err
	}
	m.toolsByName = make(map[string]module.Tool, len(list))
	for _, t := range list {
		m.toolsByName[t.Name] = t
		m.toolSpecs = append(m.toolSpecs, provider.ToolSpec{Name: t.Name, Description: t.Description, Parameters: t.Schema})
	}
	return m, nil
}

// toolNames lists registered tools, sorted, for error messages.
func (m *Manager) toolNames() []string {
	out := make([]string, 0, len(m.toolsByName))
	for n := range m.toolsByName {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------- lifecycle

// Create resolves the workspace, starts a session, and dispatches SessionStart.
func (m *Manager) Create(ctx context.Context, workspacePath string, opts protocol.Options) (*session.Session, error) {
	ws, err := workspace.Resolve(workspacePath)
	if err != nil {
		return nil, err
	}
	if opts.Model == "" {
		opts.Model = m.cfg.DefaultModel
	}
	if opts.PermissionMode == "" {
		opts.PermissionMode = protocol.PermissionAsk
	}
	s, err := m.deps.Store.Create(ws.Path, ws.Key, opts)
	if err != nil {
		return nil, err
	}
	h := m.attach(s, ws)
	m.appendContexts(h, m.deps.Modules.SessionStart(ctx, h))
	return s, nil
}

// Adopt registers an existing session (loaded from disk) with the Manager.
func (m *Manager) Adopt(s *session.Session) (*sessionHandle, error) {
	path, key := s.Workspace()
	return m.attach(s, module.Workspace{Path: path, Key: key}), nil
}

func (m *Manager) attach(s *session.Session, ws module.Workspace) *sessionHandle {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rs, ok := m.rt[s.ID()]; ok {
		return rs.handle
	}
	h := &sessionHandle{core: m, s: s, ws: ws}
	m.rt[s.ID()] = &runState{handle: h}
	return h
}

// handle returns the runtime handle for a session, loading it if needed.
func (m *Manager) handle(id string) (*sessionHandle, error) {
	m.mu.Lock()
	if rs, ok := m.rt[id]; ok {
		m.mu.Unlock()
		return rs.handle, nil
	}
	m.mu.Unlock()
	s, err := m.deps.Store.Get(id)
	if err != nil {
		return nil, err
	}
	return m.Adopt(s)
}

func (m *Manager) runState(id string) (*runState, error) {
	if _, err := m.handle(id); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rt[id], nil
}

// appendContexts logs every module-injected block as a context event, so the
// request stays a pure function of the log.
func (m *Manager) appendContexts(h *sessionHandle, blocks []module.SourcedBlock) {
	for _, b := range blocks {
		if strings.TrimSpace(b.Block.Content) == "" {
			continue
		}
		slot := b.Block.Slot
		if slot == "" {
			slot = "prefix"
		}
		if _, err := h.s.Append(protocol.EventContext, protocol.ContextData{
			Source: "module:" + b.Module, Slot: slot, Content: b.Block.Content}); err != nil {
			m.log.Error("context append failed", "session", h.ID(), "module", b.Module, "err", err)
		}
	}
}

// Prompt appends a user message and starts the loop if it is not running.
func (m *Manager) Prompt(ctx context.Context, id, content string) (protocol.Event, error) {
	h, err := m.handle(id)
	if err != nil {
		return protocol.Event{}, err
	}
	switch st := h.State().State; st {
	case protocol.StateCompleted, protocol.StateError, protocol.StatePaused:
		return protocol.Event{}, protocol.NewRPCError(protocol.CodeInvalidTransition,
			fmt.Sprintf("session is %s; resume it first", st))
	}
	e, err := h.s.Append(protocol.EventMessage, protocol.MessageData{Role: "user", Content: content})
	if err != nil {
		return protocol.Event{}, err
	}
	m.ensureRunning(h, "prompt")
	return e, nil
}

// SetGoal records a goal and starts the loop if the session is idle.
func (m *Manager) SetGoal(ctx context.Context, id, condition string) (protocol.Event, error) {
	h, err := m.handle(id)
	if err != nil {
		return protocol.Event{}, err
	}
	if strings.TrimSpace(condition) == "" {
		return protocol.Event{}, protocol.NewRPCError(protocol.CodeInvalidParams, "condition is required")
	}
	e, err := h.s.Append(protocol.EventGoal, protocol.GoalData{Condition: condition, State: "set", Source: "client"})
	if err != nil {
		return protocol.Event{}, err
	}
	if h.State().State == protocol.StateIdle {
		m.ensureRunning(h, "goal")
	}
	return e, nil
}

// ClearGoal deactivates the current goal.
func (m *Manager) ClearGoal(ctx context.Context, id string) (protocol.Event, error) {
	h, err := m.handle(id)
	if err != nil {
		return protocol.Event{}, err
	}
	st := h.State()
	if !st.GoalActive() {
		return protocol.Event{}, protocol.NewRPCError(protocol.CodeInvalidTransition, "no active goal")
	}
	return h.s.Append(protocol.EventGoal, protocol.GoalData{Condition: st.Goal.Condition, State: "cleared", Source: "client"})
}

// SetOption changes one session option.
func (m *Manager) SetOption(ctx context.Context, id, key string, value any) (protocol.Event, error) {
	h, err := m.handle(id)
	if err != nil {
		return protocol.Event{}, err
	}
	opts := h.State().Options
	var from any
	switch key {
	case "model":
		from = opts.Model
		if _, ok := value.(string); !ok {
			return protocol.Event{}, protocol.NewRPCError(protocol.CodeInvalidParams, "model must be a string")
		}
	case "compaction_enabled":
		from = opts.CompactionEnabled
		if _, ok := value.(bool); !ok {
			return protocol.Event{}, protocol.NewRPCError(protocol.CodeInvalidParams, "compaction_enabled must be a boolean")
		}
	case "permission_mode":
		from = string(opts.PermissionMode)
		s, ok := value.(string)
		if !ok || (s != "ask" && s != "auto" && s != "bypass") {
			return protocol.Event{}, protocol.NewRPCError(protocol.CodeInvalidParams, "permission_mode must be ask, auto or bypass")
		}
	default:
		return protocol.Event{}, protocol.NewRPCError(protocol.CodeInvalidParams, "unknown option "+key)
	}
	fb, _ := json.Marshal(from)
	tb, _ := json.Marshal(value)
	return h.s.Append(protocol.EventOptionsChange, protocol.OptionsChangeData{
		Key: key, From: fb, To: tb, Source: "client"})
}

// SetBudget records a new loop bound.
func (m *Manager) SetBudget(ctx context.Context, id string, b protocol.BudgetData) (protocol.Event, error) {
	h, err := m.handle(id)
	if err != nil {
		return protocol.Event{}, err
	}
	if b.Source == "" {
		b.Source = "client"
	}
	return h.s.Append(protocol.EventBudget, b)
}

// UpdateTasks implements tools.TaskStore: merge the snapshot and log it.
func (m *Manager) UpdateTasks(ctx context.Context, s module.Session, incoming []protocol.Task, source string) (protocol.TasksData, error) {
	h, ok := s.(*sessionHandle)
	if !ok {
		return protocol.TasksData{}, fmt.Errorf("foreign session handle")
	}
	log := h.s.Events()
	var prev protocol.TasksData
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Type == protocol.EventTasks {
			prev = *protocol.MustData[protocol.TasksData](log[i])
			break
		}
	}
	next, err := tools.Merge(prev, incoming, log, source)
	if err != nil {
		return protocol.TasksData{}, err
	}
	if _, err := h.s.Append(protocol.EventTasks, next); err != nil {
		return protocol.TasksData{}, err
	}
	return next, nil
}

// UpdateTasksByID is the client-facing entry point (nabu.session.update_tasks).
func (m *Manager) UpdateTasksByID(ctx context.Context, id string, incoming []protocol.Task) (protocol.TasksData, error) {
	h, err := m.handle(id)
	if err != nil {
		return protocol.TasksData{}, err
	}
	return m.UpdateTasks(ctx, h, incoming, "client")
}

// ensureRunning starts the loop goroutine unless one is already running.
func (m *Manager) ensureRunning(h *sessionHandle, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	rs := m.rt[h.ID()]
	if rs == nil || rs.running {
		return
	}
	from := h.State().State
	if _, err := h.s.Append(protocol.EventStateChange, protocol.StateChangeData{
		From: &from, To: protocol.StateRunning, Reason: reason}); err != nil {
		m.log.Error("state change failed", "session", h.ID(), "err", err)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	rs.running = true
	rs.cancel = cancel
	rs.done = make(chan struct{})
	done := rs.done
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		defer close(done)
		m.runLoop(ctx, h)
		m.mu.Lock()
		rs.running = false
		rs.cancel = nil
		m.mu.Unlock()
	}()
}

// stopRunning cancels an in-flight turn and waits for the goroutine to exit.
func (m *Manager) stopRunning(id string) {
	m.mu.Lock()
	rs := m.rt[id]
	var cancel context.CancelFunc
	var done chan struct{}
	if rs != nil && rs.running {
		cancel, done = rs.cancel, rs.done
	}
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// WaitIdle blocks until the session's loop is not running. Test and CLI helper.
func (m *Manager) WaitIdle(id string) {
	m.mu.Lock()
	rs := m.rt[id]
	var done chan struct{}
	if rs != nil && rs.running {
		done = rs.done
	}
	m.mu.Unlock()
	if done != nil {
		<-done
	}
}

// Interrupt cancels the current turn and returns the session to idle.
func (m *Manager) Interrupt(ctx context.Context, id string) error {
	h, err := m.handle(id)
	if err != nil {
		return err
	}
	m.stopRunning(id)
	from := h.State().State
	if from == protocol.StateIdle {
		return nil
	}
	_, err = h.s.Append(protocol.EventStateChange, protocol.StateChangeData{
		From: &from, To: protocol.StateIdle, Reason: "interrupted"})
	return err
}

// Stop ends a session: completed plus a report.
func (m *Manager) Stop(ctx context.Context, id string) error {
	h, err := m.handle(id)
	if err != nil {
		return err
	}
	m.stopRunning(id)
	if st := h.State().State; st == protocol.StateCompleted || st == protocol.StateError {
		return nil
	}
	return m.finish(ctx, h, protocol.StateCompleted, "stopped")
}

// Resume restarts a paused session, optionally raising the budget.
func (m *Manager) Resume(ctx context.Context, id string, budget *protocol.BudgetData) error {
	h, err := m.handle(id)
	if err != nil {
		return err
	}
	if h.State().State != protocol.StatePaused {
		return protocol.NewRPCError(protocol.CodeInvalidTransition, "session is not paused")
	}
	if budget != nil {
		if _, err := m.SetBudget(ctx, id, *budget); err != nil {
			return err
		}
	}
	m.deps.Modules.SessionResume(ctx, h)
	m.ensureRunning(h, "resumed")
	return nil
}

// Shutdown cancels every running turn and pauses those sessions so a restart
// can resume them (spec §8 graceful restart).
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	ids := make([]string, 0, len(m.rt))
	for id, rs := range m.rt {
		if rs.running {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.stopRunning(id)
		h, err := m.handle(id)
		if err != nil {
			continue
		}
		if h.State().State != protocol.StateRunning {
			continue
		}
		h.s.Append(protocol.EventNotice, protocol.NoticeData{
			Source: "daemon", Level: "warn", Message: "daemon is shutting down; session paused"})
		from := protocol.StateRunning
		h.s.Append(protocol.EventStateChange, protocol.StateChangeData{
			From: &from, To: protocol.StatePaused, Reason: "daemon_shutdown"})
	}
	m.wg.Wait()
	return nil
}

// finish moves a session to a terminal or paused state and emits the report.
func (m *Manager) finish(ctx context.Context, h *sessionHandle, to protocol.SessionState, reason string) error {
	m.deps.Modules.SessionEnd(ctx, h)
	from := h.State().State
	if from != to {
		if _, err := h.s.Append(protocol.EventStateChange, protocol.StateChangeData{
			From: &from, To: to, Reason: reason}); err != nil {
			return err
		}
	}
	return m.emitReport(ctx, h, to)
}

// emitReport builds the run report: core fills the log-derived fields,
// Reporter modules fill workspace observations (spec §13).
func (m *Manager) emitReport(ctx context.Context, h *sessionHandle, exit protocol.SessionState) error {
	st := h.State()
	r := protocol.ReportData{
		ExitStatus: exit,
		Tasks:      protocol.ReportTasks{Total: len(st.Tasks), Done: st.DoneTasks(), Open: []protocol.ReportTaskRef{}},
		Checks:     []protocol.ReportCheck{},
	}
	for _, t := range st.OpenTasks() {
		r.Tasks.Open = append(r.Tasks.Open, protocol.ReportTaskRef{ID: t.ID, Title: t.Title, Status: t.Status})
	}
	if st.Goal != nil {
		r.Goal = &protocol.ReportGoal{Condition: st.Goal.Condition, State: st.Goal.State, Reason: st.Goal.Reason}
	}
	for _, e := range h.s.Events() {
		if e.Type != protocol.EventCheck {
			continue
		}
		c := protocol.MustData[protocol.CheckData](e)
		r.Checks = append(r.Checks, protocol.ReportCheck{Name: c.Name, Status: c.Status, Summary: c.Summary})
	}
	f := m.deps.Modules.Report(ctx, h)
	r.FilesTouched, r.Commits, r.TreeDirty = f.FilesTouched, f.Commits, f.TreeDirty
	r.Checks = append(r.Checks, f.Checks...)
	_, err := h.s.Append(protocol.EventReport, r)
	return err
}

// ---------------------------------------------------------------- core impl

// invokeTool logs the call, gates it, runs it, logs the result, and notifies
// observers. Every tool call in nabu goes through here, model or module.
func (m *Manager) invokeTool(ctx context.Context, h *sessionHandle, call protocol.ToolCallData) protocol.ToolResultData {
	if len(call.Arguments) == 0 {
		call.Arguments = json.RawMessage(`{}`)
	}
	if _, err := h.s.Append(protocol.EventToolCall, call); err != nil {
		m.log.Error("tool call append failed", "session", h.ID(), "err", err)
		return protocol.ToolResultData{CallID: call.CallID, Tool: call.Tool, Content: err.Error(), Status: "error"}
	}
	res := m.executeTool(ctx, h, call)
	if _, err := h.s.Append(protocol.EventToolResult, res); err != nil {
		m.log.Error("tool result append failed", "session", h.ID(), "err", err)
	}
	m.deps.Modules.ToolResult(ctx, h, call, res)
	return res
}

func (m *Manager) executeTool(ctx context.Context, h *sessionHandle, call protocol.ToolCallData) protocol.ToolResultData {
	fail := func(msg string) protocol.ToolResultData {
		return protocol.ToolResultData{CallID: call.CallID, Tool: call.Tool, Content: msg, Status: "error"}
	}
	tool, ok := m.toolsByName[call.Tool]
	if !ok {
		return fail(fmt.Sprintf("unknown tool %q; available: %s", call.Tool, strings.Join(m.toolNames(), ", ")))
	}
	v := m.deps.Modules.GateTool(ctx, h, call)
	switch v.Decision {
	case module.Deny:
		return fail("denied: " + v.Reason)
	case module.Ask:
		if h.State().Options.PermissionMode != protocol.PermissionBypass {
			if m.deps.Asker == nil {
				return fail("denied: this call needs approval and no client is attached")
			}
			summary := v.Summary
			if summary == "" {
				summary = call.Tool
			}
			approved, reason := m.deps.Asker.Permission(ctx, h.ID(), call, summary, v.Risk)
			if !approved {
				if reason == "" {
					reason = "not approved"
				}
				return fail("denied by the user: " + reason)
			}
		}
	}
	out, err := tool.Run(ctx, h, call.Arguments)
	if err != nil {
		msg := err.Error()
		if strings.TrimSpace(out) != "" {
			msg = out
		}
		return fail(msg)
	}
	return protocol.ToolResultData{CallID: call.CallID, Tool: call.Tool, Content: out, Status: "ok"}
}

// complete runs a module's model call through the provider layer, so
// max_in_flight and usage accounting hold for judges and curators too.
func (m *Manager) complete(ctx context.Context, h *sessionHandle, req module.CompletionRequest) (module.CompletionResponse, error) {
	modelStr := req.Model
	if modelStr == "" {
		modelStr = h.State().Options.Model
	}
	p, name, _, err := m.deps.Providers.Resolve(modelStr)
	if err != nil {
		return module.CompletionResponse{}, err
	}
	var msgs []provider.Message
	if req.System != "" {
		msgs = append(msgs, provider.Message{Role: "system", Content: req.System})
	}
	for _, mm := range req.Messages {
		msgs = append(msgs, provider.Message{Role: mm.Role, Content: mm.Content})
	}
	resp, err := p.Complete(ctx, provider.Request{Model: name, Messages: msgs, MaxTokens: req.MaxTokens}, nil)
	if err != nil {
		return module.CompletionResponse{}, err
	}
	return module.CompletionResponse{Content: resp.Content, Usage: resp.Usage}, nil
}

func (m *Manager) ask(ctx context.Context, h *sessionHandle, question string, choices []string) (string, error) {
	if m.deps.Asker == nil {
		return "", fmt.Errorf("no client is attached to answer")
	}
	return m.deps.Asker.Ask(ctx, h.ID(), question, choices)
}

func (m *Manager) dataDir(name string) (string, error) {
	d := filepath.Join(m.deps.Root, "modules", name)
	return d, os.MkdirAll(d, 0o755)
}

// runLoop is implemented in runner.go; this declaration documents the contract.
// It returns when the turn sequence ends for any reason; it never appends a
// running state itself.
```

- [x] **Step 5: Add `ModuleConfig` to Config**

Append to `daemon/agent/config.go`:

```go
// Modules holds per-module config sections, keyed by module name.
// Set by the daemon from config.toml.
type Modules map[string]map[string]any
```

and add the field plus accessor:

```go
	// ModuleConfigs are the [modules.<name>] sections.
	ModuleConfigs Modules
```

```go
// ModuleConfig returns the section for a module; a missing section is empty,
// which means "on, with defaults".
func (c Config) ModuleConfig(name string) module.Config {
	if c.ModuleConfigs == nil {
		return module.Config{}
	}
	return module.Config(c.ModuleConfigs[name])
}
```

Add `"github.com/corporealshift/nabu/daemon/module"` to the config.go imports.

- [x] **Step 6: Add the stub loop so the package compiles**

Create `daemon/agent/runner.go`:

```go
package agent

import "context"

// runLoop drives one session's turns. Task 14 implements it; this stub lets
// Task 13 compile and its lifecycle tests run.
func (m *Manager) runLoop(ctx context.Context, h *sessionHandle) {
	_ = ctx
	_ = h
}
```

With the stub, `Prompt` leaves the session in `running`; `TestPromptOnTerminalSessionIsRejected`
calls `WaitIdle` (which returns at once because the goroutine finishes) and then `Stop`,
which appends `completed` and the report. That test passes with the stub.

- [x] **Step 7: Run the tests**

Run: `go test ./daemon/agent/ -count=1 -v`
Expected: every test in the file PASSes.

- [x] **Step 8: Run the gate and commit**

```bash
go build ./... && go vet ./... && go test ./... -count=1
git add daemon/agent/manager.go daemon/agent/manager_test.go daemon/agent/config.go daemon/agent/runner.go daemon/agent/testlog_test.go
git commit -m "agent: manager, tool invocation, and session lifecycle"
```

---

### Task 14: The turn loop — stream, tools, repeat

**Files:**
- Modify: `daemon/agent/runner.go` (replace the stub)
- Create: `daemon/agent/runner_test.go`

- [x] **Step 1: Write the failing test**

`daemon/agent/runner_test.go`:

```go
package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/protocol"
)

func TestLoopRunsToolsThenStopsIdle(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "write", Arguments: json.RawMessage(`{"path":"out.txt","content":"hi"}`)}}},
		{Content: "Wrote the file."},
	})
	s := h.create(t)
	if _, err := h.m.Prompt(context.Background(), s.ID(), "write out.txt"); err != nil {
		t.Fatal(err)
	}
	h.m.WaitIdle(s.ID())

	if b, err := os.ReadFile(filepath.Join(h.dir, "out.txt")); err != nil || string(b) != "hi" {
		t.Fatalf("tool did not run: %v %q", err, b)
	}
	st := s.State()
	if st.State != protocol.StateIdle || st.Turns != 2 {
		t.Fatalf("state=%s turns=%d", st.State, st.Turns)
	}
	var kinds []string
	for _, e := range s.Events() {
		kinds = append(kinds, string(e.Type))
	}
	got := strings.Join(kinds, ",")
	want := "session,message,state_change,message,tool_call,tool_result,message,state_change"
	if got != want {
		t.Fatalf("event order:\n%s\nwant:\n%s", got, want)
	}
	// The second request must carry the tool result.
	req := h.fake.Calls[1]
	if req.Messages[len(req.Messages)-1].Role != "tool" || req.Messages[len(req.Messages)-1].Content != "wrote 2 bytes to out.txt" {
		t.Fatalf("second request tail: %+v", req.Messages[len(req.Messages)-1])
	}
	if len(req.Tools) == 0 {
		t.Fatal("tools must be offered")
	}
}

func TestLoopRecordsUsageAndStreamsDeltas(t *testing.T) {
	var deltas []string
	h := newHarness(t, nil, []provider.Response{
		{Content: "Hello", Usage: protocol.Usage{InputTokens: 42, OutputTokens: 7, CachedTokens: 40}},
	})
	h.m.deps.Deltas = func(_, _, text string) { deltas = append(deltas, text) }
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "hi")
	h.m.WaitIdle(s.ID())

	st := s.State()
	if st.Usage.InputTokens != 42 || st.Usage.OutputTokens != 7 || st.Usage.CachedTokens != 40 {
		t.Fatalf("usage: %+v", st.Usage)
	}
	if strings.Join(deltas, "") != "Hello" {
		t.Fatalf("deltas: %v", deltas)
	}
	// Deltas are never logged.
	for _, e := range s.Events() {
		if e.Type == protocol.EventContext {
			t.Fatal("deltas must not become events")
		}
	}
}

func TestLoopSurvivesToolErrors(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "read", Arguments: json.RawMessage(`{"path":"missing.txt"}`)}}},
		{Content: "That file does not exist."},
	})
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "read missing.txt")
	h.m.WaitIdle(s.ID())

	if s.State().State != protocol.StateIdle {
		t.Fatalf("state: %s", s.State().State)
	}
	var res protocol.ToolResultData
	for _, e := range s.Events() {
		if e.Type == protocol.EventToolResult {
			res = *protocol.MustData[protocol.ToolResultData](e)
		}
	}
	if res.Status != "error" {
		t.Fatalf("result: %+v", res)
	}
}

func TestProviderErrorEndsTheSessionInError(t *testing.T) {
	h := newHarness(t, nil, nil) // empty script: the first call errors
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "hi")
	h.m.WaitIdle(s.ID())

	st := s.State()
	if st.State != protocol.StateError {
		t.Fatalf("state: %s", st.State)
	}
	ev := s.Events()
	if ev[len(ev)-1].Type != protocol.EventReport {
		t.Fatal("an error must still produce a report")
	}
	if protocol.MustData[protocol.ReportData](ev[len(ev)-1]).ExitStatus != protocol.StateError {
		t.Fatal("report must say error")
	}
	var sawNotice bool
	for _, e := range ev {
		if e.Type == protocol.EventNotice && strings.Contains(protocol.MustData[protocol.NoticeData](e).Message, "no scripted response") {
			sawNotice = true
		}
	}
	if !sawNotice {
		t.Fatal("the provider error must be recorded as a notice")
	}
}

func TestInterruptCancelsTheTurnAndReturnsToIdle(t *testing.T) {
	release := make(chan struct{})
	h := newHarness(t, nil, []provider.Response{{Content: "never sent"}})
	h.fake.BlockOn = release
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "long job")

	// Wait until the provider call is in flight.
	deadline := time.Now().Add(2 * time.Second)
	for h.fake.CallCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := h.m.Interrupt(context.Background(), s.ID()); err != nil {
		t.Fatal(err)
	}
	close(release)

	st := s.State()
	if st.State != protocol.StateIdle {
		t.Fatalf("state: %s", st.State)
	}
	ev := s.Events()
	last := ev[len(ev)-1]
	if last.Type != protocol.EventStateChange || protocol.MustData[protocol.StateChangeData](last).Reason != "interrupted" {
		t.Fatalf("last event: %+v", last)
	}
}

func TestPromptDuringRunIsPickedUpNextTurn(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "glob", Arguments: json.RawMessage(`{"pattern":"*.none"}`)}}},
		{Content: "done"},
	})
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "first")
	h.m.WaitIdle(s.ID())
	// Both prompts appear in the log; the second request sees the first.
	if h.fake.CallCount() != 2 {
		t.Fatalf("calls: %d", h.fake.CallCount())
	}
	if h.fake.Calls[1].Messages[1].Content != "first" {
		t.Fatalf("history not carried: %+v", h.fake.Calls[1].Messages[1])
	}
}
```

- [x] **Step 2: Run to see it fail**

Run: `go test ./daemon/agent/ -run Loop`
Expected: FAIL — the stub loop never calls the provider, so `WaitIdle` returns with the
session still `running`.

- [x] **Step 3: Implement the loop**

Replace `daemon/agent/runner.go` entirely:

```go
package agent

import (
	"context"
	"fmt"

	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/protocol"
)

// runLoop drives a session's turns until it stops, pauses, blocks, or fails.
// Every transition it makes is an appended event; it never mutates state in
// memory. It is the only caller of the provider for a session's own turns.
func (m *Manager) runLoop(ctx context.Context, h *sessionHandle) {
	for {
		if ctx.Err() != nil {
			return // Interrupt/Shutdown appends the state change
		}
		cont, err := m.turn(ctx, h)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			h.s.Append(protocol.EventNotice, protocol.NoticeData{
				Source: "daemon", Level: "error", Message: err.Error()})
			m.finish(ctx, h, protocol.StateError, "provider error")
			return
		}
		if !cont {
			return
		}
	}
}

// turn performs one model round trip plus any tool calls it requested.
// It returns true when the loop should continue.
func (m *Manager) turn(ctx context.Context, h *sessionHandle) (bool, error) {
	log := h.s.Events()
	st := protocol.Project(log)

	p, modelName, pcfg, err := m.deps.Providers.Resolve(st.Options.Model)
	if err != nil {
		return false, err
	}

	// Suffix blocks modules want in front of the model this turn.
	m.appendContexts(h, m.deps.Modules.BeforeRequest(ctx, h))
	log = h.s.Events()

	req := buildRequest(log, m.cfg.SystemPrompt, modelName, m.toolSpecs, m.cfg.MaxTokens)
	turnID := protocol.NewULID()
	onDelta := func(text string) {
		if m.deps.Deltas != nil {
			m.deps.Deltas(h.ID(), turnID, text)
		}
	}
	resp, err := p.Complete(ctx, req, onDelta)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, fmt.Errorf("model call failed: %w", err)
	}

	msg := protocol.MessageData{Role: "assistant", Content: resp.Content}
	if resp.Usage != (protocol.Usage{}) {
		u := resp.Usage
		msg.Usage = &u
	}
	if _, err := h.s.Append(protocol.EventMessage, msg); err != nil {
		return false, err
	}
	m.deps.Modules.TurnEnd(ctx, h)

	if len(resp.ToolCalls) > 0 {
		for _, tc := range resp.ToolCalls {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			m.invokeTool(ctx, h, protocol.ToolCallData{
				CallID: tc.ID, Tool: tc.Name, Arguments: tc.Arguments, Source: "model"})
		}
		return m.afterTurn(ctx, h, pcfg)
	}

	// No tool calls: the model believes it is finished. Task 15 asks the stop
	// gate here; until then the turn simply ends.
	return false, m.toIdle(ctx, h, "turn_complete")
}

// afterTurn runs between turns: compaction checks live here in Task 16.
func (m *Manager) afterTurn(ctx context.Context, h *sessionHandle, pcfg provider.Config) (bool, error) {
	return true, nil
}

// toIdle returns the session to idle.
func (m *Manager) toIdle(ctx context.Context, h *sessionHandle, reason string) error {
	from := h.State().State
	if from == protocol.StateIdle {
		return nil
	}
	_, err := h.s.Append(protocol.EventStateChange, protocol.StateChangeData{
		From: &from, To: protocol.StateIdle, Reason: reason})
	return err
}
```

Drop `"errors"` from the import block: nothing in runner.go uses it.

- [x] **Step 4: Handle the interrupted partial message**

The interrupt test expects the last event to be the state change appended by
`Interrupt`. The loop must not append a partial assistant message when the provider
call was cancelled, because the fake returns `ctx.Err()` with no content. Providers
that already streamed text are handled in P1b when real cancellation mid-stream is
wired to the API; the spec's `interrupted: true` field is populated there. Confirm the
current behaviour matches the test: on `ctx.Err()` the loop returns without appending.

- [x] **Step 5: Run the tests**

Run: `go test ./daemon/agent/ -count=1 -v`
Expected: every test PASSes, including the Task 13 ones.

- [x] **Step 6: Run the gate and commit**

```bash
go build ./... && go vet ./... && go test ./... -count=1
git add daemon/agent/runner.go daemon/agent/runner_test.go
git commit -m "agent: the turn loop with streaming, tools, and usage"
```

---

### Task 15: Stop gate, vetoes, progress detector, budget

**Files:**
- Create: `daemon/agent/stop.go`
- Modify: `daemon/agent/runner.go` (call the gate; add the budget check)
- Create: `daemon/agent/stop_test.go`

- [x] **Step 1: Write the failing test**

`daemon/agent/stop_test.go`:

```go
package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/protocol"
)

// vetoTimes vetoes the first n stop attempts, then allows.
type vetoTimes struct {
	n      int
	reason string
	seen   int
	info   module.StopInfo
}

func (v *vetoTimes) Name() string                          { return "vetoer" }
func (v *vetoTimes) Init(module.Host, module.Config) error { return nil }
func (v *vetoTimes) BeforeStop(_ context.Context, _ module.Session, info module.StopInfo) module.StopVerdict {
	v.seen++
	v.info = info
	if v.seen <= v.n {
		return module.StopVerdict{Allow: false, Reason: v.reason}
	}
	return module.StopVerdict{Allow: true}
}

func TestVetoFeedsTheReasonBackAndTheLoopContinues(t *testing.T) {
	v := &vetoTimes{n: 1, reason: "working tree is dirty"}
	h := newHarness(t, []module.Module{v}, []provider.Response{
		{Content: "All done."},
		{Content: "Committed; done now."},
	})
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "do it")
	h.m.WaitIdle(s.ID())

	st := s.State()
	if st.State != protocol.StateIdle || st.Turns != 2 {
		t.Fatalf("state=%s turns=%d", st.State, st.Turns)
	}
	var vetoes int
	for _, e := range s.Events() {
		if e.Type == protocol.EventStopVeto {
			vetoes++
			if protocol.MustData[protocol.StopVetoData](e).Module != "vetoer" {
				t.Fatal("veto must name the module")
			}
		}
	}
	if vetoes != 1 {
		t.Fatalf("veto events: %d", vetoes)
	}
	// The second request carries the veto as a trailing user message.
	tail := h.fake.Calls[1].Messages[len(h.fake.Calls[1].Messages)-1]
	if tail.Role != "user" || !strings.Contains(tail.Content, "working tree is dirty") {
		t.Fatalf("veto not fed back: %+v", tail)
	}
	// StopInfo carried the last assistant message.
	if v.info.LastAssistantMessage != "All done." || v.info.TurnsSinceUser != 1 {
		t.Fatalf("stop info: %+v", v.info)
	}
}

func TestRepeatedIdenticalVetoesBlockTheSession(t *testing.T) {
	v := &vetoTimes{n: 99, reason: "tests still fail"}
	h := newHarness(t, []module.Module{v}, []provider.Response{
		{Content: "done"}, {Content: "done"}, {Content: "done"}, {Content: "done"}, {Content: "done"},
	})
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "fix it")
	h.m.WaitIdle(s.ID())

	st := s.State()
	if st.State != protocol.StateBlocked {
		t.Fatalf("state: %s", st.State)
	}
	if st.Turns != 3 {
		t.Fatalf("the progress detector must stop after 3 identical rounds, got %d", st.Turns)
	}
	ev := s.Events()
	if ev[len(ev)-1].Type != protocol.EventReport {
		t.Fatal("blocked must emit a report")
	}
	r := protocol.MustData[protocol.ReportData](ev[len(ev)-1])
	if r.ExitStatus != protocol.StateBlocked {
		t.Fatalf("report: %+v", r)
	}
}

func TestToolCallsResetTheProgressDetector(t *testing.T) {
	v := &vetoTimes{n: 99, reason: "not yet"}
	h := newHarness(t, []module.Module{v}, []provider.Response{
		{Content: "done"},
		{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "glob", Arguments: json.RawMessage(`{"pattern":"*.none"}`)}}},
		{Content: "done"},
		{Content: "done"},
		{Content: "done"},
	})
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())
	if s.State().State != protocol.StateBlocked {
		t.Fatalf("state: %s", s.State().State)
	}
	// Turn 1 veto, turn 2 tools (reset), turns 3-5 vetoes → blocked on turn 5.
	if s.State().Turns != 5 {
		t.Fatalf("turns: %d", s.State().Turns)
	}
}

func TestBudgetPausesBeforeTheNextTurn(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{
		{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "glob", Arguments: json.RawMessage(`{"pattern":"*.none"}`)}}},
		{Content: "reached only after Resume raises the budget"},
	})
	s := h.create(t)
	if _, err := h.m.SetBudget(context.Background(), s.ID(), protocol.BudgetData{MaxTurns: 1, Source: "client"}); err != nil {
		t.Fatal(err)
	}
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())

	st := s.State()
	if st.State != protocol.StatePaused || st.Turns != 1 {
		t.Fatalf("state=%s turns=%d", st.State, st.Turns)
	}
	if h.fake.CallCount() != 1 {
		t.Fatalf("budget must stop the loop before the second call, got %d", h.fake.CallCount())
	}
	ev := s.Events()
	r := protocol.MustData[protocol.ReportData](ev[len(ev)-1])
	if r.ExitStatus != protocol.StatePaused {
		t.Fatalf("report: %+v", r)
	}
	// Resume with a bigger budget finishes the work.
	if err := h.m.Resume(context.Background(), s.ID(), &protocol.BudgetData{MaxTurns: 5, Source: "client"}); err != nil {
		t.Fatal(err)
	}
	h.m.WaitIdle(s.ID())
	if s.State().State != protocol.StateIdle {
		t.Fatalf("after resume: %s", s.State().State)
	}
}

func TestVetoesAreClearedByTheNextTurn(t *testing.T) {
	v := &vetoTimes{n: 1, reason: "once"}
	h := newHarness(t, []module.Module{v}, []provider.Response{{Content: "a"}, {Content: "b"}})
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())
	// After the second assistant message there are no outstanding vetoes.
	if got := protocol.OutstandingVetoes(s.Events()); len(got) != 0 {
		t.Fatalf("outstanding: %+v", got)
	}
}
```

- [x] **Step 2: Run to see it fail**

Run: `go test ./daemon/agent/ -run 'Veto|Budget|Progress|Blocked'`
Expected: FAIL — the loop goes idle without asking the gate.

- [x] **Step 3: Implement `stop.go`**

```go
package agent

import (
	"context"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// stopInfo gathers what a StopGate needs without a second round trip.
func stopInfo(log []protocol.Event, st protocol.State) module.StopInfo {
	info := module.StopInfo{Goal: st.Goal, Tasks: st.Tasks}
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Type != protocol.EventMessage {
			continue
		}
		d := protocol.MustData[protocol.MessageData](log[i])
		if d.Role == "assistant" {
			if info.LastAssistantMessage == "" {
				info.LastAssistantMessage = d.Content
			}
			info.TurnsSinceUser++
			continue
		}
		break // a user message ends the streak
	}
	info.VetoCount = len(vetoRounds(log))
	return info
}

// vetoRounds returns the reason sets of the consecutive trailing rounds in
// which the model produced no tool calls and a gate vetoed. A tool call or a
// new user message resets the streak, because both are progress.
func vetoRounds(log []protocol.Event) [][]string {
	var rounds [][]string
	var cur []string
	open := false
	closeRound := func() {
		if open && len(cur) > 0 {
			rounds = append(rounds, cur)
		}
		cur, open = nil, false
	}
	reset := func() {
		rounds, cur, open = nil, nil, false
	}
	for _, e := range log {
		switch e.Type {
		case protocol.EventMessage:
			d := protocol.MustData[protocol.MessageData](e)
			if d.Role == "assistant" {
				closeRound()
				open = true
			} else {
				reset()
			}
		case protocol.EventToolCall:
			reset()
		case protocol.EventStopVeto:
			if open {
				cur = append(cur, protocol.MustData[protocol.StopVetoData](e).Reason)
			}
		}
	}
	closeRound()
	return rounds
}

// sameReasons reports whether two veto reason sets are equal as ordered lists.
func sameReasons(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// stalled reports whether adding this round's vetoes would make n consecutive
// rounds with an unchanged set of reasons.
func stalled(log []protocol.Event, reasons []string, n int) bool {
	if n < 2 {
		return false
	}
	rounds := vetoRounds(log)
	if len(rounds) < n-1 {
		return false
	}
	for _, r := range rounds[len(rounds)-(n-1):] {
		if !sameReasons(r, reasons) {
			return false
		}
	}
	return true
}

// askStopGate runs every StopGate. It returns true when the loop should take
// another turn, having logged the vetoes; false when the session is finished
// (idle, or blocked because nothing is changing).
func (m *Manager) askStopGate(ctx context.Context, h *sessionHandle) (bool, error) {
	log := h.s.Events()
	st := protocol.Project(log)
	vetoes := m.deps.Modules.BeforeStop(ctx, h, stopInfo(log, st))
	if len(vetoes) == 0 {
		return false, m.toIdle(ctx, h, "turn_complete")
	}
	reasons := make([]string, 0, len(vetoes))
	for _, v := range vetoes {
		reasons = append(reasons, v.Reason)
	}
	if stalled(log, reasons, m.cfg.NoProgressTurns) {
		for _, v := range vetoes {
			h.s.Append(protocol.EventStopVeto, v)
		}
		h.s.Append(protocol.EventNotice, protocol.NoticeData{Source: "daemon", Level: "warn",
			Message: "stopping: the same objections stood for " +
				itoa(m.cfg.NoProgressTurns) + " turns with no tool use"})
		return false, m.finish(ctx, h, protocol.StateBlocked, "no progress against outstanding vetoes")
	}
	if len(vetoRounds(log))+1 > m.cfg.MaxConsecutiveVetoes {
		for _, v := range vetoes {
			h.s.Append(protocol.EventStopVeto, v)
		}
		h.s.Append(protocol.EventNotice, protocol.NoticeData{Source: "daemon", Level: "warn",
			Message: "stopping: " + itoa(m.cfg.MaxConsecutiveVetoes) + " consecutive veto rounds"})
		return false, m.finish(ctx, h, protocol.StateBlocked, "veto limit reached")
	}
	for _, v := range vetoes {
		if _, err := h.s.Append(protocol.EventStopVeto, v); err != nil {
			return false, err
		}
	}
	return true, nil
}

// itoa avoids importing strconv for two call sites.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// budgetExceeded reports whether the active budget forbids another turn.
func budgetExceeded(st protocol.State) (bool, string) {
	b := st.Budget
	if b.MaxTurns > 0 && st.Turns >= b.MaxTurns {
		return true, "budget: " + itoa(st.Turns) + " of " + itoa(b.MaxTurns) + " turns used"
	}
	if b.MaxTokens > 0 && st.Usage.InputTokens+st.Usage.OutputTokens >= b.MaxTokens {
		return true, "budget: token cap reached"
	}
	return false, ""
}
```

- [x] **Step 4: Wire the gate and the budget into the loop**

In `daemon/agent/runner.go`, replace the no-tool-call branch:

```go
	// No tool calls: the model believes it is finished. Nothing stops until
	// every stop gate agrees (spec §10.1).
	return m.askStopGate(ctx, h)
```

and add the budget check at the top of `turn`, right after `st := protocol.Project(log)`:

```go
	if over, why := budgetExceeded(st); over {
		h.s.Append(protocol.EventNotice, protocol.NoticeData{Source: "daemon", Level: "warn", Message: why})
		return false, m.finish(ctx, h, protocol.StatePaused, "budget")
	}
```

- [x] **Step 5: Run the tests**

Run: `go test ./daemon/agent/ -count=1 -v`
Expected: every test PASSes.

- [x] **Step 6: Run the gate and commit**

```bash
go build ./... && go vet ./... && go test ./... -count=1
git add daemon/agent/stop.go daemon/agent/stop_test.go daemon/agent/runner.go
git commit -m "agent: stop gate, veto feedback, progress detector, budget"
```

---

### Task 16: Compaction — clear results, then summarize

**Files:**
- Create: `daemon/agent/compaction.go`
- Modify: `daemon/agent/runner.go` (`afterTurn` calls it)
- Create: `daemon/agent/compaction_test.go`

- [x] **Step 1: Write the failing test**

`daemon/agent/compaction_test.go`:

```go
package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/protocol"
)

// bigUsage reports input token usage at the given fraction of an 8000-token
// window, so the test can drive the thresholds.
func bigUsage(frac float64) protocol.Usage {
	return protocol.Usage{InputTokens: int(8000 * frac), OutputTokens: 10}
}

func globCall(id string) []provider.ToolCall {
	return []provider.ToolCall{{ID: id, Name: "glob", Arguments: json.RawMessage(`{"pattern":"*.none"}`)}}
}

func TestClearResultsFiresAtTheLowerThreshold(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{
		{ToolCalls: globCall("c1"), Usage: bigUsage(0.10)},
		{ToolCalls: globCall("c2"), Usage: bigUsage(0.75)}, // crosses 0.70
		{Content: "done", Usage: bigUsage(0.20)},
	})
	h.m.cfg.Compaction.KeepTurns = 1
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())

	var modes []string
	for _, e := range s.Events() {
		if e.Type == protocol.EventCompaction {
			modes = append(modes, string(protocol.MustData[protocol.CompactionData](e).Mode))
		}
	}
	if strings.Join(modes, ",") != "clear_results" {
		t.Fatalf("compactions: %v", modes)
	}
	// The last request must carry a stub in place of the first tool result.
	last := h.fake.Calls[len(h.fake.Calls)-1]
	var stubs int
	for _, msg := range last.Messages {
		if msg.Role == "tool" && strings.HasPrefix(msg.Content, "[result cleared:") {
			stubs++
		}
	}
	if stubs != 1 {
		t.Fatalf("expected one cleared result, got %d: %+v", stubs, last.Messages)
	}
}

// preserver asks the summary to keep a string and re-injects a prefix block.
type preserver struct{ before, after int }

func (p *preserver) Name() string                          { return "preserver" }
func (p *preserver) Init(module.Host, module.Config) error { return nil }
func (p *preserver) BeforeCompaction(context.Context, module.Session, module.Range) []string {
	p.before++
	return []string{"the user's deploy key is never to be printed"}
}
func (p *preserver) AfterCompaction(context.Context, module.Session) ([]module.ContextBlock, error) {
	p.after++
	return []module.ContextBlock{{Slot: "prefix", Content: "# Skills (re-injected)"}}, nil
}

func TestSummarizeFiresAtTheUpperThreshold(t *testing.T) {
	p := &preserver{}
	h := newHarness(t, []module.Module{p}, []provider.Response{
		{ToolCalls: globCall("c1"), Usage: bigUsage(0.90)}, // crosses 0.85
		{Content: "THE SUMMARY"},                            // the summarizer call
		{Content: "done", Usage: bigUsage(0.10)},
	})
	s := h.create(t)
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())

	var cd *protocol.CompactionData
	for _, e := range s.Events() {
		if e.Type == protocol.EventCompaction {
			d := protocol.MustData[protocol.CompactionData](e)
			if d.Mode == protocol.CompactionSummarize {
				cd = d
			}
		}
	}
	if cd == nil || cd.Summary != "THE SUMMARY" {
		t.Fatalf("summarize event: %+v", cd)
	}
	if p.before != 1 || p.after != 1 {
		t.Fatalf("hooks: before=%d after=%d", p.before, p.after)
	}
	// The summarizer prompt carried the preserve string.
	sum := h.fake.Calls[1]
	var joined strings.Builder
	for _, msg := range sum.Messages {
		joined.WriteString(msg.Content)
	}
	if !strings.Contains(joined.String(), "deploy key") {
		t.Fatalf("preserve string missing from the summary prompt: %s", joined.String())
	}
	// The final request rebuilds from the summary plus the re-injected prefix.
	last := h.fake.Calls[len(h.fake.Calls)-1]
	sys := last.Messages[0].Content
	if !strings.Contains(sys, "THE SUMMARY") || !strings.Contains(sys, "# Skills (re-injected)") || !strings.Contains(sys, "## Current state") {
		t.Fatalf("final system message: %q", sys)
	}
	// Nothing was deleted from the log.
	var msgs int
	for _, e := range s.Events() {
		if e.Type == protocol.EventMessage {
			msgs++
		}
	}
	if msgs < 3 {
		t.Fatalf("compaction must not delete events, found %d messages", msgs)
	}
}

func TestCompactionDisabledHardStops(t *testing.T) {
	h := newHarness(t, nil, []provider.Response{
		{ToolCalls: globCall("c1"), Usage: bigUsage(0.95)},
	})
	s := h.create(t)
	if _, err := h.m.SetOption(context.Background(), s.ID(), "compaction_enabled", false); err != nil {
		t.Fatal(err)
	}
	h.m.Prompt(context.Background(), s.ID(), "go")
	h.m.WaitIdle(s.ID())

	st := s.State()
	if st.State != protocol.StateBlocked {
		t.Fatalf("state: %s", st.State)
	}
	var sawNotice bool
	for _, e := range s.Events() {
		if e.Type == protocol.EventNotice && strings.Contains(protocol.MustData[protocol.NoticeData](e).Message, "context limit") {
			sawNotice = true
		}
	}
	if !sawNotice {
		t.Fatal("a hard stop must say why")
	}
}
```

- [x] **Step 2: Run to see it fail**

Run: `go test ./daemon/agent/ -run 'Clear|Summarize|CompactionDisabled'`
Expected: FAIL — `afterTurn` returns without compacting.

- [x] **Step 3: Implement `compaction.go`**

```go
package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/protocol"
)

// summarizePrompt asks the model to compress the earlier conversation. It
// maximises recall first, as Anthropic's context-engineering guidance
// recommends: decisions, unresolved problems, and what was actually done.
const summarizePrompt = `Summarize the conversation so far for an agent that will continue the work with no other memory of it.

Keep, in this order:
1. What the user asked for, in their terms, including constraints they stated.
2. Decisions made and why, including approaches that were tried and rejected.
3. What has actually been done: files changed, commands run and their results.
4. What is unresolved: open questions, failing checks, known bugs.
5. Anything the next turn must not do.

Drop pleasantries, repeated tool output, and narration. Be specific: name files, commands and errors. Do not invent anything that is not in the conversation.`

// lastInputTokens returns the most recent assistant turn's input token count,
// which is the best available estimate of the current request size.
func lastInputTokens(log []protocol.Event) int {
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Type != protocol.EventMessage {
			continue
		}
		d := protocol.MustData[protocol.MessageData](log[i])
		if d.Role == "assistant" && d.Usage != nil {
			return d.Usage.InputTokens
		}
	}
	return 0
}

// maybeCompact runs between turns. It returns an error only when the session
// cannot continue.
func (m *Manager) maybeCompact(ctx context.Context, h *sessionHandle, pcfg provider.Config) error {
	if pcfg.ContextWindow <= 0 {
		return nil // unknown window: size-based compaction is disabled
	}
	log := h.s.Events()
	st := protocol.Project(log)
	used := float64(lastInputTokens(log)) / float64(pcfg.ContextWindow)

	if !st.Options.CompactionEnabled {
		if used >= m.cfg.Compaction.SummarizeAt {
			h.s.Append(protocol.EventNotice, protocol.NoticeData{Source: "daemon", Level: "error",
				Message: fmt.Sprintf("context limit reached (%.0f%% of %d tokens) and compaction is disabled for this session",
					used*100, pcfg.ContextWindow)})
			return m.finish(ctx, h, protocol.StateBlocked, "context limit with compaction disabled")
		}
		return nil
	}
	if used >= m.cfg.Compaction.SummarizeAt {
		return m.summarize(ctx, h, pcfg)
	}
	if used >= m.cfg.Compaction.ClearAt {
		return m.clearResults(ctx, h)
	}
	return nil
}

// clearResults is stage 0: stub out tool results older than KeepTurns
// assistant turns. The log keeps everything; only the request shrinks.
func (m *Manager) clearResults(ctx context.Context, h *sessionHandle) error {
	log := h.s.Events()
	cutoff := keepFrom(log, m.cfg.Compaction.KeepTurns)
	start, end := "", ""
	for i, e := range log {
		if i >= cutoff {
			break
		}
		if e.Type != protocol.EventToolResult || clearedAlready(log, e.ID) {
			continue
		}
		if start == "" {
			start = e.ID
		}
		end = e.ID
	}
	if start == "" {
		return nil // nothing left to clear
	}
	_, err := h.s.Append(protocol.EventCompaction, protocol.CompactionData{
		Mode: protocol.CompactionClearResults, RangeStart: start, RangeEnd: end})
	return err
}

// keepFrom returns the log index at which the last n assistant turns begin.
// Only stage 0 uses it: stage 1 summarizes everything, for the reason given
// on summarize below.
func keepFrom(log []protocol.Event, n int) int {
	if n < 1 {
		n = 1
	}
	seen := 0
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Type != protocol.EventMessage {
			continue
		}
		if protocol.MustData[protocol.MessageData](log[i]).Role != "assistant" {
			continue
		}
		seen++
		if seen == n {
			return i
		}
	}
	return 0
}

// clearedAlready reports whether an event id already falls in a clear_results
// range, so repeated stage-0 runs do not re-clear the same events.
func clearedAlready(log []protocol.Event, id string) bool {
	for _, e := range log {
		if e.Type != protocol.EventCompaction {
			continue
		}
		d := protocol.MustData[protocol.CompactionData](e)
		if d.Mode == protocol.CompactionClearResults && d.RangeStart <= id && id <= d.RangeEnd {
			return true
		}
	}
	return false
}

// summarize is stage 1: one model call compresses the conversation into a
// summary event, then modules re-inject their prefixes.
//
// The range must run to the last event before the compaction record, not to
// some earlier "keep the recent turns" cutoff. Request assembly (protocol spec
// §6) takes messages *after the last summarize compaction by log position*, so
// anything the range left out would be dropped from the request without ever
// reaching the summary. Everything up to the compaction point is summarized;
// the Current state block carries goal, tasks and budget across losslessly.
func (m *Manager) summarize(ctx context.Context, h *sessionHandle, pcfg provider.Config) error {
	log := h.s.Events()
	st := protocol.Project(log)
	start := 1 // never the session event
	if st.CompactedThrough != "" {
		for i, e := range log {
			if e.ID == st.CompactedThrough {
				start = i + 1
				break
			}
		}
	}
	if len(log)-start < 2 {
		return nil // not enough history to be worth a model call
	}
	rng := module.Range{Start: log[start].ID, End: log[len(log)-1].ID}
	preserve := m.deps.Modules.BeforeCompaction(ctx, h, rng)

	transcript := renderForSummary(log[start:])
	sys := summarizePrompt
	if len(preserve) > 0 {
		sys += "\n\nThe summary MUST preserve these facts verbatim:\n- " + strings.Join(preserve, "\n- ")
	}
	p, modelName, _, err := m.deps.Providers.Resolve(st.Options.Model)
	if err != nil {
		return err
	}
	resp, err := p.Complete(ctx, provider.Request{
		Model:    modelName,
		Messages: []provider.Message{{Role: "system", Content: sys}, {Role: "user", Content: transcript}},
	}, nil)
	if err != nil {
		// A failed summary is not fatal: fall back to clearing results.
		h.s.Append(protocol.EventNotice, protocol.NoticeData{Source: "daemon", Level: "warn",
			Message: "summarization failed (" + err.Error() + "); clearing tool results instead"})
		return m.clearResults(ctx, h)
	}
	summary := strings.TrimSpace(resp.Content)
	if summary == "" {
		summary = "(the summarizer returned nothing)"
	}
	if _, err := h.s.Append(protocol.EventCompaction, protocol.CompactionData{
		Mode: protocol.CompactionSummarize, RangeStart: rng.Start, RangeEnd: rng.End, Summary: summary}); err != nil {
		return err
	}
	m.appendContexts(h, m.deps.Modules.AfterCompaction(ctx, h))
	return nil
}

// renderForSummary turns events into plain text for the summarizer.
func renderForSummary(events []protocol.Event) string {
	var b strings.Builder
	for _, e := range events {
		switch e.Type {
		case protocol.EventMessage:
			d := protocol.MustData[protocol.MessageData](e)
			if strings.TrimSpace(d.Content) == "" {
				continue
			}
			fmt.Fprintf(&b, "%s: %s\n\n", d.Role, d.Content)
		case protocol.EventToolCall:
			d := protocol.MustData[protocol.ToolCallData](e)
			fmt.Fprintf(&b, "tool call %s(%s)\n", d.Tool, truncateText(string(d.Arguments), 400))
		case protocol.EventToolResult:
			d := protocol.MustData[protocol.ToolResultData](e)
			fmt.Fprintf(&b, "tool result [%s]: %s\n\n", d.Status, truncateText(d.Content, 1200))
		case protocol.EventCheck:
			d := protocol.MustData[protocol.CheckData](e)
			fmt.Fprintf(&b, "check %s: %s (%s)\n\n", d.Name, d.Status, d.Summary)
		}
	}
	return b.String()
}

func truncateText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf(" …(%d more bytes)", len(s)-max)
}
```

- [x] **Step 4: Call it from `afterTurn`**

In `daemon/agent/runner.go`, replace `afterTurn`:

```go
// afterTurn runs between turns: compaction happens here so the next request
// is built from an already-shrunk log.
func (m *Manager) afterTurn(ctx context.Context, h *sessionHandle, pcfg provider.Config) (bool, error) {
	if err := m.maybeCompact(ctx, h, pcfg); err != nil {
		return false, err
	}
	// maybeCompact may have ended the session (compaction disabled at the limit).
	if st := h.State().State; st != protocol.StateRunning {
		return false, nil
	}
	return true, nil
}
```

- [x] **Step 5: Run the tests**

Run: `go test ./daemon/agent/ -count=1 -v`
Expected: every test PASSes.

- [x] **Step 6: Run the gate and commit**

```bash
go build ./... && go vet ./... && go test ./... -count=1
git add daemon/agent/compaction.go daemon/agent/compaction_test.go daemon/agent/runner.go
git commit -m "agent: two-stage compaction as events"
```

---

### Task 17: Live smoke test against the local model, and close out P1a

**Files:**
- Create: `daemon/agent/live_test.go`
- Modify: `ARCHITECTURE.md` (the Verifying section)

- [x] **Step 1: Write the env-gated live test**

`daemon/agent/live_test.go`:

```go
package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/daemon/provider"
	"github.com/corporealshift/nabu/daemon/session"
	"github.com/corporealshift/nabu/daemon/tools"
	"github.com/corporealshift/nabu/protocol"
)

// TestLiveLocalModel drives the whole loop against a real OpenAI-compatible
// server. It is skipped unless NABU_LIVE_BASE_URL is set, so the default gate
// stays hermetic.
//
//	NABU_LIVE_BASE_URL=http://localhost:8033/v1 \
//	NABU_LIVE_MODEL=qwen3.6-35b-a3b \
//	go test ./daemon/agent/ -run Live -v -timeout 10m
func TestLiveLocalModel(t *testing.T) {
	base := os.Getenv("NABU_LIVE_BASE_URL")
	if base == "" {
		t.Skip("set NABU_LIVE_BASE_URL to run the live smoke test")
	}
	model := os.Getenv("NABU_LIVE_MODEL")
	if model == "" {
		model = "local-model"
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "greeting.txt"), []byte("hello from nabu\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := session.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	pr := provider.NewRegistry()
	pr.Add(provider.Config{
		Name: "local", BaseURL: base, MaxInFlight: 1, ContextWindow: 32768, Timeout: 5 * time.Minute,
	}, provider.NewOpenAI(provider.Config{Name: "local", BaseURL: base, MaxInFlight: 1, Timeout: 5 * time.Minute}, nil), true)

	builtins := &tools.Builtins{}
	mr := module.NewRegistry([]module.Module{builtins}, module.Options{Log: testLogger()})
	m, err := New(Deps{Store: store, Providers: pr, Modules: mr, Builtins: builtins, Root: dir, Log: testLogger()},
		Config{DefaultModel: "local/" + model})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown(context.Background())

	s, err := m.Create(context.Background(), dir, protocol.Options{PermissionMode: protocol.PermissionBypass})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetBudget(context.Background(), s.ID(), protocol.BudgetData{MaxTurns: 8, Source: "client"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Prompt(context.Background(), s.ID(),
		"Read the file greeting.txt in the workspace and tell me exactly what it says. Use the read tool."); err != nil {
		t.Fatal(err)
	}
	m.WaitIdle(s.ID())

	st := s.State()
	t.Logf("state=%s turns=%d usage=%+v", st.State, st.Turns, st.Usage)
	for _, e := range s.Events() {
		t.Logf("  %-14s %s", e.Type, summaryOf(e))
	}
	if st.State == protocol.StateError {
		t.Fatal("the live run ended in error; see the log above")
	}
	var readOK bool
	for _, e := range s.Events() {
		if e.Type == protocol.EventToolResult {
			d := protocol.MustData[protocol.ToolResultData](e)
			if d.Tool == "read" && d.Status == "ok" && strings.Contains(d.Content, "hello from nabu") {
				readOK = true
			}
		}
	}
	if !readOK {
		t.Fatal("the model never successfully read greeting.txt")
	}
	if st.Usage.InputTokens == 0 {
		t.Error("the provider reported no usage; compaction thresholds depend on it")
	}
}

func summaryOf(e protocol.Event) string {
	v, err := protocol.DecodeData(e)
	if err != nil {
		return err.Error()
	}
	switch d := v.(type) {
	case *protocol.MessageData:
		return d.Role + ": " + firstLine(d.Content)
	case *protocol.ToolCallData:
		return d.Tool + " " + firstLine(string(d.Arguments))
	case *protocol.ToolResultData:
		return d.Status + ": " + firstLine(d.Content)
	case *protocol.StateChangeData:
		return string(d.To) + " (" + d.Reason + ")"
	case *protocol.NoticeData:
		return d.Level + ": " + d.Message
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " …"
	}
	if len(s) > 120 {
		s = s[:120] + " …"
	}
	return s
}
```

- [x] **Step 2: Run the hermetic gate**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: all packages `ok`; the live test reports SKIP.

- [x] **Step 3: Run the live smoke test**

Confirm the model server is up first:

```bash
curl -s -m 3 http://localhost:8033/v1/models | head -c 200
```

Then, in Git Bash:

```bash
NABU_LIVE_BASE_URL=http://localhost:8033/v1 NABU_LIVE_MODEL=qwen3.6-35b-a3b \
  go test ./daemon/agent/ -run Live -v -timeout 10m
```

Expected: PASS, with a logged event list showing `message`, `tool_call read`,
`tool_result ok: hello from nabu`, a final `message`, and `state_change idle`.

If it fails, the likely causes in order: the server does not send `usage` in the
stream (check the logged usage line; set `ContextWindow: 0` in config to disable
size-based compaction until it does); the model emits tool calls in a non-standard
field (log the raw SSE by adding a temporary `t.Log` in `readStream`); or the model
ignores the tools entirely (try a more explicit prompt, and record the finding — that
is a real signal about which local model can drive nabu).

- [x] **Step 4: Record the result in ARCHITECTURE.md**

In `ARCHITECTURE.md`, replace the Verifying section with:

```markdown
## Verifying

```
go build ./... && go vet ./... && go test ./...
```

That command is the project gate. The conformance vectors under `protocol/vectors/`
are the central correctness artifact and run as part of `go test ./protocol/`.

A live smoke test drives the real agent loop against a local OpenAI-compatible
server. It is skipped unless the base URL is set:

```
NABU_LIVE_BASE_URL=http://localhost:8033/v1 NABU_LIVE_MODEL=qwen3.6-35b-a3b \
  go test ./daemon/agent/ -run Live -v -timeout 10m
```
```

- [x] **Step 5: Commit**

```bash
git add daemon/agent/live_test.go ARCHITECTURE.md
git commit -m "agent: live smoke test against a local OpenAI-compatible server"
```

- [x] **Step 6: Push the branch**

```bash
git push -u origin p1
```

---

## Done when

- `go build ./... && go vet ./... && go test ./...` is green with no skips other
  than the live test and, where git is absent, the workspace git test.
- A session created through `Manager.Create` and prompted through
  `Manager.Prompt` runs tools, records usage, and returns to `idle`.
- A stop-gate module can veto; the reason reaches the model as a trailing user
  message; three identical rounds end the session `blocked` with a report.
- A turn budget pauses the session with a report, and `Resume` continues it.
- Crossing 70 % of the context window logs a `clear_results` compaction and the
  next request carries stubs; crossing 85 % logs a `summarize` compaction whose
  summary and re-injected prefix appear in the next request, with nothing deleted
  from the log.
- The live smoke test passes against the local Qwen server.

## Not in this phase

- **P1b:** the WebSocket API (`daemon/api`), the `hello` handshake, subscriptions,
  delta fan-out, first-responder-wins permission requests, the `Asker`
  implementation, `~/.nabu/config.toml` loading, daemon lifecycle and auto-start,
  and the `cmd/nabu` subcommands (`daemon`, `run`, `status`, `attach`, `stop`,
  `resume`) with the documented exit codes. Windows job objects for bash process
  trees belong here too.
- **P1c:** the policy modules — `skills`, `guard`, `verify` (`require_done_when`,
  mechanical task checks, the open-task veto, the fresh-context goal judge, the
  workspace gate and dirty-tree veto) and `report` (files touched, commits, tree
  dirty) — plus their registration in `daemon/modules/all.go`.
- P2 onward: TUI, memory, Android, FCM, Rust GUI.

