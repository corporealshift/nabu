package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissedEditSaysWhatIsThere(t *testing.T) {
	file := "package x\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n\nfunc Sub(a, b int) int {\n\treturn a - b\n}\n"
	crlf := strings.ReplaceAll(file, "\n", "\r\n")
	cases := []struct {
		name, text, old, new string
		want                 []string
	}{
		{"already applied", file, "return a * b", "return a + b",
			[]string{"already there at line 4", "already applied"}},
		{"indentation differs", file, "func Add(a, b int) int {\n    return a + b\n}", "zzz",
			[]string{"lines 3-5", "only in whitespace", "3\tfunc Add(a, b int) int {", "4\t\treturn a + b"}},
		{"line endings differ", crlf, "func Sub(a, b int) int {\n\treturn a - b\n}", "zzz",
			[]string{"lines 7-9", "only in whitespace", "7\tfunc Sub(a, b int) int {"}},
		{"close but not equal", file, "func Sub(a, b int64) int64 {\n\treturn a - b\n}", "zzz",
			[]string{"nearest match, lines 7-9", "8\t\treturn a - b"}},
		{"nothing close", file, "impl Display for Error {\n    fn fmt(&self) {}\n}", "zzz",
			[]string{"no similar lines found"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := missedEdit(c.text, c.old, c.new)
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Fatalf("missing %q in:\n%s", w, got)
				}
			}
			if c.name == "close but not equal" && strings.Contains(got, "whitespace") {
				t.Fatalf("a real difference is not whitespace:\n%s", got)
			}
		})
	}
}

func TestWriteAndEditSayWhenNothingChanged(t *testing.T) {
	dir := t.TempDir()
	s := fakeSession{dir}
	b := &Builtins{}
	if out, _ := run(t, b, s, "write", `{"path":"f.rs","content":"fn main() {}\n"}`); !strings.HasPrefix(out, "wrote 13 bytes") {
		t.Fatalf("first write: %q", out)
	}
	stat := func() os.FileInfo {
		fi, err := os.Stat(filepath.Join(dir, "f.rs"))
		if err != nil {
			t.Fatal(err)
		}
		return fi
	}
	before := stat()
	out, err := run(t, b, s, "write", `{"path":"f.rs","content":"fn main() {}\n"}`)
	if err != nil || out != "unchanged: f.rs already has exactly this content (13 bytes)" {
		t.Fatalf("identical write: %q %v", out, err)
	}
	if !stat().ModTime().Equal(before.ModTime()) {
		t.Fatal("an identical write must not touch the file")
	}
	if out, _ := run(t, b, s, "write", `{"path":"f.rs","content":"fn main() { }\n"}`); !strings.HasPrefix(out, "wrote") {
		t.Fatalf("a different write must still write: %q", out)
	}
	out, err = run(t, b, s, "edit", `{"path":"f.rs","old":"main","new":"main"}`)
	if err != nil || !strings.HasPrefix(out, "unchanged: old and new are identical") {
		t.Fatalf("identical edit: %q %v", out, err)
	}
	_, err = run(t, b, s, "edit", `{"path":"f.rs","old":"fn start() { }","new":"fn main() { }"}`)
	if err == nil || !strings.Contains(err.Error(), "already applied") {
		t.Fatalf("an edit that already landed must say so: %v", err)
	}
}
