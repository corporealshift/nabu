package skills

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// logSession is a session with a workspace and a log the test can add to.
type logSession struct {
	dir    string
	events []protocol.Event
}

func (f *logSession) ID() string                  { return "S1" }
func (f *logSession) Workspace() module.Workspace { return module.Workspace{Path: f.dir, Key: "k"} }
func (f *logSession) State() protocol.State       { return protocol.State{} }
func (f *logSession) Events(*string) ([]protocol.Event, error) {
	return f.events, nil
}
func (f *logSession) Append(t protocol.EventType, data any) (protocol.Event, error) {
	raw, _ := json.Marshal(data)
	e := protocol.Event{Type: t, Data: raw}
	f.events = append(f.events, e)
	return e, nil
}

// androidModule has one skill covering Android build files.
func androidModule(t *testing.T) *Module {
	t.Helper()
	root := t.TempDir()
	writeRaw(t, root, "android-dev", "---\nname: android-dev\n"+
		"description: Use for any Android work on this machine.\n"+
		"paths: **/*.gradle.kts, **/AndroidManifest.xml, gradlew*\n---\n\nbody\n")
	writeSkill(t, root, "grill-me", "Use when Kyle wants a plan attacked.", "body")
	return loaded(t, root)
}

func call(tool, args string) protocol.ToolCallData {
	return protocol.ToolCallData{CallID: "c", Tool: tool, Arguments: json.RawMessage(args), Source: "model"}
}

// touch reports a tool call to the module and returns the blocks the next
// request gets, logging them as the agent would.
func touch(t *testing.T, m *Module, s *logSession, c protocol.ToolCallData) []module.ContextBlock {
	t.Helper()
	s.events = append(s.events, protocol.Event{Type: protocol.EventToolCall, Data: mustJSON(t, c)})
	m.ToolResult(context.Background(), s, c, protocol.ToolResultData{Status: "ok"})
	blocks, err := m.BeforeRequest(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range blocks {
		s.events = append(s.events, protocol.Event{Type: protocol.EventContext, Data: mustJSON(t,
			protocol.ContextData{Source: "module:skills", Slot: b.Slot, Content: b.Content})})
	}
	return blocks
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestGlobs(t *testing.T) {
	for _, tc := range []struct {
		glob, path string
		want       bool
	}{
		{"**/*.gradle.kts", "android/settings.gradle.kts", true},
		{"**/*.gradle.kts", "settings.gradle.kts", true},
		{"**/*.gradle.kts", "android/app/build.gradle", false},
		{"**/AndroidManifest.xml", "android/app/src/main/androidmanifest.xml", true},
		{"gradlew*", "android/gradlew.sh", true},
		{"gradlew*", "gradlew", true},
		{"src/*.go", "src/a/b.go", false},
		{"src/**", "src/a/b.go", true},
		{"docs/?.md", "docs/a.md", true},
	} {
		if got := compileGlob(tc.glob).MatchString(tc.path); got != tc.want {
			t.Errorf("%q against %q = %v, want %v", tc.glob, tc.path, got, tc.want)
		}
	}
}

func TestPathsAreReadFromTheFrontmatter(t *testing.T) {
	var got []string
	for _, sk := range androidModule(t).Skills() {
		if sk.Name == "android-dev" {
			for _, g := range sk.Covers {
				got = append(got, g.pattern)
			}
		}
	}
	if strings.Join(got, "|") != "**/*.gradle.kts|**/AndroidManifest.xml|gradlew*" {
		t.Errorf("paths = %q", got)
	}
}

// 01M3B65R: the first Android file written was android/settings.gradle.kts,
// and android-dev was never loaded in the 147 calls that followed.
func TestTouchingACoveredFileNamesTheSkillOnce(t *testing.T) {
	m := androidModule(t)
	s := &logSession{dir: t.TempDir()}

	blocks := touch(t, m, s, call("write", `{"path":"android/settings.gradle.kts","content":"x"}`))
	if len(blocks) != 1 || blocks[0].Slot != "suffix" {
		t.Fatalf("want one suffix block, got %+v", blocks)
	}
	for _, want := range []string{"`android/settings.gradle.kts`", "`android-dev`", "Use for any Android work", "Call `skill.load` with `android-dev`"} {
		if !strings.Contains(blocks[0].Content, want) {
			t.Errorf("reminder is missing %q:\n%s", want, blocks[0].Content)
		}
	}
	if strings.Contains(blocks[0].Content, "grill-me") {
		t.Errorf("a skill with no paths was named:\n%s", blocks[0].Content)
	}

	// Named once: later matching calls say nothing, even from a module that
	// has forgotten, as after a daemon restart.
	if again := touch(t, m, s, call("write", `{"path":"android/app/src/main/AndroidManifest.xml","content":"x"}`)); len(again) != 0 {
		t.Errorf("reminded twice: %+v", again)
	}
	if fresh := touch(t, androidModule(t), s, call("edit", `{"path":"android/app/build.gradle.kts"}`)); len(fresh) != 0 {
		t.Errorf("a restarted module reminded again: %+v", fresh)
	}
}

func TestALoadedSkillIsNotNamed(t *testing.T) {
	m := androidModule(t)
	s := &logSession{dir: t.TempDir()}
	touch(t, m, s, call("skill.load", `{"name":"Android-Dev"}`))

	if blocks := touch(t, m, s, call("write", `{"path":"android/settings.gradle.kts","content":"x"}`)); len(blocks) != 0 {
		t.Errorf("a loaded skill was named: %+v", blocks)
	}
}

func TestShellCommandsAndGlobsCount(t *testing.T) {
	for _, c := range []protocol.ToolCallData{
		call("bash", `{"command":"cd android && ./gradlew.sh :app:assembleDebug"}`),
		call("bash", `{"command":"cat \"android/app/build.gradle.kts\" | head"}`),
		call("glob", `{"pattern":"**/*.gradle.kts"}`),
		call("read", `{"path":"android/app/src/main/AndroidManifest.xml"}`),
	} {
		m := androidModule(t)
		s := &logSession{dir: t.TempDir()}
		if blocks := touch(t, m, s, c); len(blocks) != 1 {
			t.Errorf("%s %s: want a reminder, got %+v", c.Tool, c.Arguments, blocks)
		}
	}
}

func TestUnrelatedWorkNamesNothing(t *testing.T) {
	outside := filepath.ToSlash(filepath.Join(t.TempDir(), "elsewhere", "build.gradle.kts"))
	for _, c := range []protocol.ToolCallData{
		call("write", `{"path":"crates/server/src/main.rs","content":"x"}`),
		call("bash", `{"command":"cargo test --workspace"}`),
		call("grep", `{"pattern":"gradle.kts"}`), // a grep pattern is text, not a path
		call("read", `{"path":"`+outside+`"}`),
	} {
		m := androidModule(t)
		s := &logSession{dir: t.TempDir()}
		if blocks := touch(t, m, s, c); len(blocks) != 0 {
			t.Errorf("%s %s: unrelated work was reminded: %+v", c.Tool, c.Arguments, blocks)
		}
	}
}

// Git Bash names C:\x as /c/x, and the model writes it that way.
func TestAGitBashDrivePathIsInsideTheWorkspace(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("drive paths are a Windows form")
	}
	ws := t.TempDir()
	slash := filepath.ToSlash(ws)
	msys := "/" + strings.ToLower(slash[:1]) + slash[2:] + "/android/app/build.gradle.kts"
	if got := relative(ws, msys); got != "android/app/build.gradle.kts" {
		t.Errorf("relative(%q) = %q", msys, got)
	}
}
