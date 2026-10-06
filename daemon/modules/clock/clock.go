// Package clock tells the model what day it is.
//
// Without it the model guesses the date from whatever it reads, and what it
// reads is mostly UTC: event timestamps, CI logs, `date -u`. On the evening of
// 5 October a comments session in liftoff dated a design amendment 6 October,
// the UTC date, though for the person it was still the 5th.
//
// The date is a prefix block, set when the session starts and again after a
// summarize retires it. A session that runs past midnight keeps the day it
// started on until its next compaction; the block says it is the start date so
// the model does not take it for a live clock.
package clock

import (
	"context"
	"fmt"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
)

// Module is the clock module.
type Module struct {
	enabled bool
	// now is the local time. Tests replace it.
	now func() time.Time
}

func (m *Module) Name() string { return "clock" }

func (m *Module) Init(_ module.Host, cfg module.Config) error {
	m.enabled = cfg.Enabled()
	if m.now == nil {
		m.now = time.Now
	}
	return nil
}

// SessionStart implements module.SessionStarter.
func (m *Module) SessionStart(context.Context, module.Session) ([]module.ContextBlock, error) {
	return m.blocks(), nil
}

// AfterCompaction implements module.CompactionHook. A summarize retires every
// prefix block, so the date goes back in, and is the date of the compaction.
func (m *Module) AfterCompaction(context.Context, module.Session) ([]module.ContextBlock, error) {
	return m.blocks(), nil
}

// BeforeCompaction implements module.CompactionHook. Nothing needs keeping in
// the summary: the date is put back afterwards.
func (m *Module) BeforeCompaction(context.Context, module.Session, module.Range) []string {
	return nil
}

func (m *Module) blocks() []module.ContextBlock {
	if !m.enabled {
		return nil
	}
	return []module.ContextBlock{{Slot: "prefix", Content: Line(m.now())}}
}

// Line is the date as the model is told it.
func Line(t time.Time) string {
	return fmt.Sprintf("This session started on %s, %s, in the local time of the machine nabu runs on (UTC%s). "+
		"Use the local date when you write one. Timestamps in logs and tool output are often UTC, which can be a day ahead or behind.",
		t.Format("Monday"), t.Format("2006-01-02"), t.Format("-07:00"))
}
