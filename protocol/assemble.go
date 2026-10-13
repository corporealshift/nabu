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

	var out []Segment

	// 2. prefix contexts after the last summarize.
	for i := summarizeAt + 1; i < len(log); i++ {
		e := log[i]
		if e.Type == EventContext && MustData[ContextData](e).Slot == "prefix" {
			out = append(out, Segment{Kind: SegPrefixContext, ID: e.ID, Text: MustData[ContextData](e).Content})
		}
	}

	// 3. summary + current state. The state is the log's as of the summary,
	// so the system message stays the same, byte for byte, until the next
	// summarize: a provider's prompt cache can only resume a prompt whose
	// start has not changed. What happened since is in the messages.
	if summarizeAt >= 0 {
		out = append(out, Segment{Kind: SegSummary, ID: log[summarizeAt].ID, Text: MustData[CompactionData](log[summarizeAt]).Summary})
		out = append(out, Segment{Kind: SegState, Text: RenderStateBlock(log[:summarizeAt+1])})
	}

	// 4. conversation after the last summarize, with clearing applied. A
	// suffix context stays where it was appended, so each request is the one
	// before it plus what was logged since, and a provider's prompt cache can
	// resume at its end.
	for i := summarizeAt + 1; i < len(log); i++ {
		e := log[i]
		switch e.Type {
		case EventContext:
			if d := MustData[ContextData](e); d.Slot == "suffix" {
				out = append(out, Segment{Kind: SegSuffixContext, ID: e.ID, Text: d.Content})
			}
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

	// 5. outstanding vetoes as one user message.
	if text := RenderVetoes(OutstandingVetoes(log)); text != "" {
		out = append(out, Segment{Kind: SegVetoes, Text: text})
	}
	return out
}
