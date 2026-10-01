package guard

import (
	"context"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// Seen live: a runner's verify session pushed its branch and opened its own
// PR, in auto mode, where a push is medium risk and allowed. A session its
// client labels guard:no-push may not publish, whatever its mode.
func TestANoPushSessionMayNotPublish(t *testing.T) {
	m := newGuard(t)
	ws := t.TempDir()
	noPush := fakeSession{workspace: ws, mode: protocol.PermissionAuto, labels: []string{"run:plan", NoPushLabel}}
	bypass := fakeSession{workspace: ws, mode: protocol.PermissionBypass, labels: []string{NoPushLabel}}
	plain := fakeSession{workspace: ws, mode: protocol.PermissionAuto}

	denied := []string{
		"git push origin nabu/x",
		"git -C . push",
		"git add . && git commit -m x && git push -u origin HEAD",
		`gh pr create --title "x" --body "y"`,
		"gh pr merge 5 --squash",
		"gh pr comment 5 --body hi",
		"gh issue comment 12 --body done",
		"gh release create v1",
		"gh api repos/k/x/issues/12/comments -f body=hi",
		"gh api -X POST repos/k/x/pulls",
		"gh api --method=PATCH repos/k/x/pulls/5",
		"gh api repos/k/x/pulls/5/reviews --input -",
	}
	for _, c := range denied {
		if v := m.GateTool(context.Background(), noPush, bashCall(c)); v.Decision != module.Deny {
			t.Errorf("%q in a no-push session: %v, want deny", c, v.Decision)
		}
		if v := m.GateTool(context.Background(), bypass, bashCall(c)); v.Decision != module.Deny {
			t.Errorf("%q in a no-push bypass session: %v, want deny", c, v.Decision)
		}
	}
	if v := m.GateTool(context.Background(), plain, bashCall("git push origin nabu/x")); v.Decision != module.Allow {
		t.Errorf("an unlabeled auto session may push as before: %v", v.Decision)
	}

	allowed := []string{
		"git status",
		"git commit -m 'push the button'",
		"git log --oneline origin/main..HEAD",
		"gh pr view 5 --json state",
		"gh pr list",
		"gh issue view 12",
		"gh api repos/k/x/pulls/5",
		"gh api -X GET repos/k/x/pulls",
		"go test ./...",
	}
	for _, c := range allowed {
		if v := m.GateTool(context.Background(), noPush, bashCall(c)); v.Decision == module.Deny {
			t.Errorf("%q in a no-push session was denied: %s", c, v.Reason)
		}
	}
}
