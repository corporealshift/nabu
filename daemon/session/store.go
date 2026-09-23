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

	mu     sync.Mutex
	open   map[string]*Session
	lastID string // ensures session ids sort by creation order
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
func (st *Store) Create(workspace, key string, opts protocol.Options, contextWindow int) (*Session, error) {
	// Session ids order listings, so two sessions created in the same
	// millisecond must still sort by creation order.
	st.mu.Lock()
	id := protocol.NewULIDAfter(st.lastID)
	st.lastID = id
	st.mu.Unlock()
	path := filepath.Join(st.root, id+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	s := &Session{id: id, path: path, f: f, subs: map[int]chan protocol.Event{}}
	if _, err := s.Append(protocol.EventSession, protocol.SessionData{
		Workspace: workspace, WorkspaceKey: key, Options: opts,
		ContextWindow: contextWindow}); err != nil {
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

// Close closes every open session's file handle. The daemon calls it during
// shutdown; on Windows an open handle also blocks directory removal, so tests
// must call it too.
func (st *Store) Close() error {
	st.mu.Lock()
	sessions := make([]*Session, 0, len(st.open))
	for _, s := range st.open {
		sessions = append(sessions, s)
	}
	st.open = map[string]*Session{}
	st.mu.Unlock()
	var errs []error
	for _, s := range sessions {
		if err := s.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
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
	// Archived is set in an archived listing (spec 7.19).
	Archived bool `json:"archived,omitempty"`
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
// and reported together in the returned error, so one bad file cannot hide the
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
	// Newest first. Session ids are ULIDs, so this is reverse chronological,
	// which is what every caller wants from a list of sessions: the one you
	// were just working in is at the top.
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID > out[j].SessionID })
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
		if _, aerr := s.Append(protocol.EventNotice, protocol.NoticeData{
			Source: "daemon", Level: "warn",
			Message: "session was running when the daemon stopped; paused"}); aerr != nil {
			err = errors.Join(err, aerr)
			continue
		}
		from := protocol.StateRunning
		if _, aerr := s.Append(protocol.EventStateChange, protocol.StateChangeData{
			From: &from, To: protocol.StatePaused, Reason: "daemon_restart"}); aerr != nil {
			err = errors.Join(err, aerr)
			continue
		}
		paused = append(paused, sm.SessionID)
	}
	return paused, err
}

// archiveDir is where archived logs live: <root>/sessions/archive/.
func (st *Store) archiveDir() string { return filepath.Join(st.root, "archive") }

// Archive moves a session's log out of the active directory.
//
// Nothing in the log changes; it is only no longer listed, loaded at start, or
// mirrored by clients. The session is closed first, since an open file cannot
// be moved on Windows, and every subscription to it ends.
func (st *Store) Archive(id string) error {
	if protocol.ValidateULID(id) != nil {
		return ErrNotFound
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	from := filepath.Join(st.root, id+".jsonl")
	to := filepath.Join(st.archiveDir(), id+".jsonl")
	if _, err := os.Stat(from); errors.Is(err, os.ErrNotExist) {
		if _, err := os.Stat(to); err == nil {
			return nil // already archived
		}
		return ErrNotFound
	}
	if s, ok := st.open[id]; ok {
		if err := s.Close(); err != nil {
			return err
		}
		delete(st.open, id)
	}
	if err := os.MkdirAll(st.archiveDir(), 0o755); err != nil {
		return err
	}
	return os.Rename(from, to)
}

// Restore moves an archived session's log back, so it lists and loads again.
func (st *Store) Restore(id string) error {
	if protocol.ValidateULID(id) != nil {
		return ErrNotFound
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	from := filepath.Join(st.archiveDir(), id+".jsonl")
	to := filepath.Join(st.root, id+".jsonl")
	if _, err := os.Stat(from); errors.Is(err, os.ErrNotExist) {
		if _, err := os.Stat(to); err == nil {
			return nil // not archived
		}
		return ErrNotFound
	}
	return os.Rename(from, to)
}

// ListArchived summarises every archived session, newest first. Each is read
// and closed again: an archived session is looked at, not kept open.
func (st *Store) ListArchived() ([]Summary, error) {
	entries, err := os.ReadDir(st.archiveDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
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
		id := strings.TrimSuffix(name, ".jsonl")
		s, err := load(id, filepath.Join(st.archiveDir(), name))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		sum := s.Summary()
		sum.Archived = true
		_ = s.Close()
		out = append(out, sum)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID > out[j].SessionID })
	return out, errors.Join(errs...)
}

// ArchivedLogs reads every archived session's events, for anything that has to
// count history the active list no longer shows. Each is closed after reading.
func (st *Store) ArchivedLogs() ([][]protocol.Event, error) {
	entries, err := os.ReadDir(st.archiveDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out [][]protocol.Event
	var errs []error
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		s, err := load(strings.TrimSuffix(name, ".jsonl"), filepath.Join(st.archiveDir(), name))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, s.Events())
		_ = s.Close()
	}
	return out, errors.Join(errs...)
}
