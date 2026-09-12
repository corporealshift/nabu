package agent

import (
	"context"
	"strconv"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// stopInfo gathers what a StopGate needs without a second round trip.
func stopInfo(log []protocol.Event, st protocol.State) module.StopInfo {
	info := module.StopInfo{Goal: st.Goal, Tasks: st.Tasks}
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Type != protocol.EventMessage {
			continue
		}
		d := protocol.MustData[protocol.MessageData](log[i])
		if d.Role == "assistant" {
			if info.LastAssistantMessage == "" {
				info.LastAssistantMessage = d.Content
			}
			info.TurnsSinceUser++
			continue
		}
		break // a user message ends the streak
	}
	info.VetoCount = len(vetoRounds(log))
	return info
}

// vetoRounds returns the reason sets of the consecutive trailing rounds in
// which the model produced no tool calls and a gate vetoed. A tool call or a
// new user message resets the streak, because both are progress.
func vetoRounds(log []protocol.Event) [][]string {
	var rounds [][]string
	var cur []string
	open := false
	closeRound := func() {
		if open && len(cur) > 0 {
			rounds = append(rounds, cur)
		}
		cur, open = nil, false
	}
	reset := func() {
		rounds, cur, open = nil, nil, false
	}
	for _, e := range log {
		switch e.Type {
		case protocol.EventMessage:
			if protocol.MustData[protocol.MessageData](e).Role == "assistant" {
				closeRound()
				open = true
			} else {
				reset()
			}
		case protocol.EventToolCall:
			reset()
		case protocol.EventStopVeto:
			if open {
				cur = append(cur, protocol.MustData[protocol.StopVetoData](e).Reason)
			}
		}
	}
	closeRound()
	return rounds
}

// sameReasons reports whether two veto reason sets are equal as ordered lists.
func sameReasons(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// stalled reports whether adding this round's vetoes would make n consecutive
// rounds with an unchanged set of reasons.
func stalled(log []protocol.Event, reasons []string, n int) bool {
	if n < 2 {
		return false
	}
	rounds := vetoRounds(log)
	if len(rounds) < n-1 {
		return false
	}
	for _, r := range rounds[len(rounds)-(n-1):] {
		if !sameReasons(r, reasons) {
			return false
		}
	}
	return true
}

// askStopGate runs every StopGate. It returns true when the loop should take
// another turn, having logged the vetoes; false when the session is finished
// (idle, or blocked because nothing is changing).
func (m *Manager) askStopGate(ctx context.Context, h *sessionHandle) (bool, error) {
	log := h.s.Events()
	st := protocol.Project(log)
	vetoes := m.deps.Modules.BeforeStop(ctx, h, stopInfo(log, st))
	if len(vetoes) == 0 {
		return false, m.toIdle(ctx, h, "turn_complete")
	}
	reasons := make([]string, 0, len(vetoes))
	for _, v := range vetoes {
		reasons = append(reasons, v.Reason)
	}
	appendVetoes := func() {
		for _, v := range vetoes {
			if _, err := h.s.Append(protocol.EventStopVeto, v); err != nil {
				m.log.Error("veto append failed", "session", h.ID(), "err", err)
			}
		}
	}
	if stalled(log, reasons, m.cfg.NoProgressTurns) {
		appendVetoes()
		h.s.Append(protocol.EventNotice, protocol.NoticeData{Source: "daemon", Level: "warn",
			Message: "stopping: the same objections stood for " +
				strconv.Itoa(m.cfg.NoProgressTurns) + " turns with no tool use"})
		return false, m.finish(ctx, h, protocol.StateBlocked, "no progress against outstanding vetoes")
	}
	if len(vetoRounds(log))+1 > m.cfg.MaxConsecutiveVetoes {
		appendVetoes()
		h.s.Append(protocol.EventNotice, protocol.NoticeData{Source: "daemon", Level: "warn",
			Message: "stopping: " + strconv.Itoa(m.cfg.MaxConsecutiveVetoes) + " consecutive veto rounds"})
		return false, m.finish(ctx, h, protocol.StateBlocked, "veto limit reached")
	}
	appendVetoes()
	return true, nil
}

// budgetExceeded reports whether the active budget forbids another turn.
func budgetExceeded(st protocol.State) (bool, string) {
	b := st.Budget
	if b.MaxTurns > 0 && st.Turns >= b.MaxTurns {
		return true, "budget: " + strconv.Itoa(st.Turns) + " of " + strconv.Itoa(b.MaxTurns) + " turns used"
	}
	if b.MaxTokens > 0 && st.Usage.InputTokens+st.Usage.OutputTokens >= b.MaxTokens {
		return true, "budget: token cap reached"
	}
	return false, ""
}
