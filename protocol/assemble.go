package protocol

import "fmt"

// SegmentKind names one piece of an assembled request (spec §6).
type SegmentKind string

const (
	SegPrefixContext     SegmentKind = "prefix_context"
	SegSummary           SegmentKind = "summary"
	SegState             SegmentKind = "state"
	SegMessage           SegmentKind = "message"
	SegToolCall          SegmentKind = "tool_call"
	SegToolResult        SegmentKind = "tool_result"
	SegToolResultCleared SegmentKind = "tool_result_cleared"
	SegSuffixContext     SegmentKind = "suffix_context"
	SegVetoes            SegmentKind = "vetoes"
)

// Segment is one ordered piece of the request. ID is the originating event
// for event-backed segments and empty for the state block and vetoes. Text is
// the rendered content for summary, state, cleared stubs, and vetoes; for
// message/tool segments the caller reads the event itself.
type Segment struct {
	Kind SegmentKind `json:"kind"`
	ID   string      `json:"id,omitempty"`
	Text string      `json:"text,omitempty"`
}

// Assemble computes the request body as a pure function of the log (spec §6).
// The static system prompt is not included; the daemon prepends it.
func Assemble(log []Event) []Segment {
	pos := make(map[string]int, len(log))
	for i, e := range log {
		pos[e.ID] = i
	}

	// Last summarize compaction and all clear_results ranges.
	summarizeAt := -1
	type span struct{ lo, hi int }
	var cleared []span
	for i, e := range log {
		if e.Type != EventCompaction {
			continue
		}
		d := MustData[CompactionData](e)
		switch d.Mode {
		case CompactionSummarize:
			summarizeAt = i
		case CompactionClearResults:
			lo, okLo := pos[d.RangeStart]
			hi, okHi := pos[d.RangeEnd]
			if okLo && okHi && lo <= hi {
				cleared = append(cleared, span{lo, hi})
			}
		}
	}
	isCleared := func(i int) bool {
		for _, s := range cleared {
			if i >= s.lo && i <= s.hi {
				return true
			}
		}
		return false
	}

	// Last assistant message (for suffix contexts and vetoes).
	lastAssistant := -1
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Type == EventMessage && MustData[MessageData](log[i]).Role == "assistant" {
			lastAssistant = i
			break
		}
	}

	var out []Segment

	// 2. prefix contexts after the last summarize.
	for i := summarizeAt + 1; i < len(log); i++ {
		e := log[i]
		if e.Type == EventContext && MustData[ContextData](e).Slot == "prefix" {
			out = append(out, Segment{Kind: SegPrefixContext, ID: e.ID, Text: MustData[ContextData](e).Content})
		}
	}

	// 3. summary + current state.
	if summarizeAt >= 0 {
		out = append(out, Segment{Kind: SegSummary, ID: log[summarizeAt].ID, Text: MustData[CompactionData](log[summarizeAt]).Summary})
		out = append(out, Segment{Kind: SegState, Text: RenderCurrentState(Project(log))})
	}

	// 4. conversation after the last summarize, with clearing applied.
	for i := summarizeAt + 1; i < len(log); i++ {
		e := log[i]
		switch e.Type {
		case EventMessage:
			out = append(out, Segment{Kind: SegMessage, ID: e.ID})
		case EventToolCall:
			out = append(out, Segment{Kind: SegToolCall, ID: e.ID})
		case EventToolResult:
			if isCleared(i) {
				d := MustData[ToolResultData](e)
				out = append(out, Segment{Kind: SegToolResultCleared, ID: e.ID,
					Text: fmt.Sprintf("[result cleared: %s, %d bytes]", d.Tool, len(d.Content))})
			} else {
				out = append(out, Segment{Kind: SegToolResult, ID: e.ID})
			}
		}
	}

	// 5. suffix contexts after the last assistant message.
	for i := lastAssistant + 1; i < len(log); i++ {
		e := log[i]
		if e.Type == EventContext && MustData[ContextData](e).Slot == "suffix" {
			out = append(out, Segment{Kind: SegSuffixContext, ID: e.ID, Text: MustData[ContextData](e).Content})
		}
	}

	// 6. outstanding vetoes as one user message.
	if text := RenderVetoes(OutstandingVetoes(log)); text != "" {
		out = append(out, Segment{Kind: SegVetoes, Text: text})
	}
	return out
}
