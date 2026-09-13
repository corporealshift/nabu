package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

// DefaultRequestTimeout bounds how long the daemon waits for a client to
// answer a permission or ask request. Spec 7.15 fixes the default at ten
// minutes: long enough for a phone in a pocket, short enough that a run does
// not hang forever on a client that will never answer.
const DefaultRequestTimeout = 10 * time.Minute

// requestState is where a daemon-to-client request has got to.
type requestState int

const (
	requestOpen requestState = iota
	requestAnswered
	requestTimedOut
)

// pendingRequest is one outstanding daemon-to-client request. It is resolved
// first-responder-wins: whichever client answers first decides, and every
// later answer is refused.
type pendingRequest struct {
	sessionID string
	answer    chan json.RawMessage

	mu    sync.Mutex
	state requestState
}

// resolve records an answer. It reports the state the request was in, so the
// caller can tell a winning answer from a late one and a late-after-timeout
// answer from a late-after-answer one.
func (p *pendingRequest) resolve(raw json.RawMessage) requestState {
	p.mu.Lock()
	was := p.state
	if was == requestOpen {
		p.state = requestAnswered
	}
	p.mu.Unlock()

	if was == requestOpen {
		p.answer <- raw
	}
	return was
}

// markTimedOut moves an unanswered request to timed out.
func (p *pendingRequest) markTimedOut() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == requestOpen {
		p.state = requestTimedOut
	}
}

// permissionReply is what a client returns for nabu.rpc.permission.request.
type permissionReply struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason,omitempty"`
}

// askReply is what a client returns for nabu.rpc.ui.ask.
type askReply struct {
	Answer string `json:"answer"`
}

// DefaultResolvedGrace is how long a resolved or timed-out request stays
// addressable so late answers can still be recognised.
const DefaultResolvedGrace = 2 * time.Minute

// resolvedGrace is the configured retention, falling back to the default.
func (h *Handler) resolvedGrace() time.Duration {
	if h.ResolvedGrace > 0 {
		return h.ResolvedGrace
	}
	return DefaultResolvedGrace
}

// requestTimeout is the configured wait, falling back to the spec default.
func (h *Handler) requestTimeout() time.Duration {
	if h.RequestTimeout > 0 {
		return h.RequestTimeout
	}
	return DefaultRequestTimeout
}

// ask broadcasts a daemon-to-client request to every connection subscribed to
// the session and waits for the first answer. It returns the raw result, or an
// error if nobody was attached, nobody answered in time, or the caller's
// context was cancelled.
func (h *Handler) ask(ctx context.Context, sessionID, method string, params map[string]any) (json.RawMessage, error) {
	subs := h.subscribers(sessionID)
	if len(subs) == 0 {
		return nil, fmt.Errorf("no client is attached to session %s", sessionID)
	}

	id := protocol.NewULID()
	params["session_id"] = sessionID
	params["request_id"] = id

	p := &pendingRequest{sessionID: sessionID, answer: make(chan json.RawMessage, 1)}
	h.reqMu.Lock()
	h.pending[id] = p
	h.reqMu.Unlock()

	// The entry outlives the wait on purpose. Spec 7.15 requires that a late
	// responder be told the request is already resolved, and that an answer
	// arriving after a timeout still be accepted — neither is possible once
	// the id has been forgotten. It is dropped after a grace window.
	defer func() {
		time.AfterFunc(h.resolvedGrace(), func() {
			h.reqMu.Lock()
			delete(h.pending, id)
			h.reqMu.Unlock()
		})
	}()

	req := jsonrpcRequest{JSONRPC: "2.0", ID: id, Method: method}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	req.Params = raw
	for _, cs := range subs {
		_ = cs.write(req)
	}

	timer := time.NewTimer(h.requestTimeout())
	defer timer.Stop()

	select {
	case answer := <-p.answer:
		return answer, nil
	case <-timer.C:
		p.markTimedOut()
		return nil, fmt.Errorf("no client answered %s within %s", method, h.requestTimeout())
	case <-ctx.Done():
		p.markTimedOut()
		return nil, ctx.Err()
	}
}

// routeResponse handles a client message that is a response rather than a
// request: it is an answer to a daemon-to-client request. It reports whether
// the message was consumed as one.
func (h *Handler) routeResponse(ctx context.Context, cs *connState, msg *clientMessage) bool {
	if msg.ID == nil {
		return false
	}
	id, ok := msg.ID.(string)
	if !ok {
		return false
	}

	h.reqMu.Lock()
	p, known := h.pending[id]
	h.reqMu.Unlock()
	if !known {
		return false
	}

	switch p.resolve(msg.Result) {
	case requestOpen:
		// This client won. Nothing to send back.
	case requestAnswered:
		// Spec 7.15: a later responder is told the request is already resolved
		// so it can dismiss its prompt.
		_ = cs.write(errorResp(msg.ID, protocol.CodeAlreadyResolved,
			protocol.ErrorNames[protocol.CodeAlreadyResolved]))
	case requestTimedOut:
		// Spec 7.15: "a later answer is still accepted and resumes it." The
		// answer is too late to feed the call that asked, so what it buys is
		// the session moving on.
		h.resumeAfterLateAnswer(ctx, p.sessionID)
	}
	return true
}

// resumeAfterLateAnswer resumes a session that stalled waiting on a request
// that timed out. Resume is only valid from paused, so an invalid transition
// here means the session moved on by itself and there is nothing to do.
func (h *Handler) resumeAfterLateAnswer(ctx context.Context, sessionID string) {
	err := h.manager.Resume(ctx, sessionID, nil)
	if err == nil {
		h.log.Info("resumed after a late answer to a timed-out request", "session", sessionID)
		return
	}
	var rpcErr *protocol.RPCError
	if errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeInvalidTransition {
		return
	}
	h.log.Warn("could not resume after a late answer", "session", sessionID, "error", err)
}

// Permission implements agent.Asker. It broadcasts to every attached client
// and takes the first verdict. With nobody attached, or nobody answering, it
// denies: refusing a gated call is the safe outcome.
func (h *Handler) Permission(ctx context.Context, sessionID string, call protocol.ToolCallData, summary, risk string) (bool, string) {
	raw, err := h.ask(ctx, sessionID, "nabu.rpc.permission.request", map[string]any{
		"tool":    call.Tool,
		"summary": summary,
		"risk":    risk,
	})
	if err != nil {
		return false, err.Error()
	}
	var reply permissionReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return false, "unreadable verdict: " + err.Error()
	}
	if reply.Verdict != "approve" {
		reason := reply.Reason
		if reason == "" {
			reason = "denied by the client"
		}
		return false, reason
	}
	return true, reply.Reason
}

// Ask implements agent.Asker for module ui.ask.
func (h *Handler) Ask(ctx context.Context, sessionID, question string, choices []string) (string, error) {
	params := map[string]any{"question": question}
	if len(choices) > 0 {
		params["choices"] = choices
	}
	raw, err := h.ask(ctx, sessionID, "nabu.rpc.ui.ask", params)
	if err != nil {
		return "", err
	}
	var reply askReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return "", fmt.Errorf("unreadable answer: %w", err)
	}
	return reply.Answer, nil
}
