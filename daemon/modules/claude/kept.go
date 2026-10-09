package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// Keeping Claude's answers through a summary
// (docs/specs/2026-10-08-ask-survives-compaction-design.md).
//
// A summary covers the history up to its newest event, so after one a
// claude.ask answer survives only as the summariser's paraphrase, written by
// the same small model that needed the help. In one liftoff session that
// paraphrase dropped every per-test cause Claude had found and kept one the
// model had invented, and the model spent the next hour on it. The answers go
// back word for word instead, as a prefix block, which lasts until the next
// summary puts them back again.

// keptMax caps the whole block. keptLeast is the least room worth giving an
// answer: an answer cut to less than this says nothing, and is dropped and
// counted instead. droppedRoom is held back for the line that counts them.
const (
	keptMax     = 12 << 10
	keptLeast   = 256
	droppedRoom = 80
)

const keptHeader = "## Claude's answers in this task\n\n" +
	"Claude was asked for help during this task. Its answers are kept here word for word " +
	"through summarising. Some steps may already be done: check the files before redoing one.\n\n"

// keptAnswer is one claude.ask answer, and the stretch's turn when it came.
type keptAnswer struct {
	turn int
	text string
}

// answers is every claude.ask that worked in a stretch, oldest first: the
// module's automatic asks and the model's own alike.
func answers(since []protocol.Event) []keptAnswer {
	var out []keptAnswer
	turn := 0
	for _, e := range since {
		switch e.Type {
		case protocol.EventMessage:
			var d protocol.MessageData
			if json.Unmarshal(e.Data, &d) == nil && d.Role == "assistant" {
				turn++
			}
		case protocol.EventToolResult:
			var d protocol.ToolResultData
			if json.Unmarshal(e.Data, &d) == nil && d.Tool == "claude.ask" && d.Status == "ok" && strings.TrimSpace(d.Content) != "" {
				out = append(out, keptAnswer{turn: turn, text: d.Content})
			}
		}
	}
	return out
}

// keptBlock renders the answers under keptMax. The newest gets the room
// first, since it was written with the most of the task behind it; older ones
// take what is left, and any with no room left are counted instead.
func keptBlock(all []keptAnswer) string {
	room := keptMax - len(keptHeader) - droppedRoom
	sections := make([]string, len(all))
	dropped := 0
	for i := len(all) - 1; i >= 0; i-- {
		heading := fmt.Sprintf("### Answer %d, after turn %d\n\n", i+1, all[i].turn)
		if room-len(heading) < keptLeast && i < len(all)-1 {
			dropped = i + 1
			break
		}
		text := cut(strings.TrimSpace(all[i].text), room-len(heading)-2)
		sections[i] = heading + text + "\n\n"
		room -= len(sections[i])
	}

	var b strings.Builder
	b.WriteString(keptHeader)
	if dropped == 1 {
		b.WriteString("(1 earlier answer did not fit and is left out.)\n\n")
	} else if dropped > 1 {
		fmt.Fprintf(&b, "(%d earlier answers did not fit and are left out.)\n\n", dropped)
	}
	for _, s := range sections[dropped:] {
		b.WriteString(s)
	}
	return strings.TrimRight(b.String(), "\n")
}

// AfterCompaction implements module.CompactionHook: it puts the stretch's
// answers back once a summary has retired them.
func (m *Module) AfterCompaction(_ context.Context, s module.Session) ([]module.ContextBlock, error) {
	if !m.enabled || s == nil {
		return nil, nil
	}
	log, err := s.Events(nil)
	if err != nil {
		// No block leaves the session exactly where it would be without
		// this hook; disabling the module would also take claude.ask away.
		return nil, nil
	}
	_, since := stretchOf(log)
	all := answers(since)
	if len(all) == 0 {
		return nil, nil
	}
	return []module.ContextBlock{{Slot: "prefix", Content: keptBlock(all)}}, nil
}

// BeforeCompaction implements module.CompactionHook. It hands the summariser
// nothing: the answers go back word for word afterwards, and a summariser
// told to preserve them would only paraphrase them again.
func (m *Module) BeforeCompaction(context.Context, module.Session, module.Range) []string {
	return nil
}
