package tools

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestWaitReturnsOnceSomethingHappens(t *testing.T) {
	requireShell(t)
	cases := []struct {
		name    string
		until   string
		later   string // written to status.txt after 150ms
		timeout time.Duration
		want    []string
	}{
		{"output changes", "", "done\n", 3 * time.Second, []string{"[changed after", "done"}},
		{"output matches", "^done", "done\n", 3 * time.Second, []string{"[matched after", "done"}},
		{"a change that does not match", "^done", "still going 2\n", 600 * time.Millisecond,
			[]string{`[no match for "^done" after`, "still going 2"}},
		{"nothing changes", "", "", 400 * time.Millisecond, []string{"[no change after", "running"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			status := filepath.Join(dir, "status.txt")
			os.WriteFile(status, []byte("running\n"), 0o644)
			if c.later != "" {
				time.AfterFunc(150*time.Millisecond, func() { os.WriteFile(status, []byte(c.later), 0o644) })
			}
			var until *regexp.Regexp
			if c.until != "" {
				until = regexp.MustCompile(c.until)
			}
			b := &Builtins{}
			out, err := b.poll(context.Background(), fakeSession{dir}, "cat status.txt", until, 50*time.Millisecond, c.timeout)
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Fatalf("missing %q in %q", w, out)
				}
			}
		})
	}
}

func TestWaitMatchingAtOnceDoesNotPoll(t *testing.T) {
	requireShell(t)
	b := &Builtins{}
	start := time.Now()
	out, err := b.poll(context.Background(), fakeSession{t.TempDir()}, "echo ready", regexp.MustCompile("ready"), time.Hour, time.Hour)
	if err != nil || !strings.HasPrefix(out, "[matched on the first check]") || time.Since(start) > 10*time.Second {
		t.Fatalf("%q %v", out, err)
	}
}

func TestWaitStopsWhenTheSessionIsInterrupted(t *testing.T) {
	requireShell(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	b := &Builtins{}
	if _, err := b.poll(ctx, fakeSession{t.TempDir()}, "echo same", nil, 20*time.Millisecond, time.Hour); err == nil {
		t.Fatal("an interrupt must end the wait")
	}
}

func TestWaitRejectsABadPattern(t *testing.T) {
	if _, err := run(t, &Builtins{}, fakeSession{t.TempDir()}, "wait", `{"command":"true","until":"("}`); err == nil ||
		!strings.Contains(err.Error(), "bad until pattern") {
		t.Fatalf("%v", err)
	}
}
