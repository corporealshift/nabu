// Package tui renders a nabu session live and answers the daemon's permission
// prompts. It holds no session state: the daemon owns the log, and closing the
// TUI does not stop the run.
package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/corporealshift/nabu/clients/goclient"
	"github.com/corporealshift/nabu/daemon"
	"github.com/corporealshift/nabu/daemon/config"
)

const version = "0.0.0"

// Reconnect backoff. A dropped connection is not the end of a run, so this
// keeps trying rather than exiting.
const (
	minBackoff = 250 * time.Millisecond
	maxBackoff = 10 * time.Second
)

// Options are what the TUI needs to attach. SessionID is required: choosing
// or creating a session is the caller's job, because that is session lifecycle
// and this package only renders one.
type Options struct {
	Root      string
	SessionID string
}

// Run attaches to a session and renders it until the human quits. It starts a
// daemon if none is listening, so opening the TUI is always one step.
func Run(ctx context.Context, opts Options) error {
	dir := opts.Root
	if dir == "" {
		var err error
		if dir, err = daemon.DefaultRoot(); err != nil {
			return err
		}
	}
	sessionID := opts.SessionID
	if sessionID == "" {
		return fmt.Errorf("tui: a session id is required")
	}
	addr, err := daemon.EnsureRunning(ctx, dir)
	if err != nil {
		return err
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// The action channel outlives any single connection, so a decision made
	// while reconnecting is not dropped on the floor.
	actions := make(chan action, 16)
	p := tea.NewProgram(newModel(sessionID, actions), tea.WithAltScreen())

	// One goroutine owns the connection and pushes into the program; the
	// program never blocks on the network.
	go connectLoop(ctx, p, addr, cfg.Daemon.Token, sessionID, actions)

	_, err = p.Run()
	return err
}

// connectLoop keeps a connection up for the life of the program. On a drop it
// reconnects with backoff and replays from the cursor, so closing a laptop lid
// costs the gap and nothing else.
func connectLoop(ctx context.Context, p *tea.Program, addr, token, sessionID string, actions chan action) {
	var wait retry
	var cursor string

	for ctx.Err() == nil {
		p.Send(connMsg{state: connecting})

		// An attach action switches sessions: the loop reconnects against
		// the new one and starts its cursor from nothing.
		next, err := attach(ctx, p, addr, token, sessionID, &cursor, actions, wait.connected)
		if next != "" && next != sessionID {
			sessionID, cursor = next, ""
			wait.connected()
			continue
		}
		if ctx.Err() != nil {
			return
		}
		note := "disconnected — reconnecting"
		if err != nil {
			note = "disconnected: " + err.Error()
		}
		p.Send(connMsg{state: disconnected, note: note})

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait.failed()):
		}
	}
}

// retry is the reconnect backoff. It doubles on each failure and starts over
// once a connection is actually made. It used to start over only on a session
// switch, so after a few drops every reconnect waited the full ten seconds,
// however long the connection before it had lasted — the fault issue 69 found
// on Android.
type retry struct{ next time.Duration }

// failed returns how long to wait before trying again.
func (r *retry) failed() time.Duration {
	if r.next == 0 {
		r.next = minBackoff
	}
	wait := r.next
	if r.next *= 2; r.next > maxBackoff {
		r.next = maxBackoff
	}
	return wait
}

// connected starts the backoff over.
func (r *retry) connected() { r.next = 0 }

// attach dials, catches up, subscribes and pumps. It returns the session to
// switch to, if the human picked a different one, and the reason it ended.
func attach(ctx context.Context, p *tea.Program, addr, token, sessionID string, cursor *string, actions chan action, onConnected func()) (string, error) {
	dctx, cancel := context.WithTimeout(ctx, goclient.DialTimeout)
	c, err := goclient.Dial(dctx, addr, token, "nabu-tui", version)
	cancel()
	if err != nil {
		return "", err
	}
	defer c.Close()

	// Subscribe before replaying, so an event appended during the catch-up is
	// delivered live rather than falling into the gap between the two.
	if err := c.Subscribe(ctx, sessionID); err != nil {
		return "", err
	}

	var after *string
	if *cursor != "" {
		after = cursor
	}
	events, _, err := c.EventsAfter(ctx, sessionID, after)
	if err != nil {
		return "", err
	}
	if len(events) > 0 {
		p.Send(eventsMsg{events: events})
		*cursor = events[len(events)-1].ID
	}
	p.Send(connMsg{state: connected})
	onConnected()

	// Carry out actions for as long as this connection lives. The channel is
	// owned by the caller, so a decision made mid-reconnect is not lost.
	connCtx, stop := context.WithCancel(ctx)
	defer stop()
	switchTo := make(chan string, 1)
	go func() {
		for {
			select {
			case <-connCtx.Done():
				return
			case a := <-actions:
				if next := perform(connCtx, c, p, a); next != "" {
					select {
					case switchTo <- next:
					default:
					}
					return
				}
			}
		}
	}()

	err = c.Stream(ctx, func(msg goclient.Message) bool {
		switch msg.Method {
		case "nabu.session.event":
			if ev, ok := goclient.ParseEvent(msg); ok {
				*cursor = ev.Event.ID
				p.Send(eventMsg{ev: ev.Event})
			}
		case "nabu.session.delta":
			if d, ok := goclient.ParseDelta(msg); ok {
				p.Send(deltaMsg{d: d})
			}
		case "nabu.session.thinking":
			if d, ok := goclient.ParseThinking(msg); ok {
				p.Send(thinkingMsg{d: d})
			}
		case "nabu.rpc.ui.ask":
			if req, ok := goclient.ParseAsk(msg); ok {
				p.Send(askMsg{q: question{id: msg.ID, req: req}})
			}
		case "nabu.rpc.permission.request":
			if req, ok := goclient.ParsePermission(msg); ok {
				p.Send(promptMsg{p: prompt{id: msg.ID, req: req}})
			}
		}
		// A session switch ends this connection so the loop can start the next.
		select {
		case next := <-switchTo:
			c.Close() // unblock the read
			switchTo <- next
			return false
		default:
			return true
		}
	})

	select {
	case next := <-switchTo:
		return next, nil
	default:
		return "", err
	}
}

// perform carries out one action against the daemon. It returns a session id
// when the action was a switch, which ends the current connection.
func perform(ctx context.Context, c *goclient.Client, p *tea.Program, a action) string {
	var err error
	switch a.kind {
	case actAnswer:
		err = c.AnswerPermission(ctx, a.id, a.approve, a.reason)
	case actAnswerAsk:
		err = c.AnswerAsk(ctx, a.id, a.text)
	case actPrompt:
		_, err = c.Call(ctx, "nabu.session.send_prompt",
			map[string]any{"session_id": a.sessionID, "content": a.text})
	case actInterrupt, actStop:
		// Off the action queue: these exist to end something slow, and would
		// be useless waiting behind it (a prompt held until a summary lands).
		method := "nabu.session.interrupt"
		if a.kind == actStop {
			method = "nabu.session.stop"
		}
		go func() {
			if _, err := c.Call(ctx, method, map[string]any{"session_id": a.sessionID}); err != nil {
				p.Send(errMsg{text: err.Error()})
			}
		}()
	case actCompact:
		// A model call, and minutes on a local one (issue 87). It runs off the
		// action queue so ctrl+x can still reach the daemon while it does.
		go func() {
			var out struct {
				Mode string `json:"mode"`
			}
			err := c.CallInto(ctx, "nabu.session.compact",
				map[string]any{"session_id": a.sessionID}, &out)
			done := compactedMsg{mode: out.Mode}
			if err != nil {
				done.err = err.Error()
			}
			p.Send(done)
		}()
	case actSetGoal:
		_, err = c.Call(ctx, "nabu.session.set_goal",
			map[string]any{"session_id": a.sessionID, "condition": a.text})
	case actClearGoal:
		_, err = c.Call(ctx, "nabu.session.clear_goal",
			map[string]any{"session_id": a.sessionID})
	case actListSessions:
		var sessions []goclient.SessionSummary
		if sessions, err = c.List(ctx); err == nil {
			p.Send(sessionsMsg{sessions: sessions})
		}
	case actListArchived:
		var sessions []goclient.SessionSummary
		if sessions, err = c.ListArchived(ctx); err == nil {
			p.Send(sessionsMsg{sessions: sessions, archived: true})
		}
	case actArchive:
		if _, err = c.Call(ctx, "nabu.session.archive", map[string]any{"session_id": a.sessionID}); err == nil {
			p.Send(noteMsg{text: "archived " + shortID(a.sessionID) + " — tab in the picker shows the archive"})
			var sessions []goclient.SessionSummary
			if sessions, err = c.List(ctx); err == nil {
				p.Send(sessionsMsg{sessions: sessions})
			}
		}
	case actRestore:
		if _, err = c.Call(ctx, "nabu.session.restore", map[string]any{"session_id": a.sessionID}); err == nil {
			return a.sessionID
		}
	case actAttach:
		return a.sessionID
	}
	if err != nil {
		p.Send(errMsg{text: err.Error()})
	}
	return ""
}
