package verify

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
)

// run runs git in a test repository, failing the test on error.
func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// writeAt writes a file, creating its directories.
func writeAt(t *testing.T, ws, rel string) {
	t.Helper()
	p := filepath.Join(ws, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildPrefix(t *testing.T) {
	for _, tc := range []struct {
		path, want string
	}{
		{"android/app/build/intermediates/x.bin", "android/app/build/"},
		{"android/.gradle/8.11.1/checksums/checksums.lock", "android/.gradle/"},
		{"android/app/build/generated/source/buildConfig/release/", "android/app/build/"},
		{"web/node_modules/left-pad/index.js", "web/node_modules/"},
		{"target/", "target/"},
		{"src/main/Build.kt", ""},
		{"scripts/build", ""}, // a file named build is not a build directory
		{"docs/README.md", ""},
	} {
		if got := buildPrefix(tc.path); got != tc.want {
			t.Errorf("buildPrefix(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// The veto used to end "commit them or say why they should stay uncommitted".
// Saying why cleared nothing, and the model said why fourteen times.
func TestTheTreeVetoOffersOnlyWhatClearsIt(t *testing.T) {
	ws := gitRepo(t)
	m := newVerify(t, module.Config{})
	s := &fakeSession{workspace: ws}
	m.SessionStart(context.Background(), s)

	writeAt(t, ws, "android/app/build/intermediates/x.bin")
	writeAt(t, ws, "android/.gradle/file-system.probe")
	writeAt(t, ws, "src/Main.kt")
	writeAt(t, ws, "README.md") // tracked, now changed

	v := m.BeforeStop(context.Background(), s, module.StopInfo{})
	if v.Allow {
		t.Fatal("a tree the session dirtied must veto")
	}
	for _, want := range []string{
		"untracked, and not ignored: src/Main.kt\n", "changed: README.md",
		"build output, not ignored: 2 files under android/.gradle/, android/app/build/",
		"repository root", "git rm -r --cached",
		"explaining them does not clear it",
	} {
		if !strings.Contains(v.Reason, want) {
			t.Errorf("reason is missing %q:\n%s", want, v.Reason)
		}
	}
	if strings.Contains(v.Reason, "say why") {
		t.Errorf("reason still offers an escape that does not exist:\n%s", v.Reason)
	}
}

func TestTheTreeVetoSaysNothingAboutBuildOutputWhenThereIsNone(t *testing.T) {
	ws := gitRepo(t)
	m := newVerify(t, module.Config{})
	s := &fakeSession{workspace: ws}
	m.SessionStart(context.Background(), s)
	writeAt(t, ws, "src/Main.kt")

	v := m.BeforeStop(context.Background(), s, module.StopInfo{})
	if v.Allow || strings.Contains(v.Reason, "build output") {
		t.Errorf("want a veto without build advice, got allow=%v:\n%s", v.Allow, v.Reason)
	}
}

func TestListSomeCountsWhatItLeavesOut(t *testing.T) {
	items := make([]string, maxListed+5)
	for i := range items {
		items[i] = "f"
	}
	if got := listSome(items, maxListed); !strings.HasSuffix(got, "and 5 more") {
		t.Errorf("listSome = %q", got)
	}
}

// `git add android/` in one session committed 866 Gradle files. The stop gate
// now refuses that, and clears once they are untracked again.
func TestCommittingBuildOutputVetoesUntilItIsUntracked(t *testing.T) {
	ws := gitRepo(t)
	m := newVerify(t, module.Config{})
	s := &fakeSession{workspace: ws}
	m.SessionStart(context.Background(), s)

	writeAt(t, ws, "android/app/src/Main.kt")
	writeAt(t, ws, "android/app/build/intermediates/x.bin")
	writeAt(t, ws, "android/.gradle/checksums.lock")
	run(t, ws, "add", "android")
	run(t, ws, "commit", "-m", "phase 1")

	v := m.BeforeStop(context.Background(), s, module.StopInfo{})
	if v.Allow {
		t.Fatal("committed build output must veto")
	}
	for _, want := range []string{"committed 2 files of build output", "android/.gradle/", "android/app/build/", "git rm -r --cached"} {
		if !strings.Contains(v.Reason, want) {
			t.Errorf("reason is missing %q:\n%s", want, v.Reason)
		}
	}

	// The fix: untrack, ignore at the root, commit.
	run(t, ws, "rm", "-r", "-q", "--cached", "android/app/build", "android/.gradle")
	if err := os.WriteFile(filepath.Join(ws, ".gitignore"), []byte("build/\n.gradle/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, ws, "add", ".gitignore")
	run(t, ws, "commit", "-m", "untrack build output")

	if v := m.BeforeStop(context.Background(), s, module.StopInfo{}); !v.Allow {
		t.Errorf("still vetoed after the build output was untracked and ignored:\n%s", v.Reason)
	}
}

func TestCommittingSourceDoesNotTripTheBuildCheck(t *testing.T) {
	ws := gitRepo(t)
	m := newVerify(t, module.Config{})
	s := &fakeSession{workspace: ws}
	m.SessionStart(context.Background(), s)

	writeAt(t, ws, "src/build.go") // a file named for building is still source
	run(t, ws, "add", "src")
	run(t, ws, "commit", "-m", "source")

	if v := m.BeforeStop(context.Background(), s, module.StopInfo{}); !v.Allow {
		t.Errorf("committed source was vetoed:\n%s", v.Reason)
	}
}

// Build output the session found already committed is not its doing.
func TestBuildOutputCommittedBeforeTheSessionDoesNotVeto(t *testing.T) {
	ws := gitRepo(t)
	writeAt(t, ws, "app/build/old.bin")
	run(t, ws, "add", "app")
	run(t, ws, "commit", "-m", "someone else's mistake")

	m := newVerify(t, module.Config{})
	s := &fakeSession{workspace: ws}
	m.SessionStart(context.Background(), s)
	writeAt(t, ws, "src/Main.kt")
	run(t, ws, "add", "src")
	run(t, ws, "commit", "-m", "this session's work")

	if v := m.BeforeStop(context.Background(), s, module.StopInfo{}); !v.Allow {
		t.Errorf("vetoed over build output committed before the session:\n%s", v.Reason)
	}
}
