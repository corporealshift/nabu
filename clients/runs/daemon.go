package runs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/corporealshift/nabu/clients/goclient"
	"github.com/corporealshift/nabu/protocol"
)

// LabelRequested is the label /run puts on a home.
const LabelRequested = "run:requested"

// NoPushLabel makes the guard refuse a session anything that publishes: a
// push, or a post to GitHub.
const NoPushLabel = "guard:no-push"

// Client is the Daemon over a goclient connection.
type Client struct{ C *goclient.Client }

func (c Client) Requested(ctx context.Context) ([]Home, error) {
	list, err := c.C.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []Home
	for _, s := range list {
		if slices.Contains(s.Labels, LabelRequested) {
			out = append(out, Home{ID: s.SessionID, Workspace: s.Workspace, Labels: s.Labels, LastPrompt: s.LastPrompt})
		}
	}
	return out, nil
}

func (c Client) Home(ctx context.Context, id string) (Home, error) {
	list, err := c.C.List(ctx)
	if err != nil {
		return Home{}, err
	}
	for _, s := range list {
		if s.SessionID != id {
			continue
		}
		h := Home{ID: id, Workspace: s.Workspace, Labels: s.Labels, LastPrompt: s.LastPrompt}
		st, err := c.C.State(ctx, id)
		if err != nil {
			return Home{}, err
		}
		h.Brief = st.Options.Description
		return h, nil
	}
	return Home{}, fmt.Errorf("runs: session %s is not listed", id)
}

func (c Client) Transcript(ctx context.Context, id string) (string, error) {
	events, err := c.Events(ctx, id)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, e := range events {
		if e.Type != protocol.EventMessage {
			continue
		}
		var m protocol.MessageData
		if json.Unmarshal(e.Data, &m) != nil || strings.TrimSpace(m.Content) == "" {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n\n", m.Role, strings.TrimSpace(m.Content))
	}
	return b.String(), nil
}

func (c Client) SetLabels(ctx context.Context, id string, labels []string) error {
	if labels == nil {
		labels = []string{}
	}
	return c.C.CallInto(ctx, "nabu.session.set_option",
		map[string]any{"session_id": id, "key": "labels", "value": labels}, nil)
}

func (c Client) SetDescription(ctx context.Context, id, text string) error {
	return c.C.CallInto(ctx, "nabu.session.set_option",
		map[string]any{"session_id": id, "key": "description", "value": text}, nil)
}

func (c Client) SetGoal(ctx context.Context, id, condition string) error {
	return c.C.CallInto(ctx, "nabu.session.set_goal",
		map[string]any{"session_id": id, "condition": condition}, nil)
}

func (c Client) Create(ctx context.Context, workspace, parent string, maxTurns int) (string, error) {
	var out struct {
		SessionID string `json:"session_id"`
	}
	err := c.C.CallInto(ctx, "nabu.session.create", map[string]any{
		"workspace": workspace,
		// guard:no-push: a step session commits, and the runner pushes and
		// posts once the work is checked.
		"options": map[string]any{"permission_mode": string(protocol.PermissionAuto), "parent": parent,
			"labels": []string{NoPushLabel}},
		"budget": map[string]any{"max_turns": maxTurns, "source": "client"},
	}, &out)
	return out.SessionID, err
}

// SendPrompt sends a message with a client_id made from its text, so sending
// the same one again after a restart appends nothing (spec §7.4), while a
// different one, such as a nudge after the first prompt, is a message of its
// own.
func (c Client) SendPrompt(ctx context.Context, id, text string) error {
	sum := sha256.Sum256([]byte(text))
	return c.C.CallInto(ctx, "nabu.session.send_prompt",
		map[string]any{"session_id": id, "content": text, "client_id": "nabu-runner-" + hex.EncodeToString(sum[:8])}, nil)
}

func (c Client) State(ctx context.Context, id string) (protocol.State, error) {
	return c.C.State(ctx, id)
}

func (c Client) Events(ctx context.Context, id string) ([]protocol.Event, error) {
	var all []protocol.Event
	var cursor *string
	for {
		evs, synced, err := c.C.EventsAfter(ctx, id, cursor)
		if err != nil {
			return nil, err
		}
		all = append(all, evs...)
		if synced || len(evs) == 0 {
			return all, nil
		}
		last := evs[len(evs)-1].ID
		cursor = &last
	}
}

func (c Client) Stop(ctx context.Context, id string) error {
	return c.C.CallInto(ctx, "nabu.session.stop", map[string]any{"session_id": id}, nil)
}

func (c Client) Close() { c.C.Close() }
