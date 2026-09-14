package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sample(name, desc, body string) Memory {
	return Memory{Name: name, Description: desc, Type: "project", Body: body}
}

func TestFormatIndexOneLinePerMemory(t *testing.T) {
	mems := []Memory{
		sample("rancher-desktop-docker", "Rancher Desktop, not Docker Desktop; 8080 taken by lopress", "launch the daemon before compose"),
		sample("nabu-project", "nabu is the owner's Go agent harness", "read the spec first"),
	}
	got := FormatIndex(mems)

	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want one per memory:\n%s", len(lines), got)
	}
	for _, m := range mems {
		if !strings.Contains(got, m.Name+".md") {
			t.Errorf("index does not name %s.md:\n%s", m.Name, got)
		}
	}
	// A line the reader cannot judge is a line they have to open the file to
	// use, which defeats an index.
	if !strings.Contains(got, "Rancher Desktop, not Docker Desktop") {
		t.Errorf("index carries no description:\n%s", got)
	}
}

func TestFormatIndexIsDeterministic(t *testing.T) {
	mems := []Memory{sample("b", "bee", "x"), sample("a", "ay", "y")}
	first := FormatIndex(mems)
	for i := 0; i < 5; i++ {
		if FormatIndex(mems) != first {
			t.Fatal("same memories produced different bytes")
		}
	}
	if strings.Index(first, "a.md") > strings.Index(first, "b.md") {
		t.Errorf("index is not ordered by name:\n%s", first)
	}
}

func TestFormatIndexEmpty(t *testing.T) {
	if got := FormatIndex(nil); got != "" {
		t.Errorf("an empty store produced %q, want nothing at all", got)
	}
}

func TestIndexMarksImported(t *testing.T) {
	m := sample("from-claude", "a fact nabu imported", "x")
	m.Imported = true
	got := FormatIndex([]Memory{m})
	if !strings.Contains(strings.ToLower(got), "imported") {
		t.Errorf("an imported memory is not marked:\n%s", got)
	}
}

func TestOverCapIsVisibleNotSilent(t *testing.T) {
	var mems []Memory
	for i := 0; i < maxIndexLines+10; i++ {
		mems = append(mems, sample("memory-"+string(rune('a'+i%26))+strings.Repeat("x", i%7+1),
			"a description", "a body"))
	}
	content, notice := BuildIndex(mems)

	// Consolidation is the next plan. Until it exists a save must still land:
	// an over-long index costs context, a dropped memory costs the thing
	// memory is for.
	if n := len(strings.Split(strings.TrimSpace(content), "\n")); n != len(mems) {
		t.Errorf("index has %d lines for %d memories, want every one written", n, len(mems))
	}
	if notice == "" {
		t.Fatal("over the cap and no notice: the debt is invisible")
	}
	if !strings.Contains(notice, "consolidat") {
		t.Errorf("notice = %q, want it to say what to do", notice)
	}
	if !strings.Contains(notice, "210") {
		t.Errorf("notice = %q, want it to name the count", notice)
	}
}

func TestUnderCapHasNoNotice(t *testing.T) {
	_, notice := BuildIndex([]Memory{sample("a", "a fact", "x")})
	if notice != "" {
		t.Errorf("notice = %q, want none under the cap", notice)
	}
}

func TestWriteIndexReplacesAndRemovesForgottenLines(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, ScopeGlobal, false)
	for _, n := range []string{"keeper", "doomed"} {
		if err := s.Save(Memory{Name: n, Type: "user", Description: n + " fact", Body: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := WriteIndex(s); err != nil {
		t.Fatalf("WriteIndex: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, IndexFile))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(raw), "doomed.md") {
		t.Fatalf("index missing a memory:\n%s", raw)
	}

	if err := s.Forget("doomed"); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteIndex(s); err != nil {
		t.Fatalf("WriteIndex: %v", err)
	}
	raw, err = os.ReadFile(filepath.Join(dir, IndexFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "doomed.md") {
		t.Errorf("a forgotten memory survives in the index:\n%s", raw)
	}
	if !strings.Contains(string(raw), "keeper.md") {
		t.Errorf("rebuilding removed more than the forgotten line:\n%s", raw)
	}
}

func TestWriteIndexOnEmptyStoreWritesNoFile(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, ScopeGlobal, false)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteIndex(s); err != nil {
		t.Fatalf("WriteIndex: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, IndexFile)); err == nil {
		t.Error("an empty store wrote an index file with nothing in it")
	}
}
