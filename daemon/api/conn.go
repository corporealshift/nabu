package api

import (
	"context"
	"errors"
	"sync"
	"time"
)

// sendQueue is how many messages one connection may have waiting to be
// written. Deltas arrive a few tokens at a time, so this is sized for a burst
// of those over a slow link, not for a client that has stopped reading.
const sendQueue = 1024

// writeTimeout bounds one write. A client that takes longer than this to
// accept a frame is not reading, and holding its frames helps nobody.
const writeTimeout = 15 * time.Second

var (
	errConnClosed = errors.New("connection closed")
	errTooSlow    = errors.New("connection fell behind and was dropped")
)

// connState is one client connection.
//
// Nothing writes to the socket but the connection's own writer, and write only
// queues. That is what keeps one client from holding up another (issue 110):
// a phone frozen in the background keeps its socket open with a full receive
// window, so a write to it blocks for as long as the phone stays asleep. When
// the fan-out wrote directly, that one write stalled every session event, delta
// and question for every other client attached, and the session store then
// dropped the stalled pump for falling behind.
//
// A connection whose queue fills, or whose write times out, is closed. Clients
// reconnect and replay from their cursor, so closing loses nothing.
type connState struct {
	conn Conn
	ctx  context.Context

	out     chan any
	quit    chan struct{} // closed to stop the writer
	done    chan struct{} // closed when the writer has stopped
	quitter sync.Once
}

// newConnState starts the connection's writer. finish stops it.
func newConnState(ctx context.Context, conn Conn) *connState {
	c := &connState{
		conn: conn,
		ctx:  ctx,
		out:  make(chan any, sendQueue),
		quit: make(chan struct{}),
		done: make(chan struct{}),
	}
	go c.writeLoop()
	return c
}

// write queues v for the connection. It never blocks: a connection too far
// behind to take another message is dropped instead.
func (c *connState) write(v any) error {
	select {
	case <-c.quit:
		return errConnClosed
	default:
	}
	select {
	case c.out <- v:
		return nil
	default:
		c.drop()
		return errTooSlow
	}
}

func (c *connState) notify(method string, params any) {
	_ = c.write(jsonrpcNotification{JSONRPC: "2.0", Method: method, Params: params})
}

// flush waits until everything queued before it has been written or dropped.
func (c *connState) flush() {
	mark := make(chan struct{})
	if c.write(mark) != nil {
		return
	}
	select {
	case <-mark:
	case <-c.done:
	}
}

// finish writes what is already queued and stops the writer.
func (c *connState) finish() {
	c.quitter.Do(func() { close(c.quit) })
	<-c.done
}

// drop closes a connection that cannot keep up. Its read loop then fails, and
// the connection is torn down as if the client had gone.
func (c *connState) drop() {
	c.quitter.Do(func() { close(c.quit) })
	c.conn.CloseNow()
}

func (c *connState) writeLoop() {
	defer close(c.done)
	for {
		select {
		case v := <-c.out:
			if !c.send(v) {
				return
			}
		case <-c.quit:
			for {
				select {
				case v := <-c.out:
					if !c.send(v) {
						return
					}
				default:
					return
				}
			}
		}
	}
}

// send writes one message, reporting whether the connection is still usable.
func (c *connState) send(v any) bool {
	if mark, ok := v.(chan struct{}); ok {
		close(mark)
		return true
	}
	ctx, cancel := context.WithTimeout(c.ctx, writeTimeout)
	defer cancel()
	if err := c.conn.WriteJSON(ctx, v); err != nil {
		c.drop()
		return false
	}
	return true
}
