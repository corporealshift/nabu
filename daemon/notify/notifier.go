package notify

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

// The kinds of notification (docs/specs/2026-10-06-phone-notifications-design.md).
const (
	KindQuestion = "question"
	KindResolved = "resolved"
	KindLabel    = "label"
	KindStopped  = "stopped"
	KindDone     = "done"
)

// Sender delivers one data message to one device. FCM is one.
type Sender interface {
	Send(ctx context.Context, device string, data map[string]string) error
}

// Rules is what is worth a notification: policy, from config.
type Rules struct {
	// Labels are the labels whose arrival on a session with no parent
	// notifies, matched as strings. The daemon gives them no meaning.
	Labels []string
	// DoneAfter is how long a turn must have run for its end to notify.
	DoneAfter time.Duration
}

// DefaultLabels are the run and goal outcomes worth a phone buzzing.
var DefaultLabels = []string{"run:done", "run:failed", "goal:done", "goal:blocked"}

// DefaultDoneAfter is the shortest turn whose end notifies: under a minute,
// the person was probably waiting for it.
const DefaultDoneAfter = 60 * time.Second

// Lookup is a session's current projection, for its state and task counts.
type Lookup func(sessionID string) (protocol.State, bool)

// item is one thing the notifier is told.
type item struct {
	sessionID string
	event     *protocol.Event
	// request is a daemon-to-client request opening (method set) or closing.
	request string
	method  string
}

// queueSize is how much the notifier holds before it drops. An append never
// waits on it.
const queueSize = 1024

// Notifier turns what happens in sessions into notifications for every
// registered phone. Tell it things from any goroutine; it decides and sends
// on its own.
type Notifier struct {
	rules   Rules
	devices *Devices
	sender  Sender
	lookup  Lookup
	log     *slog.Logger

	in chan item

	// Owned by the Run goroutine.
	parents   map[string]string
	turnStart map[string]time.Time
}

// New makes a notifier. sender may be nil: devices still register, and
// nothing is sent.
func New(rules Rules, devices *Devices, sender Sender, lookup Lookup, log *slog.Logger) *Notifier {
	if log == nil {
		log = slog.Default()
	}
	if rules.DoneAfter <= 0 {
		rules.DoneAfter = DefaultDoneAfter
	}
	return &Notifier{rules: rules, devices: devices, sender: sender, lookup: lookup, log: log,
		in: make(chan item, queueSize), parents: map[string]string{}, turnStart: map[string]time.Time{}}
}

// Observe takes one appended event. It never blocks: a full queue drops it.
func (n *Notifier) Observe(sessionID string, e protocol.Event) {
	n.offer(item{sessionID: sessionID, event: &e})
}

// RequestOpened takes a daemon-to-client request that waits on a person.
func (n *Notifier) RequestOpened(sessionID, requestID, method string) {
	n.offer(item{sessionID: sessionID, request: requestID, method: method})
}

// RequestClosed takes that request's end: answered, or timed out.
func (n *Notifier) RequestClosed(sessionID, requestID string) {
	n.offer(item{sessionID: sessionID, request: requestID})
}

// Register and Unregister keep the devices.
func (n *Notifier) Register(token, name string) error { return n.devices.Register(token, name) }
func (n *Notifier) Unregister(token string) error     { return n.devices.Unregister(token) }

func (n *Notifier) offer(it item) {
	select {
	case n.in <- it:
	default:
		n.log.Warn("notify: falling behind; dropped one", "session", it.sessionID)
	}
}

// Run decides and sends until ctx is done.
func (n *Notifier) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case it := <-n.in:
			for _, data := range n.decide(it) {
				n.send(ctx, data)
			}
		}
	}
}

// decide is what one item is worth: no notification, or one.
func (n *Notifier) decide(it item) []map[string]string {
	if it.event == nil {
		kind := KindResolved
		if it.method != "" {
			kind = KindQuestion
		}
		data := n.payload(kind, it.sessionID)
		data["request_id"] = it.request
		if it.method != "" {
			data["method"] = it.method
		}
		return []map[string]string{data}
	}

	e := *it.event
	switch e.Type {
	case protocol.EventSession:
		n.parents[it.sessionID] = protocol.MustData[protocol.SessionData](e).Options.Parent
		return nil
	case protocol.EventStateChange:
		d := protocol.MustData[protocol.StateChangeData](e)
		if d.To == protocol.StateRunning {
			n.turnStart[it.sessionID] = e.Timestamp
			return nil
		}
		start, known := n.turnStart[it.sessionID]
		delete(n.turnStart, it.sessionID)
		if d.From == nil || *d.From != protocol.StateRunning || n.parent(it.sessionID) != "" {
			return nil
		}
		switch {
		case d.To == protocol.StateBlocked || d.To == protocol.StateError || d.To == protocol.StatePaused:
			return []map[string]string{n.payload(KindStopped, it.sessionID)}
		case (d.To == protocol.StateIdle && d.Reason == "turn_complete") || d.To == protocol.StateCompleted:
			// A turn whose start was missed (a daemon restart) counts as long.
			if known && e.Timestamp.Sub(start) < n.rules.DoneAfter {
				return nil
			}
			return []map[string]string{n.payload(KindDone, it.sessionID)}
		}
	case protocol.EventOptionsChange:
		d := protocol.MustData[protocol.OptionsChangeData](e)
		if d.Key != "labels" || n.parent(it.sessionID) != "" {
			return nil
		}
		var from, to []string
		_ = json.Unmarshal(d.From, &from)
		_ = json.Unmarshal(d.To, &to)
		var out []map[string]string
		for _, l := range to {
			if !slices.Contains(from, l) && slices.Contains(n.rules.Labels, l) {
				data := n.payload(KindLabel, it.sessionID)
				data["label"] = l
				out = append(out, data)
			}
		}
		return out
	}
	return nil
}

// parent is a session's parent, from its first event or, for one created
// before the notifier saw it, from its projection. Parents never change.
func (n *Notifier) parent(id string) string {
	if p, ok := n.parents[id]; ok {
		return p
	}
	st, ok := n.lookup(id)
	if !ok {
		return ""
	}
	n.parents[id] = st.Options.Parent
	return st.Options.Parent
}

// payload is a notification's data: identifiers, the state and task counts.
// Never text from the log (architecture §16).
func (n *Notifier) payload(kind, id string) map[string]string {
	data := map[string]string{"kind": kind, "session_id": id}
	if st, ok := n.lookup(id); ok {
		done := 0
		for _, t := range st.Tasks {
			if t.Status == protocol.TaskDone {
				done++
			}
		}
		data["state"] = string(st.State)
		data["tasks_done"] = strconv.Itoa(done)
		data["tasks_total"] = strconv.Itoa(len(st.Tasks))
	}
	return data
}

// send delivers one notification to every device, dropping any FCM no longer
// knows.
func (n *Notifier) send(ctx context.Context, data map[string]string) {
	if n.sender == nil {
		return
	}
	for _, token := range n.devices.Tokens() {
		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := n.sender.Send(sctx, token, data)
		cancel()
		switch {
		case errors.Is(err, ErrStaleToken):
			n.log.Info("notify: dropping a device FCM no longer knows")
			_ = n.devices.Unregister(token)
		case err != nil:
			n.log.Warn("notify: send failed", "kind", data["kind"], "session", data["session_id"], "error", err)
		default:
			n.log.Info("notify: sent", "kind", data["kind"], "session", data["session_id"])
		}
	}
}
