package stats

import (
	"sort"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

// TreeStats is a session and every session under it, by parent, at any depth:
// a goal's home, its runs' homes and their steps. The totals are summed as
// Totals sums them, and Members gives each session's own numbers.
type TreeStats struct {
	SessionID string `json:"session_id"`
	Window
	StartedAt   time.Time `json:"started_at"`
	LastEventAt time.Time `json:"last_event_at"`
	Members     []Member  `json:"members"`
}

// Member is one session of a tree, measured alone.
type Member struct {
	SessionID      string       `json:"session_id"`
	Parent         string       `json:"parent,omitempty"`
	Labels         []string     `json:"labels"`
	Turns          int          `json:"turns"`
	WorkingSeconds int64        `json:"working_seconds"`
	Tokens         MemberTokens `json:"tokens"`
	StartedAt      time.Time    `json:"started_at"`
	LastEventAt    time.Time    `json:"last_event_at"`
	Archived       bool         `json:"archived,omitempty"`
}

// MemberTokens are one session's token totals.
type MemberTokens struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

// Tree measures root and its descendants among logs. Members come in the
// order the sessions were made, root first. A root not among the logs gives
// an empty tree.
func Tree(root string, logs []Log, loc *time.Location) TreeStats {
	children := map[string][]Log{}
	var top *Log
	for i, l := range logs {
		if l.ID == root {
			top = &logs[i]
		}
		if len(l.Events) == 0 {
			continue
		}
		if p := protocol.Project(l.Events[:1]).Options.Parent; p != "" {
			children[p] = append(children[p], l)
		}
	}
	out := TreeStats{SessionID: root, Members: []Member{}}
	if top == nil {
		var none tally
		out.Window = none.window()
		out.PerDay = []Day{}
		return out
	}

	var members []Log
	seen := map[string]bool{}
	var walk func(l Log)
	walk = func(l Log) {
		if seen[l.ID] {
			return
		}
		seen[l.ID] = true
		members = append(members, l)
		for _, c := range children[l.ID] {
			walk(c)
		}
	}
	walk(*top)
	// Session ids are ULIDs, so by id is by creation.
	sort.SliceStable(members[1:], func(i, j int) bool { return members[1+i].ID < members[1+j].ID })

	var t tally
	var all [][]protocol.Event
	for _, l := range members {
		s := Of(l.ID, l.Events)
		t.add(s)
		all = append(all, l.Events)
		o := protocol.Project(l.Events).Options
		labels := o.Labels
		if labels == nil {
			labels = []string{}
		}
		out.Members = append(out.Members, Member{
			SessionID: l.ID, Parent: o.Parent, Labels: labels,
			Turns: s.Turns, WorkingSeconds: s.WorkingSeconds,
			Tokens:    MemberTokens{Input: s.Tokens.Input, Output: s.Tokens.Output},
			StartedAt: s.StartedAt, LastEventAt: s.LastEventAt, Archived: l.Archived,
		})
		if out.StartedAt.IsZero() || (!s.StartedAt.IsZero() && s.StartedAt.Before(out.StartedAt)) {
			out.StartedAt = s.StartedAt
		}
		if s.LastEventAt.After(out.LastEventAt) {
			out.LastEventAt = s.LastEventAt
		}
	}
	out.Window = t.window()
	out.PerDay = []Day{}
	if !out.StartedAt.IsZero() {
		out.PerDay = Usage(all, out.StartedAt, out.LastEventAt, loc)
	}
	return out
}
