package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// TaskStore records a task snapshot for a session. The agent implements it:
// it runs Merge against the session's log and appends the `tasks` event.
type TaskStore interface {
	UpdateTasks(ctx context.Context, s module.Session, incoming []protocol.Task, source string) (protocol.TasksData, error)
}

// Merge applies a whole-list snapshot (spec §9.2): stable ids, ids assigned
// where missing (reusing the id of an existing task with the same title, so a
// plan resent without ids does not mint new ones on every call),
// done_when/check/note carried over when omitted, revision
// incremented, and evidence set mechanically from the latest passing check
// event for the task. Model-supplied evidence is ignored.
func Merge(prev protocol.TasksData, incoming []protocol.Task, log []protocol.Event, source string) (protocol.TasksData, error) {
	if len(incoming) == 0 {
		return protocol.TasksData{}, fmt.Errorf("tasks must not be empty; mark tasks done or cancelled instead of removing them")
	}
	prevByID := map[string]protocol.Task{}
	next := 0
	for _, t := range prev.Tasks {
		prevByID[t.ID] = t
		next = maxSeq(next, t.ID)
	}
	seen := map[string]bool{}
	for _, t := range incoming {
		if t.ID != "" {
			if seen[t.ID] {
				return protocol.TasksData{}, fmt.Errorf("duplicate task id %q", t.ID)
			}
			seen[t.ID] = true
			next = maxSeq(next, t.ID)
		}
	}
	byTitle := map[string]string{}
	for _, t := range prev.Tasks {
		if k := strings.TrimSpace(t.Title); !seen[t.ID] && byTitle[k] == "" {
			byTitle[k] = t.ID
		}
	}
	out := protocol.TasksData{Revision: prev.Revision + 1, Source: source}
	for _, t := range incoming {
		if strings.TrimSpace(t.Title) == "" {
			return protocol.TasksData{}, fmt.Errorf("every task needs a title")
		}
		switch t.Status {
		case protocol.TaskPending, protocol.TaskInProgress, protocol.TaskBlocked,
			protocol.TaskDone, protocol.TaskFailed, protocol.TaskCancelled:
		default:
			return protocol.TasksData{}, fmt.Errorf(
				"task %q: status %q invalid (pending|in_progress|blocked|done|failed|cancelled)", t.Title, t.Status)
		}
		if t.ID == "" {
			if id := byTitle[strings.TrimSpace(t.Title)]; id != "" {
				t.ID = id
				delete(byTitle, strings.TrimSpace(t.Title))
			} else {
				next++
				t.ID = "t" + strconv.Itoa(next)
			}
		}
		if t.BlockedBy == nil {
			t.BlockedBy = []string{}
		}
		p, had := prevByID[t.ID]
		if had {
			if t.DoneWhen == "" {
				t.DoneWhen = p.DoneWhen
			}
			if t.Check == "" {
				t.Check = p.Check
			}
			if t.Note == "" {
				t.Note = p.Note
			}
		}
		t.Evidence = ""
		if t.Status == protocol.TaskDone {
			if had && p.Status == protocol.TaskDone && p.Evidence != "" {
				t.Evidence = p.Evidence
			} else {
				t.Evidence = latestPassingCheck(log, t.ID)
			}
		}
		out.Tasks = append(out.Tasks, t)
	}
	return out, nil
}

// maxSeq returns the larger of cur and the numeric suffix of an id like "t12".
func maxSeq(cur int, id string) int {
	if strings.HasPrefix(id, "t") {
		if n, err := strconv.Atoi(id[1:]); err == nil && n > cur {
			return n
		}
	}
	return cur
}

// latestTasks returns the session's current task snapshot, or none.
func latestTasks(s module.Session) protocol.TasksData {
	log, err := s.Events(nil)
	if err != nil {
		return protocol.TasksData{}
	}
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Type != protocol.EventTasks {
			continue
		}
		var d protocol.TasksData
		if json.Unmarshal(log[i].Data, &d) == nil {
			return d
		}
		break
	}
	return protocol.TasksData{}
}

func latestPassingCheck(log []protocol.Event, taskID string) string {
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Type != protocol.EventCheck {
			continue
		}
		var c protocol.CheckData
		if json.Unmarshal(log[i].Data, &c) == nil && c.TaskID == taskID && c.Status == "pass" {
			return log[i].ID
		}
	}
	return ""
}

func (b *Builtins) taskTool() module.Tool {
	type args struct {
		Tasks []protocol.Task `json:"tasks"`
	}
	return module.Tool{
		Name: "task.update",
		Description: "Replace the session's task list with this snapshot. Send every task each time (whole-list replace); keep ids stable; " +
			"give each task a done_when (the observable condition that proves it finished) and, where possible, a check command. " +
			"Statuses: pending, in_progress, blocked (needs a note), done, failed, cancelled.",
		Schema: schema(`{"type":"object","required":["tasks"],"properties":{"tasks":{"type":"array","items":{
			"type":"object","required":["title","status"],"properties":{
			"id":{"type":"string"},"title":{"type":"string"},
			"status":{"type":"string","enum":["pending","in_progress","blocked","done","failed","cancelled"]},
			"done_when":{"type":"string"},"check":{"type":"string"},
			"blocked_by":{"type":"array","items":{"type":"string"}},"note":{"type":"string"}}}}}}`),
		Run: func(ctx context.Context, s module.Session, raw json.RawMessage) (string, error) {
			a, err := decode[args](raw)
			if err != nil {
				return "", err
			}
			prev := latestTasks(s)
			data, err := b.Tasks.UpdateTasks(ctx, s, a.Tasks, "model")
			if err != nil {
				return "", err
			}
			out := protocol.RenderTasks(data.Tasks)
			// Resending the same plan is a loop shape of its own; the unchanged
			// list alone reads like progress.
			if prev.Revision > 0 && reflect.DeepEqual(prev.Tasks, data.Tasks) {
				out = fmt.Sprintf("no change: the plan is the same as revision %d\n", prev.Revision) + out
			}
			return out, nil
		},
	}
}
