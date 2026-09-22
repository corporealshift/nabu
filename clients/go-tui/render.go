package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/corporealshift/nabu/protocol"
)

// Styles. Colours are the terminal's own 0-15 palette, so the TUI inherits
// whatever theme the user already has rather than fighting it.
var (
	dim       = lipgloss.NewStyle().Faint(true)
	userStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4"))
	toolStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	errStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	okStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
)

// maxInline is how much of a long value is shown on one transcript line.
const maxInline = 160

// renderEvent turns one event into the lines it contributes to the transcript.
// It returns nil for events that belong to the machinery rather than the
// conversation, so the transcript stays readable.
func renderEvent(ev protocol.Event) []string {
	switch ev.Type {
	case protocol.EventSession:
		var d protocol.SessionData
		if json.Unmarshal(ev.Data, &d) != nil {
			return nil
		}
		return []string{dim.Render("session in " + d.Workspace)}

	case protocol.EventMessage:
		var d protocol.MessageData
		if json.Unmarshal(ev.Data, &d) != nil {
			return nil
		}
		return renderMessage(d, ev.Timestamp)

	case protocol.EventToolCall:
		var d protocol.ToolCallData
		if json.Unmarshal(ev.Data, &d) != nil {
			return nil
		}
		label := d.Tool
		if arg := firstArg(d.Arguments); arg != "" {
			label += " " + arg
		}
		return []string{toolStyle.Render("→ " + truncate(label, maxInline))}

	case protocol.EventToolResult:
		var d protocol.ToolResultData
		if json.Unmarshal(ev.Data, &d) != nil {
			return nil
		}
		body := strings.TrimSpace(d.Content)
		if body == "" {
			body = "(no output)"
		}
		line := "  " + truncate(firstLine(body), maxInline)
		if d.Status != "ok" {
			return []string{errStyle.Render(line)}
		}
		return []string{dim.Render(line)}

	case protocol.EventStateChange:
		var d protocol.StateChangeData
		if json.Unmarshal(ev.Data, &d) != nil {
			return nil
		}
		from := ""
		if d.From != nil {
			from = string(*d.From) + " → "
		}
		line := from + string(d.To)
		if d.Reason != "" {
			line += " (" + d.Reason + ")"
		}
		return []string{dim.Render(line)}

	case protocol.EventGoal:
		var d protocol.GoalData
		if json.Unmarshal(ev.Data, &d) != nil {
			return nil
		}
		line := "goal " + d.State + ": " + d.Condition
		if d.Reason != "" {
			line += " — " + d.Reason
		}
		return []string{warnStyle.Render(truncate(line, maxInline))}

	case protocol.EventCheck:
		var d protocol.CheckData
		if json.Unmarshal(ev.Data, &d) != nil {
			return nil
		}
		style := okStyle
		if d.Status != "pass" {
			style = errStyle
		}
		return []string{style.Render("check " + d.Name + ": " + d.Status + " " + d.Summary)}

	case protocol.EventStopVeto:
		var d protocol.StopVetoData
		if json.Unmarshal(ev.Data, &d) != nil {
			return nil
		}
		return []string{warnStyle.Render("veto (" + d.Module + "): " + truncate(d.Reason, maxInline))}

	case protocol.EventNotice:
		var d protocol.NoticeData
		if json.Unmarshal(ev.Data, &d) != nil {
			return nil
		}
		style := dim
		switch d.Level {
		case "error":
			style = errStyle
		case "warn":
			style = warnStyle
		}
		return []string{style.Render(d.Message)}

	case protocol.EventReport:
		var d protocol.ReportData
		if json.Unmarshal(ev.Data, &d) != nil {
			return nil
		}
		return []string{renderReport(d)}

	case protocol.EventOptionsChange:
		var d protocol.OptionsChangeData
		if json.Unmarshal(ev.Data, &d) != nil {
			return nil
		}
		return []string{dim.Render(fmt.Sprintf("%s → %s", d.Key, strings.Trim(string(d.To), `"`)))}

	case protocol.EventCompaction:
		var d protocol.CompactionData
		if json.Unmarshal(ev.Data, &d) != nil {
			return []string{dim.Render("context compacted")}
		}
		// Which kind matters: one stubs out old tool results, the other
		// replaces the conversation with a summary.
		switch d.Mode {
		case protocol.CompactionSummarize:
			return []string{warnStyle.Render("— earlier conversation summarised —")}
		case protocol.CompactionClearResults:
			return []string{dim.Render("— older tool output cleared to save context —")}
		}
		return []string{dim.Render("context compacted")}

	default:
		// context, tasks and budget shape the request or the status bar
		// rather than the conversation.
		return nil
	}
}

// renderMessage renders a user or assistant turn.
func renderMessage(d protocol.MessageData, at time.Time) []string {
	body := strings.TrimSpace(d.Content)
	if body == "" && !d.Interrupted {
		return nil
	}
	var out []string
	timestamp := ""
	if !at.IsZero() {
		timestamp = dim.Render(at.Local().Format("Jan 2 15:04")) + " "
	}
	if d.Role == "user" {
		for i, l := range strings.Split(body, "\n") {
			prefix := ""
			if i == 0 {
				prefix = timestamp
			}
			out = append(out, prefix+userStyle.Render("› "+l))
		}
		return out
	}
	for i, l := range strings.Split(body, "\n") {
		prefix := ""
		if i == 0 {
			prefix = timestamp
		}
		out = append(out, prefix+l)
	}
	if d.Interrupted {
		out = append(out, dim.Render("(interrupted)"))
	}
	return out
}

// renderReport renders the run report, which is what a caller checks instead
// of trusting the model's account of what happened.
func renderReport(d protocol.ReportData) string {
	var b strings.Builder
	b.WriteString("report: " + string(d.ExitStatus))
	if d.Tasks.Total > 0 {
		fmt.Fprintf(&b, "  tasks %d/%d", d.Tasks.Done, d.Tasks.Total)
	}
	if n := len(d.FilesTouched); n > 0 {
		fmt.Fprintf(&b, "  files %d", n)
	}
	if n := len(d.Commits); n > 0 {
		fmt.Fprintf(&b, "  commits %d", n)
	}
	if d.TreeDirty != nil && *d.TreeDirty {
		b.WriteString("  tree dirty")
	}
	style := okStyle
	if d.ExitStatus != protocol.StateCompleted {
		style = warnStyle
	}
	return style.Render(b.String())
}

// firstArg pulls a short identifier out of a tool call's arguments, so a line
// reads "bash go test ./..." rather than just "bash".
func firstArg(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(args, &m) != nil {
		return ""
	}
	for _, key := range []string{"command", "path", "pattern", "file_path"} {
		if v, ok := m[key].(string); ok && v != "" {
			return firstLine(v)
		}
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i]) + " …"
	}
	return s
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
