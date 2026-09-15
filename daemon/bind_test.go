package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Binding beyond loopback with no token configured means every remote client
// is refused. Saying so at startup beats a phone that silently cannot connect.
func TestBindingRemotelyWithoutATokenWarns(t *testing.T) {
	var logs strings.Builder
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.json"),
		[]byte(`{"daemon":{"bind":"0.0.0.0:0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := New(Options{Root: root, LogWriter: &logs})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Listen(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	got := logs.String()
	if !strings.Contains(got, "token") {
		t.Errorf("no warning about the missing token:\n%s", got)
	}
	if !strings.Contains(strings.ToLower(got), "refus") {
		t.Errorf("the warning does not say what will happen:\n%s", got)
	}
}

// Loopback needs no token, so it must not nag.
func TestBindingOnLoopbackDoesNotWarn(t *testing.T) {
	var logs strings.Builder
	d, err := New(Options{Root: t.TempDir(), Bind: "127.0.0.1:0", LogWriter: &logs})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Listen(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })

	if strings.Contains(logs.String(), "token") {
		t.Errorf("warned about a token on a loopback bind:\n%s", logs.String())
	}
}
