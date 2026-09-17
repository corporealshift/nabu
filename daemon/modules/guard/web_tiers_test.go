package guard

import "testing"

// The web tools have no path, so nothing about the workspace applies to them.
// Searching is reading; fetching brings back whatever the page decides to say.
func TestWebToolRiskTiers(t *testing.T) {
	ws := t.TempDir()
	for _, tc := range []struct {
		tool string
		want Tier
	}{
		{"web.search", TierLow},
		{"web.fetch", TierMedium},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			got := classify(callInfo{tool: tc.tool, workspace: ws, inWorkspace: true})
			if got != tc.want {
				t.Errorf("classify(%s) = %v, want %v", tc.tool, got, tc.want)
			}
		})
	}
}
