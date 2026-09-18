package guard

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// fakeSession is the minimum module.Session a gate needs: a workspace and a
// permission mode.
type fakeSession struct {
	workspace string
	mode      protocol.PermissionMode
}

func (f fakeSession) ID() string                  { return "01ARZ3NDEKTSV4RRFFQ69G5FAV" }
func (f fakeSession) Workspace() module.Workspace { return module.Workspace{Path: f.workspace} }
func (f fakeSession) Events(*string) ([]protocol.Event, error) {
	return nil, nil
}
func (f fakeSession) State() protocol.State {
	return protocol.State{Options: protocol.Options{PermissionMode: f.mode}}
}
func (f fakeSession) Append(protocol.EventType, any) (protocol.Event, error) {
	return protocol.Event{}, nil
}

func bashCall(command string) protocol.ToolCallData {
	return call("bash", map[string]any{"command": command})
}

func call(tool string, args map[string]any) protocol.ToolCallData {
	raw, err := json.Marshal(args)
	if err != nil {
		panic(err)
	}
	return protocol.ToolCallData{CallID: "c1", Tool: tool, Arguments: raw, Source: "model"}
}

// newGuard builds a guard with the default rules.
func newGuard(t *testing.T) *Module {
	t.Helper()
	m := &Module{}
	if err := m.Init(nil, module.Config{}); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNameAndEmptyInit(t *testing.T) {
	m := &Module{}
	if got := m.Name(); got != "guard" {
		t.Errorf("Name: got %q, want %q", got, "guard")
	}
	if err := m.Init(nil, nil); err != nil {
		t.Fatalf("Init with no config: %v", err)
	}
	if len(m.rules) == 0 {
		t.Error("an unconfigured guard must install the default rules")
	}
}

func TestClassifyCommandRiskTiers(t *testing.T) {
	ws := t.TempDir()
	for _, tc := range []struct {
		command string
		want    Tier
	}{
		{"cat file.txt", TierLow},
		{"ls -la", TierLow},
		{"grep pattern .", TierLow},
		{"echo hello", TierLow},
		{"go test ./...", TierMedium},
		{"mkdir build", TierMedium},
		{"git commit -m x", TierMedium},
		{"npm install", TierMedium},
		{"rm -rf /", TierHigh}, // rooted: leaves the workspace
		{"sudo reboot", TierHigh},
		{"chmod 777 x", TierMedium}, // a relative target is ordinary work
		{"ssh host", TierHigh},
		{"", TierLow},

		// The riskiest word in a pipeline decides, not the first.
		{"cat x | sudo tee /etc/hosts", TierHigh},
		{"ls && rm -rf build", TierMedium}, // build/ is inside the workspace
		{"echo hi; git push", TierMedium},

		// A full path still names the program.
		{"/usr/bin/rm -rf x", TierMedium}, // the program is rm; x is relative
		{`C:\Windows\System32\rm.exe x`, TierMedium},
	} {
		if got := classifyCommand(tc.command, ws); got != tc.want {
			t.Errorf("classifyCommand(%q) = %v, want %v", tc.command, got, tc.want)
		}
	}
}

func TestClassifyToolTiers(t *testing.T) {
	ws := t.TempDir()
	inside := filepath.Join(ws, "a.txt")

	for _, tc := range []struct {
		name string
		call protocol.ToolCallData
		want Tier
	}{
		{"read is low", call("read", map[string]any{"path": inside}), TierLow},
		{"grep is low", call("grep", map[string]any{"path": inside}), TierLow},
		{"write is medium", call("write", map[string]any{"path": inside}), TierMedium},
		{"edit is medium", call("edit", map[string]any{"path": inside}), TierMedium},
		{"a sweeping edit is high", call("edit", map[string]any{"path": inside, "replace_all": true}), TierHigh},
		{"an unknown tool is low", call("teleport", map[string]any{}), TierLow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := inspect(fakeSession{workspace: ws}, tc.call)
			if info.tier != tc.want {
				t.Errorf("tier: got %v, want %v", info.tier, tc.want)
			}
		})
	}
}

// Leaving the workspace is the escape that matters, whatever the tool.
func TestPathOutsideWorkspaceIsHighRisk(t *testing.T) {
	ws := t.TempDir()
	info := inspect(fakeSession{workspace: ws}, call("read", map[string]any{"path": "/etc/passwd"}))
	if info.inWorkspace {
		t.Fatal("/etc/passwd must not count as inside the workspace")
	}
	if info.tier != TierHigh {
		t.Errorf("tier: got %v, want high", info.tier)
	}
}

func TestWithinWorkspace(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "home", "kyle", "proj")
	for _, tc := range []struct {
		path string
		want bool
	}{
		{filepath.Join(root, "main.go"), true},
		{filepath.Join(root, "a", "b", "c.go"), true},
		{root, true},
		{filepath.Join(string(filepath.Separator), "etc", "passwd"), false},
		{filepath.Join(string(filepath.Separator), "home", "kyle", "other"), false},
		// A sibling whose name merely starts with the root's name.
		{root + "-other" + string(filepath.Separator) + "x.go", false},
		{"relative.go", true},
	} {
		if got := withinWorkspace(root, tc.path); got != tc.want {
			t.Errorf("withinWorkspace(%q, %q) = %v, want %v", root, tc.path, got, tc.want)
		}
	}
	if !withinWorkspace("", "/anything") {
		t.Error("an empty workspace should not constrain anything")
	}
}

// Windows paths must compare correctly whichever separator they use.
func TestWithinWorkspaceNormalisesSeparators(t *testing.T) {
	root := filepath.FromSlash("C:/Users/corpo/proj")
	for _, p := range []string{
		filepath.FromSlash("C:/Users/corpo/proj/main.go"),
		`C:\Users\corpo\proj\main.go`,
		"C:/Users/corpo/proj/main.go",
	} {
		if !withinWorkspace(root, p) {
			t.Errorf("withinWorkspace(%q, %q) = false, want true", root, p)
		}
	}
}

// Spec 8: bypass approves everything. No rule overrides it.
func TestBypassApprovesEverything(t *testing.T) {
	ws := t.TempDir()
	m := newGuard(t)
	s := fakeSession{workspace: ws, mode: protocol.PermissionBypass}

	for _, c := range []protocol.ToolCallData{
		bashCall("rm -rf /"),
		bashCall("sudo reboot"),
		call("write", map[string]any{"path": "/etc/passwd"}),
		call("read", map[string]any{"path": filepath.Join(ws, "a.txt")}),
	} {
		v := m.GateTool(context.Background(), s, c)
		if v.Decision != module.Allow {
			t.Errorf("bypass must allow %q, got %v (%s)", c.Tool, v.Decision, v.Reason)
		}
	}
}

func TestAskModeAsksWhenNoRuleMatches(t *testing.T) {
	ws := t.TempDir()
	m := newGuard(t)
	s := fakeSession{workspace: ws, mode: protocol.PermissionAsk}

	v := m.GateTool(context.Background(), s, call("read", map[string]any{"path": filepath.Join(ws, "a.txt")}))
	if v.Decision != module.Ask {
		t.Fatalf("ask mode should ask, got %v", v.Decision)
	}
	if v.Summary == "" {
		t.Error("an Ask must carry a summary for the human")
	}
	if v.Risk == "" {
		t.Error("an Ask must carry a risk tier")
	}
}

// An empty permission mode is treated as ask, the documented default.
func TestEmptyPermissionModeDefaultsToAsk(t *testing.T) {
	ws := t.TempDir()
	m := newGuard(t)
	v := m.GateTool(context.Background(), fakeSession{workspace: ws},
		call("read", map[string]any{"path": filepath.Join(ws, "a.txt")}))
	if v.Decision != module.Ask {
		t.Errorf("an unset mode should ask, got %v", v.Decision)
	}
}

func TestAutoMode(t *testing.T) {
	ws := t.TempDir()
	m := newGuard(t)
	s := fakeSession{workspace: ws, mode: protocol.PermissionAuto}

	for _, tc := range []struct {
		name string
		call protocol.ToolCallData
		want module.Decision
	}{
		{"low risk is allowed", call("read", map[string]any{"path": filepath.Join(ws, "a.txt")}), module.Allow},
		{"a read-only command is allowed", bashCall("ls -la"), module.Allow},
		{"an edit inside the workspace is approved", call("write", map[string]any{"path": filepath.Join(ws, "a.txt")}), module.Allow},
		{"a build command is approved", bashCall("go build ./..."), module.Allow},
		{"deleting inside the workspace is approved", bashCall("rm -rf build"), module.Allow},
		{"deleting outside the workspace asks", bashCall("rm -rf /etc"), module.Ask},
		{"privilege escalation asks", bashCall("sudo reboot"), module.Ask},
		{"a path outside the workspace still asks", call("read", map[string]any{"path": "/etc/passwd"}), module.Ask},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := m.GateTool(context.Background(), s, tc.call)
			if v.Decision != tc.want {
				t.Errorf("decision: got %v, want %v (%s)", v.Decision, tc.want, v.Reason)
			}
		})
	}
}

// The built-in rules are what an unconfigured session gets, so they matter most.
// They ask rather than deny: a refusal the human never sees makes legitimate
// work impossible without editing config.
func TestDefaultRulesAskAboutTheDangerousThings(t *testing.T) {
	ws := t.TempDir()
	m := newGuard(t)

	for _, mode := range []protocol.PermissionMode{protocol.PermissionAsk, protocol.PermissionAuto} {
		s := fakeSession{workspace: ws, mode: mode}
		for _, tc := range []struct {
			name string
			call protocol.ToolCallData
			why  string
		}{
			{"destructive bash", bashCall("rm -rf /"), "destructive"},
			{"privilege escalation", bashCall("sudo reboot"), "destructive"},
			{"write outside the workspace", call("write", map[string]any{"path": "/etc/passwd"}), "outside"},
			{"read outside the workspace", call("read", map[string]any{"path": "/etc/shadow"}), "outside"},
		} {
			t.Run(string(mode)+"/"+tc.name, func(t *testing.T) {
				v := m.GateTool(context.Background(), s, tc.call)
				if v.Decision != module.Ask {
					t.Fatalf("decision: got %v, want Ask", v.Decision)
				}
				if v.Summary == "" {
					t.Error("an Ask must carry a summary for the human")
				}
				if !strings.Contains(v.Summary, tc.why) {
					t.Errorf("the summary should say why it is being asked, got %q", v.Summary)
				}
				if v.Risk == "" {
					t.Error("an Ask must carry a risk tier")
				}
			})
		}
	}
}

func TestWriteInsideWorkspaceAsksRatherThanDenies(t *testing.T) {
	ws := t.TempDir()
	m := newGuard(t)
	s := fakeSession{workspace: ws, mode: protocol.PermissionAsk}

	v := m.GateTool(context.Background(), s, call("write", map[string]any{"path": filepath.Join(ws, "main.go")}))
	if v.Decision != module.Ask {
		t.Errorf("a write inside the workspace should ask, got %v (%s)", v.Decision, v.Reason)
	}
}

// Malformed or absent arguments must not panic; the call falls back to being
// classified by tool name.
func TestMalformedArgumentsAreSurvivable(t *testing.T) {
	ws := t.TempDir()
	m := newGuard(t)
	s := fakeSession{workspace: ws, mode: protocol.PermissionAsk}

	for _, name := range []string{"malformed", "empty", "null"} {
		t.Run(name, func(t *testing.T) {
			c := protocol.ToolCallData{CallID: "c1", Tool: "bash", Source: "model"}
			switch name {
			case "malformed":
				c.Arguments = json.RawMessage(`{not json`)
			case "empty":
				c.Arguments = nil
			case "null":
				c.Arguments = json.RawMessage(`null`)
			}
			v := m.GateTool(context.Background(), s, c)
			if v.Decision != module.Ask {
				t.Errorf("decision: got %v, want Ask", v.Decision)
			}
		})
	}
}

// A nil session must not panic: the gate is defensive about its inputs.
func TestNilSessionIsSurvivable(t *testing.T) {
	m := newGuard(t)
	v := m.GateTool(context.Background(), nil, bashCall("ls"))
	if v.Decision != module.Ask {
		t.Errorf("decision: got %v, want Ask", v.Decision)
	}
}

func TestConfiguredRulesReplaceDefaults(t *testing.T) {
	ws := t.TempDir()
	m := &Module{}
	cfg := module.Config{"rules": []any{
		map[string]any{
			"verdict": "allow",
			"match":   map[string]any{"tool": "bash", "command_pattern": "^go test"},
		},
		map[string]any{
			"verdict": "deny",
			"reason":  "no network",
			"match":   map[string]any{"tool": "bash", "command_pattern": "curl"},
		},
	}}
	if err := m.Init(nil, cfg); err != nil {
		t.Fatal(err)
	}
	if len(m.rules) != 2 {
		t.Fatalf("rules: got %d, want 2", len(m.rules))
	}

	s := fakeSession{workspace: ws, mode: protocol.PermissionAsk}
	if v := m.GateTool(context.Background(), s, bashCall("go test ./...")); v.Decision != module.Allow {
		t.Errorf("an allow rule should allow, got %v", v.Decision)
	}
	if v := m.GateTool(context.Background(), s, bashCall("curl example.com")); v.Decision != module.Deny {
		t.Errorf("a deny rule should deny, got %v", v.Decision)
	} else if v.Reason != "no network" {
		t.Errorf("reason: got %q, want %q", v.Reason, "no network")
	}

	// Configured rules replaced the defaults, so an unmatched dangerous
	// command now falls through to the permission-mode default.
	if v := m.GateTool(context.Background(), s, bashCall("rm -rf /")); v.Decision != module.Ask {
		t.Errorf("an unmatched call should fall through to the mode default, got %v", v.Decision)
	}
}

// First match wins, so a deny ahead of an allow decides.
func TestFirstMatchingRuleWins(t *testing.T) {
	ws := t.TempDir()
	m := &Module{}
	cfg := module.Config{"rules": []any{
		map[string]any{"verdict": "deny", "reason": "first", "match": map[string]any{"tool": "bash"}},
		map[string]any{"verdict": "allow", "match": map[string]any{"tool": "bash"}},
	}}
	if err := m.Init(nil, cfg); err != nil {
		t.Fatal(err)
	}
	v := m.GateTool(context.Background(), fakeSession{workspace: ws, mode: protocol.PermissionAsk}, bashCall("ls"))
	if v.Decision != module.Deny || v.Reason != "first" {
		t.Errorf("got %v %q, want Deny \"first\"", v.Decision, v.Reason)
	}
}

func TestRiskMatchesTierAndAbove(t *testing.T) {
	ws := t.TempDir()
	m := &Module{}
	cfg := module.Config{"rules": []any{
		map[string]any{"verdict": "deny", "reason": "too risky", "match": map[string]any{"risk": "medium"}},
	}}
	if err := m.Init(nil, cfg); err != nil {
		t.Fatal(err)
	}
	s := fakeSession{workspace: ws, mode: protocol.PermissionAsk}

	if v := m.GateTool(context.Background(), s, bashCall("go build ./...")); v.Decision != module.Deny {
		t.Errorf("medium risk should match a medium rule, got %v", v.Decision)
	}
	if v := m.GateTool(context.Background(), s, bashCall("rm -rf /")); v.Decision != module.Deny {
		t.Errorf("high risk should match a medium rule, got %v", v.Decision)
	}
	if v := m.GateTool(context.Background(), s, bashCall("ls")); v.Decision == module.Deny {
		t.Error("low risk should not match a medium rule")
	}
}

func TestConfigErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  module.Config
		want string
	}{
		{"invalid regex", module.Config{"rules": []any{
			map[string]any{"verdict": "deny", "match": map[string]any{"command_pattern": "[invalid"}},
		}}, "command_pattern"},
		{"unknown verdict", module.Config{"rules": []any{
			map[string]any{"verdict": "maybe"},
		}}, "unknown verdict"},
		{"missing verdict", module.Config{"rules": []any{
			map[string]any{"match": map[string]any{"tool": "bash"}},
		}}, "verdict is required"},
		{"unknown risk", module.Config{"rules": []any{
			map[string]any{"verdict": "deny", "match": map[string]any{"risk": "spicy"}},
		}}, "unknown risk"},
		{"rules not a list", module.Config{"rules": "nope"}, "must be a list"},
		{"rule not an object", module.Config{"rules": []any{"nope"}}, "must be an object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Module{}
			err := m.Init(nil, tc.cfg)
			if err == nil {
				t.Fatalf("want an error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should mention %q", err, tc.want)
			}
		})
	}
}

// The registry is what actually calls the gate, so the module must satisfy the
// interface and behave through that dispatch.
func TestRegistryDispatchesToGuard(t *testing.T) {
	var _ module.ToolGate = (*Module)(nil)

	ws := t.TempDir()
	g := &Module{}
	if err := g.Init(nil, module.Config{}); err != nil {
		t.Fatal(err)
	}
	r := module.NewRegistry([]module.Module{g}, module.Options{})
	r.Init(
		func(string) module.Host { return nil },
		func(string) module.Config { return module.Config{} },
	)

	s := fakeSession{workspace: ws, mode: protocol.PermissionAsk}
	if v := r.GateTool(context.Background(), s, bashCall("rm -rf /")); v.Decision != module.Ask {
		t.Errorf("through the registry: got %v, want Ask", v.Decision)
	}
	if v := r.GateTool(context.Background(), s, bashCall("ls")); v.Decision != module.Ask {
		t.Errorf("through the registry: got %v, want Ask", v.Decision)
	}
}

// Spec 8, amended 2026-09-14: auto is the default. Guard's own rules already
// stop the dangerous calls, so asking on top of that prompted for every read
// and every edit and made the agent unusable to watch.
func TestAutoModeLetsOrdinaryWorkThrough(t *testing.T) {
	ws := t.TempDir()
	m := newGuard(t)
	s := fakeSession{workspace: ws, mode: protocol.PermissionAuto}

	for _, c := range []protocol.ToolCallData{
		call("read", map[string]any{"path": filepath.Join(ws, "main.go")}),
		call("write", map[string]any{"path": filepath.Join(ws, "main.go")}),
		call("edit", map[string]any{"path": filepath.Join(ws, "main.go")}),
		call("grep", map[string]any{"path": ws}),
		bashCall("go test ./..."),
		bashCall("git commit -m x"),
		bashCall("ls -la"),
	} {
		v := m.GateTool(context.Background(), s, c)
		if v.Decision != module.Allow {
			t.Errorf("auto should approve %q without asking, got %v (%s)", c.Tool, v.Decision, v.Summary)
		}
	}
}

// ask stays conservative: it is the mode for when you want to see everything.
func TestAskModeStillAsksForOrdinaryWork(t *testing.T) {
	ws := t.TempDir()
	m := newGuard(t)
	s := fakeSession{workspace: ws, mode: protocol.PermissionAsk}

	v := m.GateTool(context.Background(), s, call("write", map[string]any{"path": filepath.Join(ws, "main.go")}))
	if v.Decision != module.Ask {
		t.Errorf("ask mode should still ask about a write, got %v", v.Decision)
	}
}

// Deny is reserved for rules someone configured deliberately.
func TestDenyOnlyComesFromConfiguredRules(t *testing.T) {
	ws := t.TempDir()
	unconfigured := newGuard(t)
	s := fakeSession{workspace: ws, mode: protocol.PermissionAuto}
	for _, c := range []protocol.ToolCallData{
		bashCall("rm -rf /"), bashCall("sudo rm -rf /"),
		call("write", map[string]any{"path": "/etc/passwd"}),
	} {
		if v := unconfigured.GateTool(context.Background(), s, c); v.Decision == module.Deny {
			t.Errorf("an unconfigured guard should ask, not deny: %q", c.Tool)
		}
	}

	configured := &Module{}
	if err := configured.Init(nil, module.Config{"rules": []any{
		map[string]any{"verdict": "deny", "reason": "never", "match": map[string]any{"tool": "bash"}},
	}}); err != nil {
		t.Fatal(err)
	}
	if v := configured.GateTool(context.Background(), s, bashCall("ls")); v.Decision != module.Deny {
		t.Errorf("a configured deny should deny, got %v", v.Decision)
	}
}

// The split that matters: a command is judged by what it points at, not by
// its name. "rm" is not dangerous; "rm" pointed outside the workspace is.
func TestDestructiveCommandsAreJudgedByTarget(t *testing.T) {
	ws := t.TempDir()
	for _, tc := range []struct {
		command string
		want    Tier
		why     string
	}{
		// Ordinary work inside the workspace.
		{"rm -rf build", TierMedium, "a relative target cannot leave"},
		{"rm -rf ./node_modules", TierMedium, "explicitly relative"},
		{"rm dist/bundle.js", TierMedium, "nested but inside"},
		{"chmod +x scripts/run.sh", TierMedium, "a mode is not a path"},
		{"chmod 755 build/out", TierMedium, "a numeric mode is not a path"},
		{"mv old.go new.go", TierMedium, "both inside"},
		{"rmdir tmp", TierMedium, "inside"},

		// Leaving the workspace.
		{"rm -rf /", TierHigh, "the filesystem root"},
		{"rm -rf /etc/nginx", TierHigh, "absolute and outside"},
		{"rm -rf ~/Documents", TierHigh, "a home directory is not workspace work"},
		{"rm -rf ../../other", TierHigh, "climbs out"},
		{"chmod 777 /etc/passwd", TierHigh, "absolute and outside"},
		{"mv secrets.txt /tmp/", TierHigh, "moving out of the workspace"},

		// No benign form, whatever the arguments.
		{"sudo anything", TierHigh, "privilege escalation"},
		{"dd if=/dev/zero of=/dev/sda", TierHigh, "raw devices"},
		{"ssh host", TierHigh, "reaches another machine"},
		{"apt install x", TierHigh, "system package manager"},
		{"systemctl restart nginx", TierHigh, "system state"},
	} {
		if got := classifyCommand(tc.command, ws); got != tc.want {
			t.Errorf("classifyCommand(%q) = %v, want %v (%s)", tc.command, got, tc.want, tc.why)
		}
	}
}

// The commands this change exists for: a script or a patch over many files
// must not stop.
func TestOrdinaryWorkIsNeverHighRisk(t *testing.T) {
	ws := t.TempDir()
	for _, command := range []string{
		"sed -i 's/a/b/g' **/*.go",
		"awk '{print $1}' data.txt",
		"git apply patch.diff",
		"python scripts/parse.py",
		"node tools/build.js",
		"go test ./...",
		"make build",
		"npm install",
		"find . -name '*.go' | xargs grep TODO",
		"cat a.txt | sed 's/x/y/' | tee b.txt",
	} {
		if got := classifyCommand(command, ws); got == TierHigh {
			t.Errorf("classifyCommand(%q) = high; ordinary work must not stop", command)
		}
	}
}

// A pipeline is judged by its worst segment, and the arguments of each
// segment are read, not just the first word.
func TestPipelineSegmentsAreJudgedSeparately(t *testing.T) {
	ws := t.TempDir()
	if got := classifyCommand("ls | rm -rf build", ws); got == TierHigh {
		t.Error("a relative delete in a pipeline is still ordinary work")
	}
	if got := classifyCommand("ls | rm -rf /etc", ws); got != TierHigh {
		t.Errorf("a rooted delete in a pipeline must be high, got %v", got)
	}
	if got := classifyCommand("cat x | sudo tee /etc/hosts", ws); got != TierHigh {
		t.Errorf("privilege escalation anywhere in a pipeline is high, got %v", got)
	}
}

func TestEscapesWorkspace(t *testing.T) {
	ws := t.TempDir()
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"-rf", "build"}, false},
		{[]string{"-rf", "./build"}, false},
		{[]string{"+x", "script.sh"}, false},
		{[]string{"755", "file"}, false},
		{[]string{"-rf", "/"}, true},
		{[]string{"-rf", "~/x"}, true},
		{[]string{"-rf", "../../x"}, true},
		{[]string{"-rf"}, false},
		{[]string{}, false},
		{[]string{""}, false},
	} {
		if got := escapesWorkspace(tc.args, ws); got != tc.want {
			t.Errorf("escapesWorkspace(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

// git and gh read by default and write only for named ops, so the verb has to
// decide the tier. Rating them by tool name would either gate every "git
// status" or wave through "git commit".
func TestVcsToolsAreRatedByTheirOp(t *testing.T) {
	cases := []struct {
		name string
		tool string
		op   string
		want Tier
	}{
		{"git status reads", "git", "status", TierLow},
		{"git log reads", "git", "log", TierLow},
		{"git diff reads", "git", "diff", TierLow},
		{"git blame reads", "git", "blame", TierLow},
		{"git commit writes", "git", "commit", TierMedium},
		{"gh pr.list reads", "gh", "pr.list", TierLow},
		{"gh run.view reads", "gh", "run.view", TierLow},
		{"a call with no op cannot do anything", "git", "", TierLow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classify(callInfo{tool: tc.tool, op: tc.op, inWorkspace: true})
			if got != tc.want {
				t.Errorf("tier = %v, want %v", got, tc.want)
			}
		})
	}
}

// The op has to survive the trip from the tool call's arguments, or classify
// rates everything as the no-op case.
func TestInspectReadsTheOpArgument(t *testing.T) {
	info := inspect(nil, protocol.ToolCallData{
		Tool:      "git",
		Arguments: json.RawMessage(`{"op":"commit","message":"x"}`),
	})
	if info.op != "commit" {
		t.Fatalf("op = %q, want commit", info.op)
	}
	if info.tier != TierMedium {
		t.Errorf("tier = %v, want medium", info.tier)
	}
}
