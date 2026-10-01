package github

import (
	"context"

	"github.com/corporealshift/nabu/clients/goclient"
	"github.com/corporealshift/nabu/protocol"
)

// Client is the Daemon over a goclient connection.
type Client struct{ C *goclient.Client }

func (c Client) Create(ctx context.Context, workspace string, maxTurns int) (string, error) {
	var out struct {
		SessionID string `json:"session_id"`
	}
	err := c.C.CallInto(ctx, "nabu.session.create", map[string]any{
		"workspace": workspace,
		// guard:no-push: the watcher pushes and posts itself, from what a
		// finished session left.
		"options": map[string]any{"permission_mode": string(protocol.PermissionAuto), "labels": []string{"guard:no-push"}},
		"budget":  map[string]any{"max_turns": maxTurns, "source": "client"},
	}, &out)
	return out.SessionID, err
}

func (c Client) SetGoal(ctx context.Context, sessionID, condition string) error {
	return c.C.CallInto(ctx, "nabu.session.set_goal", map[string]any{
		"session_id": sessionID, "condition": condition,
	}, nil)
}

// promptClientID makes the one prompt a watcher session gets idempotent, so
// sending it again after a crash does not append it twice (spec §7.4).
const promptClientID = "nabu-github-prompt"

func (c Client) SendPrompt(ctx context.Context, sessionID, text string) error {
	return c.C.CallInto(ctx, "nabu.session.send_prompt", map[string]any{
		"session_id": sessionID, "content": text, "client_id": promptClientID,
	}, nil)
}

func (c Client) State(ctx context.Context, sessionID string) (protocol.State, error) {
	return c.C.State(ctx, sessionID)
}

func (c Client) Stop(ctx context.Context, sessionID string) error {
	return c.C.CallInto(ctx, "nabu.session.stop", map[string]any{"session_id": sessionID}, nil)
}

// Events is the whole log, fetched page by page until the daemon says it is
// in sync.
func (c Client) Events(ctx context.Context, sessionID string) ([]protocol.Event, error) {
	var all []protocol.Event
	var cursor *string
	for {
		evs, synced, err := c.C.EventsAfter(ctx, sessionID, cursor)
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

func (c Client) Close() { c.C.Close() }

func (c Client) CreateHome(ctx context.Context, workspace, description string, labels []string) (string, error) {
	var out struct {
		SessionID string `json:"session_id"`
	}
	err := c.C.CallInto(ctx, "nabu.session.create", map[string]any{
		"workspace": workspace,
		"options":   map[string]any{"description": description, "labels": labels},
	}, &out)
	return out.SessionID, err
}

func (c Client) Labels(ctx context.Context, sessionID string) ([]string, error) {
	st, err := c.C.State(ctx, sessionID)
	return st.Options.Labels, err
}

func (c Client) Rerun(ctx context.Context, sessionID, description string, labels []string) error {
	// The brief first, so the runner never sees the request without it.
	if err := c.C.CallInto(ctx, "nabu.session.set_option",
		map[string]any{"session_id": sessionID, "key": "description", "value": description}, nil); err != nil {
		return err
	}
	return c.C.CallInto(ctx, "nabu.session.set_option",
		map[string]any{"session_id": sessionID, "key": "labels", "value": labels}, nil)
}
