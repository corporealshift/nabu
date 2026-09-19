package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
)

// writeSkill creates dir/name/SKILL.md with the given frontmatter and body.
func writeSkill(t *testing.T, root, name, description, body string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", name, description, body)
	path := filepath.Join(dir, SkillFile)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeRaw creates dir/name/SKILL.md with exactly the given content.
func writeRaw(t *testing.T, root, name, content string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, SkillFile)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// loaded builds a module over the given directories.
func loaded(t *testing.T, dirs ...string) *Module {
	t.Helper()
	m := &Module{}
	raw := make([]any, len(dirs))
	for i, d := range dirs {
		raw[i] = d
	}
	if err := m.Init(nil, module.Config{"paths": raw}); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNameAndInitWithNoPaths(t *testing.T) {
	m := &Module{}
	if got := m.Name(); got != "skills" {
		t.Errorf("Name: got %q, want %q", got, "skills")
	}
	if err := m.Init(nil, nil); err != nil {
		t.Fatalf("Init with no config: %v", err)
	}
}

func TestDiscoverFindsSkills(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "brainstorming", "Use before creative work.", "# Brainstorming")
	writeSkill(t, root, "verifying-work", "Use before claiming done.", "# Verifying")

	m := loaded(t, root)
	got := m.Skills()
	if len(got) != 2 {
		t.Fatalf("skills: got %d, want 2", len(got))
	}
	// Sorted by name, so the order is stable across filesystems.
	if got[0].Name != "brainstorming" || got[1].Name != "verifying-work" {
		t.Fatalf("order: got %q then %q", got[0].Name, got[1].Name)
	}
	if got[0].Description != "Use before creative work." {
		t.Errorf("description: got %q", got[0].Description)
	}
	if !strings.HasSuffix(got[0].Path, SkillFile) {
		t.Errorf("path should point at %s, got %q", SkillFile, got[0].Path)
	}
}

func TestDiscoverRecursesButNotIntoASkill(t *testing.T) {
	root := t.TempDir()
	// A skill nested two levels down is found.
	writeSkill(t, filepath.Join(root, "a", "b"), "deep", "Nested skill.", "body")
	// A SKILL.md inside a skill's own subdirectory is not a second skill.
	outer := filepath.Join(root, "outer")
	writeSkill(t, root, "outer", "The outer skill.", "body")
	writeRaw(t, outer, "examples", "---\nname: should-not-appear\ndescription: x\n---\n")

	m := loaded(t, root)
	names := map[string]bool{}
	for _, s := range m.Skills() {
		names[s.Name] = true
	}
	if !names["deep"] {
		t.Error("a nested skill should be discovered")
	}
	if !names["outer"] {
		t.Error("the outer skill should be discovered")
	}
	if names["should-not-appear"] {
		t.Error("a SKILL.md inside a skill must not register a second skill")
	}
}

// Entries under ~/.claude/skills are commonly symlinks, and the standard
// walker does not follow them, so skipping them would silently lose skills.
func TestDiscoverFollowsSymlinks(t *testing.T) {
	root := t.TempDir()
	elsewhere := t.TempDir()
	writeSkill(t, elsewhere, "linked", "Lives somewhere else.", "body")

	link := filepath.Join(root, "linked")
	if err := os.Symlink(filepath.Join(elsewhere, "linked"), link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("creating a symlink needs privilege on this machine")
		}
		t.Fatal(err)
	}

	m := loaded(t, root)
	if len(m.Skills()) != 1 || m.Skills()[0].Name != "linked" {
		t.Fatalf("a symlinked skill should be found, got %+v", m.Skills())
	}
}

// A symlink loop must not hang discovery.
func TestDiscoverSurvivesASymlinkLoop(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "real", "A real skill.", "body")
	loop := filepath.Join(root, "loop")
	if err := os.Symlink(root, loop); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("creating a symlink needs privilege on this machine")
		}
		t.Fatal(err)
	}

	m := loaded(t, root)
	if len(m.Skills()) != 1 {
		t.Fatalf("skills: got %d, want 1", len(m.Skills()))
	}
}

// One bad skill on disk must not cost the others.
func TestMalformedSkillsAreSkipped(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "good", "A usable skill.", "body")
	writeRaw(t, root, "no-frontmatter", "# Just a heading\n\nNo frontmatter here.\n")
	writeRaw(t, root, "unterminated", "---\nname: x\ndescription: y\n")
	writeRaw(t, root, "no-name", "---\ndescription: has no name\n---\n\nbody\n")

	m := loaded(t, root)
	if len(m.Skills()) != 1 {
		t.Fatalf("skills: got %d, want 1 (%+v)", len(m.Skills()), m.Skills())
	}
	if m.Skills()[0].Name != "good" {
		t.Errorf("the usable skill should survive, got %q", m.Skills()[0].Name)
	}
}

// A description is optional; a name is not.
func TestMissingDescriptionIsAccepted(t *testing.T) {
	root := t.TempDir()
	writeRaw(t, root, "terse", "---\nname: terse\n---\n\nbody\n")

	m := loaded(t, root)
	if len(m.Skills()) != 1 {
		t.Fatalf("skills: got %d, want 1", len(m.Skills()))
	}
	if m.Skills()[0].Description != "" {
		t.Errorf("description should be empty, got %q", m.Skills()[0].Description)
	}
	if !strings.Contains(FormatIndex(m.Skills()), "(no description)") {
		t.Error("the index should say a description is missing rather than showing nothing")
	}
}

func TestDuplicateNamesKeepTheFirstFound(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	writeSkill(t, first, "shared", "From the first directory.", "body")
	writeSkill(t, second, "shared", "From the second directory.", "body")

	m := loaded(t, first, second)
	if len(m.Skills()) != 1 {
		t.Fatalf("skills: got %d, want 1", len(m.Skills()))
	}
	if m.Skills()[0].Description != "From the first directory." {
		t.Errorf("the first directory should win, got %q", m.Skills()[0].Description)
	}
}

func TestMissingDirectoryIsNotAnError(t *testing.T) {
	m := loaded(t, filepath.Join(t.TempDir(), "does-not-exist"))
	if len(m.Skills()) != 0 {
		t.Errorf("skills: got %d, want 0", len(m.Skills()))
	}
}

func TestFrontmatterVariants(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    string
		ok      bool
	}{
		{"plain", "---\nname: a\ndescription: d\n---\n", "a", true},
		{"quoted", "---\nname: \"a\"\ndescription: \"d\"\n---\n", "a", true},
		{"single quoted", "---\nname: 'a'\n---\n", "a", true},
		{"crlf", "---\r\nname: a\r\ndescription: d\r\n---\r\n", "a", true},
		{"extra keys ignored", "---\nname: a\nversion: 3\n---\n", "a", true},
		{"colon in description", "---\nname: a\ndescription: use when: always\n---\n", "a", true},
		{"no frontmatter", "# heading\n", "", false},
		{"unterminated", "---\nname: a\n", "", false},
		{"no name", "---\ndescription: d\n---\n", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := writeRaw(t, root, "s", tc.content)
			got, err := parseSkill(path)
			if !tc.ok {
				if err == nil {
					t.Fatalf("want an error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSkill: %v", err)
			}
			if got.Name != tc.want {
				t.Errorf("name: got %q, want %q", got.Name, tc.want)
			}
		})
	}
}

// A colon inside a description must not be lost: only the first one separates.
func TestDescriptionKeepsLaterColons(t *testing.T) {
	root := t.TempDir()
	path := writeRaw(t, root, "s", "---\nname: a\ndescription: use when: always\n---\n")
	got, err := parseSkill(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != "use when: always" {
		t.Errorf("description: got %q", got.Description)
	}
}

func TestSessionStartReturnsAPrefixBlock(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "brainstorming", "Use before creative work.", "body")
	m := loaded(t, root)

	blocks, err := m.SessionStart(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks: got %d, want 1", len(blocks))
	}
	if blocks[0].Slot != "prefix" {
		t.Errorf("slot: got %q, want %q", blocks[0].Slot, "prefix")
	}
	if !strings.Contains(blocks[0].Content, "brainstorming") {
		t.Error("the index should name the skill")
	}
	if !strings.Contains(blocks[0].Content, "skill.load") {
		t.Error("the index should tell the model how to read a skill")
	}
}

// A block announcing that there are no skills is context spent to say nothing.
func TestNoSkillsMeansNoBlock(t *testing.T) {
	m := loaded(t, t.TempDir())
	blocks, err := m.SessionStart(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 0 {
		t.Fatalf("blocks: got %d, want 0", len(blocks))
	}
}

// A prefix block is fixed between compactions, so a summarize retires the
// index and it must be put back or the model forgets which skills exist.
func TestAfterCompactionReinjectsTheIndex(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "brainstorming", "Use before creative work.", "body")
	m := loaded(t, root)

	start, err := m.SessionStart(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	after, err := m.AfterCompaction(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 {
		t.Fatalf("blocks after compaction: got %d, want 1", len(after))
	}
	if after[0].Content != start[0].Content {
		t.Error("the re-injected index should match the one from session start")
	}
	if after[0].Slot != "prefix" {
		t.Errorf("slot: got %q, want prefix", after[0].Slot)
	}
}

func TestBeforeCompactionPreservesNothing(t *testing.T) {
	m := loaded(t, t.TempDir())
	if got := m.BeforeCompaction(context.Background(), nil, module.Range{}); got != nil {
		t.Errorf("preserve: got %v, want nil — the index is re-injected instead", got)
	}
}

// The index rides on every request, so its size is a standing cost.
func TestIndexIsCapped(t *testing.T) {
	var many []Skill
	long := strings.Repeat("x", 400)
	for i := 0; i < 600; i++ {
		many = append(many, Skill{Name: fmt.Sprintf("skill-%03d", i), Description: long})
	}
	index := FormatIndex(many)
	if len(index) > maxIndexBytes {
		t.Fatalf("index is %d bytes, over the %d cap", len(index), maxIndexBytes)
	}
	// Names survive before descriptions do, because a name alone is loadable.
	if !strings.Contains(index, "skill-000") {
		t.Error("names should be kept in preference to descriptions")
	}
}

// A truncated list must not be mistaken for the whole set.
func TestOverflowingIndexSaysSo(t *testing.T) {
	var many []Skill
	for i := 0; i < 5000; i++ {
		many = append(many, Skill{Name: fmt.Sprintf("a-very-long-skill-name-number-%05d", i)})
	}
	index := FormatIndex(many)
	if len(index) > maxIndexBytes {
		t.Fatalf("index is %d bytes, over the %d cap", len(index), maxIndexBytes)
	}
	if !strings.Contains(index, "not listed") {
		t.Error("an incomplete index must say that it is incomplete")
	}
}

func TestDescriptionsAreTruncated(t *testing.T) {
	long := strings.Repeat("y", 500)
	index := FormatIndex([]Skill{{Name: "s", Description: long}})
	if strings.Contains(index, long) {
		t.Error("a long description should be shortened in the index")
	}
	if !strings.Contains(index, "…") {
		t.Error("a shortened description should show that it was cut")
	}
}

func TestSkillLoadTool(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "brainstorming", "Use before creative work.", "# The body\n\nDetails here.")
	m := loaded(t, root)

	tools := m.Tools()
	if len(tools) != 1 {
		t.Fatalf("tools: got %d, want 1", len(tools))
	}
	tool := tools[0]
	if tool.Name != "skill.load" {
		t.Errorf("name: got %q, want %q", tool.Name, "skill.load")
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.Schema, &schema); err != nil {
		t.Fatalf("the schema must be valid JSON: %v", err)
	}

	out, err := tool.Run(context.Background(), nil, json.RawMessage(`{"name":"brainstorming"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "The body") {
		t.Error("loading a skill should return its body")
	}
	if !strings.Contains(out, "name: brainstorming") {
		t.Error("the body is returned verbatim, frontmatter included")
	}
}

func TestSkillLoadErrors(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "known", "A skill.", "body")
	m := loaded(t, root)
	run := m.Tools()[0].Run

	for _, tc := range []struct {
		name string
		args string
		want string
	}{
		{"unknown skill", `{"name":"nope"}`, "no skill named"},
		{"missing name", `{}`, "name is required"},
		{"blank name", `{"name":"   "}`, "name is required"},
		{"malformed arguments", `{not json`, "invalid arguments"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := run(context.Background(), nil, json.RawMessage(tc.args))
			if err == nil {
				t.Fatalf("want an error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should mention %q", err, tc.want)
			}
		})
	}
}

// A name is how the model addresses a skill, so matching should not hinge on
// the case it happened to copy.
func TestSkillLoadIsCaseInsensitive(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "Brainstorming", "A skill.", "body text")
	m := loaded(t, root)

	out, err := m.Tools()[0].Run(context.Background(), nil, json.RawMessage(`{"name":"brainstorming"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "body text") {
		t.Error("expected the skill body")
	}
}

func TestSkillLoadTruncatesAHugeBody(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "huge", "A big skill.", strings.Repeat("z", maxBodyBytes+5000))
	m := loaded(t, root)

	out, err := m.Tools()[0].Run(context.Background(), nil, json.RawMessage(`{"name":"huge"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "truncated") {
		t.Error("a truncated body must say so, or the model treats a fragment as the whole skill")
	}
}

func TestConfigErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  module.Config
		want string
	}{
		{"paths not a list", module.Config{"paths": "nope"}, "must be a list"},
		{"path not a string", module.Config{"paths": []any{3}}, "must be a string"},
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

// The module must satisfy every hook it claims, or the registry silently skips it.
func TestImplementsItsHooks(t *testing.T) {
	var m any = &Module{}
	if _, ok := m.(module.Module); !ok {
		t.Error("not a Module")
	}
	if _, ok := m.(module.SessionStarter); !ok {
		t.Error("not a SessionStarter")
	}
	if _, ok := m.(module.ToolProvider); !ok {
		t.Error("not a ToolProvider")
	}
	if _, ok := m.(module.CompactionHook); !ok {
		t.Error("not a CompactionHook")
	}
}

// Skills are edited far more often than the daemon is restarted, so a session
// reads the directories again when it opens. Before this, a daemon outlived
// every edit and the change only took after a restart nobody remembered.
func TestSessionStartSeesSkillsAddedSinceInit(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "brainstorming", "Use before creative work.", "body")
	m := loaded(t, root)

	if got := len(m.Skills()); got != 1 {
		t.Fatalf("at init: got %d skills, want 1", got)
	}

	// The daemon keeps running; the repository gains a skill.
	writeSkill(t, root, "grill-me", "Use to attack a plan.", "body")

	if _, err := m.SessionStart(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if got := len(m.Skills()); got != 2 {
		t.Fatalf("after a session opened: got %d skills, want 2", got)
	}

	// And the new one is loadable, not merely listed.
	out, err := m.runLoad(context.Background(), nil, []byte(`{"name":"grill-me"}`))
	if err != nil {
		t.Fatalf("skill.load on a newly added skill: %v", err)
	}
	if !strings.Contains(out, "body") {
		t.Errorf("loaded text was %q", out)
	}
}

// A skill deleted from the repository should stop being offered, or the model
// is told about something skill.load will then refuse.
func TestSessionStartDropsSkillsThatWentAway(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "keeper", "Stays.", "body")
	writeSkill(t, root, "goner", "Removed.", "body")
	m := loaded(t, root)

	if got := len(m.Skills()); got != 2 {
		t.Fatalf("at init: got %d, want 2", got)
	}
	if err := os.RemoveAll(filepath.Join(root, "goner")); err != nil {
		t.Fatal(err)
	}

	if _, err := m.SessionStart(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, s := range m.Skills() {
		names = append(names, s.Name)
	}
	if len(names) != 1 || names[0] != "keeper" {
		t.Errorf("skills = %v, want just [keeper]", names)
	}
}

// Sessions open concurrently, and each rescans. Run with -race.
func TestConcurrentSessionStartsAreSafe(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "brainstorming", "Use before creative work.", "body")
	m := loaded(t, root)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.SessionStart(context.Background(), nil); err != nil {
				t.Error(err)
			}
			_ = m.Skills()
		}()
	}
	wg.Wait()
}
