// Command nabu-tui attaches to a nabu session, renders its transcript live,
// and answers the daemon's permission prompts. It holds no session state: the
// daemon owns the log, and closing this does not stop the run.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
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

const usage = `nabu-tui — watch a nabu session and answer its prompts

usage: nabu-tui [flags] [session-id]

With no session id, the most recently updated session is attached.

  --root DIR   nabu root (default ~/.nabu, or $NABU_ROOT)

Keys: y approve · n deny · q quit (the run continues) · g/G top/bottom
`

func main() {
	fs := flag.NewFlagSet("nabu-tui", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	root := fs.String("root", "", "nabu root")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	if err := run(*root, fs.Arg(0)); err != nil {
		fmt.Fprintf(os.Stderr, "nabu-tui: %v\n", err)
		os.Exit(1)
	}
}

func run(root, sessionID string) error {
	dir := root
	if dir == "" {
		var err error
		if dir, err = daemon.DefaultRoot(); err != nil {
			return err
		}
	}
	addr, ok := daemon.RunningAddr(dir)
	if !ok {
		return fmt.Errorf("no daemon is listening under %s — start one with `nabu daemon`", dir)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if sessionID == "" {
		if sessionID, err = latestSession(ctx, addr, cfg.Daemon.Token); err != nil {
			return err
		}
	}

	// The answer channel outlives any single connection, so a decision made
	// while reconnecting is not dropped on the floor.
	answers := make(chan answer, 8)
	p := tea.NewProgram(newModel(sessionID, answers), tea.WithAltScreen())

	// One goroutine owns the connection and pushes into the program; the
	// program never blocks on the network.
	go connectLoop(ctx, p, addr, cfg.Daemon.Token, sessionID, answers)

	_, err = p.Run()
	return err
}

// latestSession picks the most recently updated session, so attaching with no
// argument does the obvious thing.
func latestSession(ctx context.Context, addr, token string) (string, error) {
	dctx, cancel := context.WithTimeout(ctx, goclient.DialTimeout)
	defer cancel()
	c, err := goclient.Dial(dctx, addr, token, "nabu-tui", version)
	if err != nil {
		return "", err
	}
	defer c.Close()

	sessions, err := c.List(ctx)
	if err != nil {
		return "", err
	}
	if len(sessions) == 0 {
		return "", fmt.Errorf("no sessions yet — start one with `nabu run`")
	}
	// The store lists newest first; take the head.
	return sessions[0].SessionID, nil
}

// connectLoop keeps a connection up for the life of the program. On a drop it
// reconnects with backoff and replays from the cursor, so closing a laptop lid
// costs the gap and nothing else.
func connectLoop(ctx context.Context, p *tea.Program, addr, token, sessionID string, answers <-chan answer) {
	backoff := minBackoff
	var cursor string

	for ctx.Err() == nil {
		p.Send(connMsg{state: connecting})

		err := attach(ctx, p, addr, token, sessionID, &cursor, answers)
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
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// attach dials, catches up from the cursor, subscribes, and pumps until the
// connection ends. It returns the reason it ended.
func attach(ctx context.Context, p *tea.Program, addr, token, sessionID string, cursor *string, answers <-chan answer) error {
	dctx, cancel := context.WithTimeout(ctx, goclient.DialTimeout)
	c, err := goclient.Dial(dctx, addr, token, "nabu-tui", version)
	cancel()
	if err != nil {
		return err
	}
	defer c.Close()

	// Subscribe before replaying, so an event appended during the catch-up is
	// delivered live rather than falling into the gap between the two.
	if err := c.Subscribe(ctx, sessionID); err != nil {
		return err
	}

	var after *string
	if *cursor != "" {
		after = cursor
	}
	events, _, err := c.EventsAfter(ctx, sessionID, after)
	if err != nil {
		return err
	}
	for _, ev := range events {
		p.Send(eventMsg{ev: ev})
		*cursor = ev.ID
	}
	p.Send(connMsg{state: connected})

	// Drain answers for as long as this connection lives. The channel is
	// owned by the caller, so it survives a reconnect.
	connCtx, stopAnswers := context.WithCancel(ctx)
	defer stopAnswers()
	go func() {
		for {
			select {
			case <-connCtx.Done():
				return
			case a := <-answers:
				_ = c.AnswerPermission(connCtx, a.id, a.approve, a.reason)
			}
		}
	}()

	return c.Stream(ctx, func(msg goclient.Message) bool {
		switch {
		case msg.Method == "nabu.session.event":
			if ev, ok := goclient.ParseEvent(msg); ok {
				*cursor = ev.Event.ID
				p.Send(eventMsg{ev: ev.Event})
			}
		case msg.Method == "nabu.session.delta":
			if d, ok := goclient.ParseDelta(msg); ok {
				p.Send(deltaMsg{d: d})
			}
		case msg.Method == "nabu.rpc.permission.request":
			if req, ok := goclient.ParsePermission(msg); ok {
				p.Send(promptMsg{p: prompt{id: msg.ID, req: req}})
			}
		}
		return true
	})
}
