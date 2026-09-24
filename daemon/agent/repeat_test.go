package agent

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func testdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// feed streams text into a watch in small chunks, as a provider does, and
// returns how far it got before tripping, or -1.
func feed(w *repeatWatch, text string) int {
	for i := 0; i < len(text); i += 20 {
		if w.add(text[i:min(i+20, len(text))]) {
			return min(i+20, len(text))
		}
	}
	return -1
}

// 01M39RT5, event 429 and 430: one paragraph, 88 times in the thinking and
// 265 times in the reply, until the reasoning budget and then the token cap
// cut them off. Both must stop within their first few kilobytes.
func TestARepeatingReplyTrips(t *testing.T) {
	for _, name := range []string{"degenerate-thinking.txt", "degenerate-reply.txt"} {
		t.Run(name, func(t *testing.T) {
			text := testdata(t, name)
			w := newRepeatWatch(8)
			at := feed(w, text)
			if at < 0 || at > 4096 {
				t.Fatalf("tripped at %d of %d bytes, want within 4096", at, len(text))
			}
			// The loop shares its ending with the paragraph before it, so the
			// copy kept starts partway in; its sentence is there once.
			kept := w.kept()
			if n := strings.Count(kept, "I need to reset main back"); n != 1 || len(kept) > 2*len(w.passage()) {
				t.Errorf("kept holds the passage %d times in %d bytes, want once:\n%s", n, len(kept), kept)
			}
			if w.repeats() < 8 {
				t.Errorf("repeats = %d, want at least 8", w.repeats())
			}
			if !strings.Contains(w.passage(), "I need to reset main back") {
				t.Errorf("passage = %q", w.passage())
			}
		})
	}
}

func TestOrdinaryOutputDoesNotTrip(t *testing.T) {
	line := strings.Repeat("x", 41) + "\n"
	var testLog, imports, table strings.Builder
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&testLog, "=== RUN   TestCase%02d\n--- PASS: TestCase%02d (0.00s)\n", i, i)
	}
	testLog.WriteString("PASS\nok  \tgithub.com/corporealshift/nabu/daemon/agent\t0.412s\n")
	for _, p := range []string{"context", "encoding/json", "fmt", "os", "path/filepath", "sort", "strings", "sync", "testing", "time", "errors", "io"} {
		fmt.Fprintf(&imports, "\t%q\n", p)
	}
	table.WriteString("| step | file | proves it |\n|---|---|---|\n")
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&table, "| %d | daemon/agent/runner.go | go test ./daemon/agent/ -run TestStep%d |\n", i, i)
	}
	var long strings.Builder
	for i := 0; long.Len() < 20000; i++ {
		fmt.Fprintf(&long, "Sentence %d says something new about the work, with its own words. ", i)
	}
	for _, tc := range []struct {
		name, text string
	}{
		{"a span under the minimum, many times", strings.Repeat(strings.Repeat("y", 38)+"\n", 20)},
		{"a long line seven times", strings.Repeat(line, 7)},
		{"a rule of one character", strings.Repeat("-", 2000)},
		{"a passing test log", testLog.String()},
		{"import lines", imports.String()},
		{"a table of distinct rows", table.String()},
		{"a long reply that never repeats", long.String()},
		{"earlier replies in the same session", testdata(t, "ordinary-replies.txt")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if at := feed(newRepeatWatch(8), tc.text); at >= 0 {
				t.Fatalf("tripped at %d of %d bytes", at, len(tc.text))
			}
		})
	}
	// Degenerate output does not stop by itself: a check every few hundred
	// bytes meets it still going.
	if at := feed(newRepeatWatch(8), strings.Repeat(line, 20)); at < 0 || at > 8*len(line)+256 {
		t.Fatalf("a 42-byte line repeating should trip soon after its eighth copy; tripped at %d", at)
	}
}

// A limit below one turns the check off.
func TestARepeatWatchCanBeOff(t *testing.T) {
	w := newRepeatWatch(-1)
	if at := feed(w, testdata(t, "degenerate-reply.txt")); at >= 0 {
		t.Fatalf("an off watch tripped at %d", at)
	}
}
