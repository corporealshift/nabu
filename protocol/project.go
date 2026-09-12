package protocol

import "encoding/json"

// State is the session projection (spec §5): everything a client or the daemon
// needs to know about a session, derived by one pass over the log.
type State struct {
	State            SessionState `json:"state"`
	Options          Options      `json:"options"`
	Goal             *GoalData    `json:"goal,omitempty"`
	Tasks            []Task       `json:"tasks"`
	Budget           BudgetData   `json:"budget"`
	Turns            int          `json:"turns"`
	Usage            Usage        `json:"usage"`
	LastEventID      string       `json:"last_event_id"`
	CompactedThrough string       `json:"compacted_through,omitempty"`
}

// Project derives State from a log that has already passed ValidateLog.
func Project(log []Event) State {
	st := State{State: StateIdle, Tasks: []Task{}, Budget: BudgetData{Source: "daemon"}}
	for _, e := range log {
		st.LastEventID = e.ID
		switch e.Type {
		case EventSession:
			st.Options = MustData[SessionData](e).Options
		case EventOptionsChange:
			d := MustData[OptionsChangeData](e)
			switch d.Key {
			case "model":
				_ = json.Unmarshal(d.To, &st.Options.Model)
			case "compaction_enabled":
				_ = json.Unmarshal(d.To, &st.Options.CompactionEnabled)
			case "permission_mode":
				_ = json.Unmarshal(d.To, &st.Options.PermissionMode)
			}
		case EventStateChange:
			st.State = MustData[StateChangeData](e).To
		case EventGoal:
			st.Goal = MustData[GoalData](e)
		case EventTasks:
			d := MustData[TasksData](e)
			st.Tasks = d.Tasks
			if st.Tasks == nil {
				st.Tasks = []Task{}
			}
		case EventBudget:
			st.Budget = *MustData[BudgetData](e)
		case EventMessage:
			d := MustData[MessageData](e)
			if d.Role == "assistant" {
				st.Turns++
				if d.Usage != nil {
					st.Usage.InputTokens += d.Usage.InputTokens
					st.Usage.OutputTokens += d.Usage.OutputTokens
					st.Usage.CachedTokens += d.Usage.CachedTokens
				}
			}
		case EventCompaction:
			d := MustData[CompactionData](e)
			if d.Mode == CompactionSummarize {
				st.CompactedThrough = d.RangeEnd
			}
		}
	}
	return st
}

// GoalActive reports whether the projected goal still governs the loop.
func (s State) GoalActive() bool {
	return s.Goal != nil && (s.Goal.State == "set" || s.Goal.State == "unmet")
}

// OpenTasks returns tasks that are pending or in progress.
func (s State) OpenTasks() []Task {
	var out []Task
	for _, t := range s.Tasks {
		if t.Status == TaskPending || t.Status == TaskInProgress {
			out = append(out, t)
		}
	}
	return out
}

// DoneTasks counts tasks with status done.
func (s State) DoneTasks() int {
	n := 0
	for _, t := range s.Tasks {
		if t.Status == TaskDone {
			n++
		}
	}
	return n
}
