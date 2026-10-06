package clock

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
)

// The liftoff case: 21:30 on 5 October at UTC-7 is already 6 October in UTC.
// The model is told the 5th.
func TestTheDateIsLocal(t *testing.T) {
	at := time.Date(2026, 10, 5, 21, 30, 0, 0, time.FixedZone("PDT", -7*60*60))
	m := &Module{now: func() time.Time { return at }}
	if err := m.Init(nil, module.Config{}); err != nil {
		t.Fatal(err)
	}

	for name, get := range map[string]func() ([]module.ContextBlock, error){
		"session start":    func() ([]module.ContextBlock, error) { return m.SessionStart(context.Background(), nil) },
		"after compaction": func() ([]module.ContextBlock, error) { return m.AfterCompaction(context.Background(), nil) },
	} {
		blocks, err := get()
		if err != nil || len(blocks) != 1 || blocks[0].Slot != "prefix" {
			t.Fatalf("%s: want one prefix block, got %+v (%v)", name, blocks, err)
		}
		for _, want := range []string{"Monday, 2026-10-05", "UTC-07:00"} {
			if !strings.Contains(blocks[0].Content, want) {
				t.Errorf("%s: %q does not say %q", name, blocks[0].Content, want)
			}
		}
		if strings.Contains(blocks[0].Content, "2026-10-06") {
			t.Errorf("%s: %q gives the UTC date", name, blocks[0].Content)
		}
	}
}

func TestDisabledSaysNothing(t *testing.T) {
	m := &Module{}
	if err := m.Init(nil, module.Config{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	if blocks, _ := m.SessionStart(context.Background(), nil); len(blocks) != 0 {
		t.Fatalf("a disabled clock should inject nothing, got %+v", blocks)
	}
}
