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
// protocol data struct for t, or any JSON-marshalable equivalent. Nothing is
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
