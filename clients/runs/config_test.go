package runs

import (
	"os"
	"path/filepath"
	"testing"
)

// Sessions have no turn cap unless one is configured: caps of 50 and 100
// failed a live run whose sessions were still working. A configured cap is
// kept, and a negative one means none.
func TestTurnCaps(t *testing.T) {
	tests := []struct {
		name, json string
		plan, work int
	}{
		{"none configured", "", 0, 0},
		{"configured", `{"plan_turns": 40, "work_turns": 150}`, 40, 150},
		{"negative", `{"plan_turns": -1, "work_turns": -5}`, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.json != "" {
				if err := os.MkdirAll(filepath.Dir(ConfigPath(root)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(ConfigPath(root), []byte(tt.json), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			c, err := LoadConfig(root)
			if err != nil {
				t.Fatal(err)
			}
			if c.PlanTurns != tt.plan || c.WorkTurns != tt.work {
				t.Errorf("plan %d, work %d; want %d, %d", c.PlanTurns, c.WorkTurns, tt.plan, tt.work)
			}
		})
	}
}
