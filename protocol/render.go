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
		lines = append(lines, RenderTasks(s.Tasks))
	}
	if s.Budget.MaxTurns > 0 {
		lines = append(lines, fmt.Sprintf("Budget: turn %d of %d", s.Turns, s.Budget.MaxTurns))
	} else {
		lines = append(lines, fmt.Sprintf("Budget: turn %d (unlimited)", s.Turns))
	}
	return strings.Join(lines, "\n")
}

// MaxSaidBytes bounds the person's words in the Current state block (spec §6.3).
const MaxSaidBytes = 4096

// RenderStateBlock produces the Current state block (spec §6.3) from a log:
// the projection's state, then what the person said. The person's words are
// rendered from the log, not left to a summary, so a summary cannot lose them.
func RenderStateBlock(log []Event) string {
	block := RenderCurrentState(Project(log))
	if said := renderSaid(log); said != "" {
		block += "\n" + said
	}
	return block
}

// renderSaid renders every user message, or "" when there is none. The first
// is always kept, since in a run it is the brief; then the newest that fit in
// MaxSaidBytes. A marker stands where messages were left out.
func renderSaid(log []Event) string {
	var lines []string
	for _, e := range log {
		if e.Type != EventMessage {
			continue
		}
		if d := MustData[MessageData](e); d.Role == "user" {
			text := strings.ReplaceAll(d.Content, "\n", "\n  ")
			lines = append(lines, fmt.Sprintf("- %s: %s", e.Timestamp.UTC().Format("2006-01-02 15:04Z"), text))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	kept := map[int]bool{0: true}
	size := len(lines[0])
	for i := len(lines) - 1; i > 0; i-- {
		if size+1+len(lines[i]) > MaxSaidBytes {
			break
		}
		size += 1 + len(lines[i])
		kept[i] = true
	}
	out := []string{fmt.Sprintf("The person said (%d of %d, oldest first):", len(kept), len(lines))}
	for i := 0; i < len(lines); i++ {
		if kept[i] {
			out = append(out, lines[i])
			continue
		}
		left := 0
		for ; i < len(lines) && !kept[i]; i++ {
			left++
		}
		out = append(out, fmt.Sprintf("- (%d left out)", left))
		i--
	}
	return strings.Join(out, "\n")
}

// RenderTasks renders a task list in the same form the Current state block
// uses, so a task.update tool result and the state block never disagree.
func RenderTasks(tasks []Task) string {
	if len(tasks) == 0 {
		return "Tasks: none"
	}
	done := 0
	for _, t := range tasks {
		if t.Status == TaskDone {
			done++
		}
	}
	lines := []string{fmt.Sprintf("Tasks (%d/%d done):", done, len(tasks))}
	for _, t := range tasks {
		lines = append(lines, renderTaskLine(t))
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
