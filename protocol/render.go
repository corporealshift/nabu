package protocol

import (
	"fmt"
	"strings"
)

// RenderCurrentState produces the Current state block (spec §6.3) from a
// projection. Lines are joined with "\n" and there is no trailing newline.
func RenderCurrentState(s State) string {
	var lines []string
	lines = append(lines, "## Current state")
	if s.Goal == nil || s.Goal.State == "cleared" {
		lines = append(lines, "Goal: none")
	} else {
		lines = append(lines, fmt.Sprintf("Goal: %s (%s)", s.Goal.Condition, s.Goal.State))
	}
	if len(s.Tasks) > 0 {
		lines = append(lines, fmt.Sprintf("Tasks (%d/%d done):", s.DoneTasks(), len(s.Tasks)))
		for _, t := range s.Tasks {
			lines = append(lines, renderTaskLine(t))
		}
	}
	if s.Budget.MaxTurns > 0 {
		lines = append(lines, fmt.Sprintf("Budget: turn %d of %d", s.Turns, s.Budget.MaxTurns))
	} else {
		lines = append(lines, fmt.Sprintf("Budget: turn %d (unlimited)", s.Turns))
	}
	return strings.Join(lines, "\n")
}

func renderTaskLine(t Task) string {
	switch t.Status {
	case TaskDone:
		return fmt.Sprintf("- [x] %s %s", t.ID, t.Title)
	case TaskInProgress:
		return fmt.Sprintf("- [>] %s %s", t.ID, t.Title)
	case TaskPending:
		return fmt.Sprintf("- [ ] %s %s", t.ID, t.Title)
	case TaskBlocked:
		return fmt.Sprintf("- [!] %s %s — blocked: %s", t.ID, t.Title, t.Note)
	default: // failed, cancelled
		return fmt.Sprintf("- [-] %s %s — %s", t.ID, t.Title, t.Status)
	}
}

// OutstandingVetoes returns the stop_veto events appended after the most
// recent assistant message (spec §3.1 stop_veto).
func OutstandingVetoes(log []Event) []StopVetoData {
	start := 0
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Type == EventMessage && MustData[MessageData](log[i]).Role == "assistant" {
			start = i + 1
			break
		}
	}
	var out []StopVetoData
	for _, e := range log[start:] {
		if e.Type == EventStopVeto {
			out = append(out, *MustData[StopVetoData](e))
		}
	}
	return out
}

// RenderVetoes produces the veto message (spec §6.4), or "" when there are no
// outstanding vetoes.
func RenderVetoes(vetoes []StopVetoData) string {
	if len(vetoes) == 0 {
		return ""
	}
	lines := []string{"Before stopping, the following must be addressed:"}
	for _, v := range vetoes {
		lines = append(lines, fmt.Sprintf("- (%s) %s", v.Module, v.Reason))
	}
	return strings.Join(lines, "\n")
}
