package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run calls one of the module's tools by name.
func run(t *testing.T, m *Module, s *fakeSession, name, args string) (string, error) {
	t.Helper()
	for _, tool := range m.Tools() {
		if tool.Name == name {
			return tool.Run(context.Background(), s, json.RawMessage(args))
		}
	}
	t.Fatalf("no tool named %s", name)
	return "", nil
}

func TestEveryToolDeclaresValidSchema(t *testing.T) {
	m := newModule(t)
	tools := m.Tools()
	want := map[string]bool{"memory.recall": false, "memory.save": false, "memory.forget": false}
	for _, tool := range tools {
		if _, ok := want[tool.Name]; !ok {
			t.Errorf("unexpected tool %s", tool.Name)
			continue
		}
		want[tool.Name] = true
		var schema map[string]any
		if err := json.Unmarshal(tool.Schema, &schema); err != nil {
			t.Errorf("%s: schema is not valid JSON: %v", tool.Name, err)
		}
		if tool.Description == "" {
			t.Errorf("%s: no description", tool.Name)
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing tool %s", name)
		}
	}
}

func TestSaveThenRecall(t *testing.T) {
	m := newModule(t)
	s := newSession(t, "repo-abc123")

	out, err := run(t, m, s, "memory.save", `{
		"scope":"workspace","name":"Gate Is Build Vet Test","type":"project",
		"description":"the project gate; run it whole",
		"body":"Run go build, go vet and go test together, not one package."
	}`)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !strings.Contains(out, "gate-is-build-vet-test") {
		t.Errorf("save did not report the name it used: %q", out)
	}

	out, err = run(t, m, s, "memory.recall", `{"query":"project gate"}`)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !strings.Contains(out, "go build, go vet and go test") {
		t.Errorf("recall did not return the body:\n%s", out)
	}
	if !strings.Contains(out, "workspace") {
		t.Errorf("recall did not name the scope of the hit:\n%s", out)
	}
}

func TestRecallDefaultsToBothScopes(t *testing.T) {
	m := newModule(t)
	s := newSession(t, "repo-abc123")
	if err := m.GlobalStore().Save(Memory{
		Name: "owner-uses-rancher", Type: "user",
		Description: "the owner runs Rancher Desktop", Body: "not Docker Desktop",
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.WorkspaceStore(s).Save(Memory{
		Name: "rancher-port-clash", Type: "project",
		Description: "8080 is taken", Body: "lopress already listens on 8080",
	}); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, m, s, "memory.recall", `{"query":"rancher"}`)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	for _, want := range []string{"owner-uses-rancher", "rancher-port-clash"} {
		if !strings.Contains(out, want) {
			t.Errorf("the default scope missed %s:\n%s", want, out)
		}
	}

	out, err = run(t, m, s, "memory.recall", `{"query":"rancher","scope":"workspace"}`)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if strings.Contains(out, "owner-uses-rancher") {
		t.Errorf("scope workspace returned a global memory:\n%s", out)
	}
	if !strings.Contains(out, "rancher-port-clash") {
		t.Errorf("scope workspace lost its own memory:\n%s", out)
	}
}

func TestRecallCapsAtFive(t *testing.T) {
	m := newModule(t)
	s := newSession(t, "repo-abc123")
	for i := 0; i < 12; i++ {
		name := "docker-note-" + string(rune('a'+i))
		if err := m.GlobalStore().Save(Memory{
			Name: name, Type: "project", Description: "a docker fact", Body: "docker docker",
		}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := run(t, m, s, "memory.recall", `{"query":"docker"}`)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if n := strings.Count(out, "docker-note-"); n > 5*3 {
		t.Logf("%s", out)
	}
	var hits int
	for i := 0; i < 12; i++ {
		if strings.Contains(out, "name: docker-note-"+string(rune('a'+i))) {
			hits++
		}
	}
	if hits != maxRecall {
		t.Errorf("recall returned %d memories, want at most %d", hits, maxRecall)
	}
}

func TestRecallFindingNothingSaysSo(t *testing.T) {
	m := newModule(t)
	s := newSession(t, "repo-abc123")
	if err := m.GlobalStore().Save(Memory{Name: "a", Type: "user", Description: "d", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, m, s, "memory.recall", `{"query":"kubernetes"}`)
	if err != nil {
		t.Fatalf("a miss is not an error: %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "no memor") {
		t.Errorf("a miss must say so plainly: %q", out)
	}
}

func TestRecallRejectsBadInput(t *testing.T) {
	m := newModule(t)
	s := newSession(t, "repo-abc123")
	for _, tc := range []struct{ name, args, want string }{
		{"no query", `{}`, "query"},
		{"bad scope", `{"query":"x","scope":"everything"}`, "everything"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := run(t, m, s, "memory.recall", tc.args)
			if err == nil {
				t.Fatal("want an error, got none")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestSaveRejectsBadInput(t *testing.T) {
	m := newModule(t)
	s := newSession(t, "repo-abc123")
	for _, tc := range []struct{ name, args, want string }{
		{"bad type", `{"scope":"global","name":"a","type":"notes","description":"d","body":"x"}`, "notes"},
		{"no name", `{"scope":"global","name":"","type":"user","description":"d","body":"x"}`, "name"},
		{"no body", `{"scope":"global","name":"a","type":"user","description":"d","body":""}`, "body"},
		{"bad scope", `{"scope":"all","name":"a","type":"user","description":"d","body":"x"}`, "all"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := run(t, m, s, "memory.save", tc.args)
			if err == nil {
				t.Fatal("want an error, got none")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	// The valid types must be named, or the model cannot correct itself.
	_, err := run(t, m, s, "memory.save", `{"scope":"global","name":"a","type":"notes","description":"d","body":"x"}`)
	if err == nil || !strings.Contains(err.Error(), "feedback") {
		t.Errorf("error = %v, want it to list the valid types", err)
	}
}

func TestSaveOverAnImportWritesToNabu(t *testing.T) {
	importDir := t.TempDir()
	content := "---\nname: shadowed\ndescription: \"imported\"\nmetadata:\n  type: project\n---\n\nthe imported version\n"
	if err := os.WriteFile(filepath.Join(importDir, "shadowed.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newModule(t, importDir)
	s := newSession(t, "repo-abc123")

	if _, err := run(t, m, s, "memory.save", `{
		"scope":"global","name":"shadowed","type":"project",
		"description":"the nabu version","body":"the nabu version of the fact"
	}`); err != nil {
		t.Fatalf("save: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(importDir, "shadowed.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "the imported version") {
		t.Error("the save wrote into the import directory")
	}
	if _, err := os.Stat(m.GlobalStore().Path("shadowed")); err != nil {
		t.Errorf("the nabu copy was not written: %v", err)
	}
}

func TestForgetTool(t *testing.T) {
	m := newModule(t)
	s := newSession(t, "repo-abc123")
	if _, err := run(t, m, s, "memory.save", `{
		"scope":"workspace","name":"doomed","type":"project","description":"d","body":"a fact about kafka"
	}`); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, m, s, "memory.forget", `{"name":"doomed"}`); err != nil {
		t.Fatalf("forget: %v", err)
	}
	out, err := run(t, m, s, "memory.recall", `{"query":"kafka"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "doomed") {
		t.Errorf("a forgotten memory is still recalled:\n%s", out)
	}

	_, err = run(t, m, s, "memory.forget", `{"name":"never-existed"}`)
	if err == nil || !strings.Contains(err.Error(), "never-existed") {
		t.Errorf("error = %v, want it to name the memory", err)
	}
}

func TestForgetRefusesAnImport(t *testing.T) {
	importDir := t.TempDir()
	content := "---\nname: theirs\ndescription: \"imported\"\nmetadata:\n  type: project\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(importDir, "theirs.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newModule(t, importDir)
	s := newSession(t, "repo-abc123")

	_, err := run(t, m, s, "memory.forget", `{"name":"theirs"}`)
	if err == nil {
		t.Fatal("forget deleted another tool's memory")
	}
	if _, statErr := os.Stat(filepath.Join(importDir, "theirs.md")); statErr != nil {
		t.Errorf("the import was removed: %v", statErr)
	}
}

func TestWritesAreCountedForTheCommit(t *testing.T) {
	m := newModule(t)
	s := newSession(t, "repo-abc123")
	if _, err := run(t, m, s, "memory.save", `{
		"scope":"global","name":"a","type":"user","description":"d","body":"x"
	}`); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, m, s, "memory.forget", `{"name":"a"}`); err != nil {
		t.Fatal(err)
	}
	w := m.takeWrites(s.ID())
	if w.saved != 1 || w.forgotten != 1 {
		t.Errorf("counted %+v, want one save and one forget", w)
	}
	// Reading changes nothing, so a recall-only session must not commit.
	if _, err := run(t, m, s, "memory.recall", `{"query":"anything"}`); err != nil {
		t.Fatal(err)
	}
	if again := m.takeWrites(s.ID()); again.saved != 0 || again.forgotten != 0 {
		t.Errorf("a recall counted as a write: %+v", again)
	}
}

func TestSaveRebuildsTheIndexFile(t *testing.T) {
	m := newModule(t)
	s := newSession(t, "repo-abc123")
	if _, err := run(t, m, s, "memory.save", `{
		"scope":"global","name":"indexed","type":"user","description":"a fact","body":"x"
	}`); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(m.GlobalStore().Dir(), IndexFile))
	if err != nil {
		t.Fatalf("the index file was not written: %v", err)
	}
	if !strings.Contains(string(raw), "indexed.md") {
		t.Errorf("index does not list the saved memory:\n%s", raw)
	}
}
