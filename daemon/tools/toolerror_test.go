package tools

import (
	"strings"
	"testing"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// runKind calls a built-in and reports the kind and exit code the daemon would
// record for it, which is what a model ends up branching on.
func runKind(t *testing.T, b *Builtins, s module.Session, name, args string) (string, string, *int) {
	t.Helper()
	out, err := run(t, b, s, name, args)
	kind, code := module.ClassifyToolError(err)
	return out, kind, code
}

// A failing command is not a missing file is not a timeout. The model branches
// on the difference, so each has to arrive as itself.
func TestBashFailuresCarryTheirKind(t *testing.T) {
	if _, _, err := (&Builtins{}).shellCommand(); err != nil {
		t.Skip("no shell on this machine")
	}
	b := &Builtins{BashTimeout: 3 * time.Second}
	s := fakeSession{t.TempDir()}

	t.Run("non-zero exit reports the code", func(t *testing.T) {
		_, kind, code := runKind(t, b, s, "bash", `{"command":"exit 3"}`)
		if kind != protocol.ToolErrorExit {
			t.Fatalf("kind = %q, want %q", kind, protocol.ToolErrorExit)
		}
		if code == nil || *code != 3 {
			t.Fatalf("exit code = %v, want 3", code)
		}
	})

	// Zero is a real exit code, so a pointer distinguishes "exited 0" from
	// "never ran a process". A success has no code at all.
	t.Run("a successful command has no kind and no code", func(t *testing.T) {
		_, kind, code := runKind(t, b, s, "bash", `{"command":"exit 0"}`)
		if kind != "" || code != nil {
			t.Fatalf("kind = %q, code = %v, want empty and nil", kind, code)
		}
	})

	t.Run("a timeout is not an exit", func(t *testing.T) {
		_, kind, code := runKind(t, b, s, "bash", `{"command":"sleep 5","timeout_seconds":1}`)
		if kind != protocol.ToolErrorTimeout {
			t.Fatalf("kind = %q, want %q", kind, protocol.ToolErrorTimeout)
		}
		if code != nil {
			t.Fatalf("a timeout has no exit code, got %v", *code)
		}
	})

	t.Run("a missing command is bad arguments", func(t *testing.T) {
		_, kind, _ := runKind(t, b, s, "bash", `{}`)
		if kind != protocol.ToolErrorInvalidArgs {
			t.Fatalf("kind = %q, want %q", kind, protocol.ToolErrorInvalidArgs)
		}
	})

	t.Run("unparseable arguments are bad arguments", func(t *testing.T) {
		_, kind, _ := runKind(t, b, s, "bash", `{"command":42}`)
		if kind != protocol.ToolErrorInvalidArgs {
			t.Fatalf("kind = %q, want %q", kind, protocol.ToolErrorInvalidArgs)
		}
	})
}

func TestFileFailuresCarryTheirKind(t *testing.T) {
	b := &Builtins{}
	s := fakeSession{t.TempDir()}

	cases := []struct {
		name string
		tool string
		args string
		want string
	}{
		{"reading a file that is not there", "read", `{"path":"nope.txt"}`, protocol.ToolErrorNotFound},
		{"editing a file that is not there", "edit", `{"path":"nope.txt","old":"a","new":"b"}`, protocol.ToolErrorNotFound},
		{"a read with no path", "read", `{}`, protocol.ToolErrorInvalidArgs},
		{"an edit with an empty old", "edit", `{"path":"nope.txt","old":"","new":"b"}`, protocol.ToolErrorInvalidArgs},
		{"a grep with no pattern", "grep", `{}`, protocol.ToolErrorInvalidArgs},
		{"a grep with a broken regexp", "grep", `{"pattern":"("}`, protocol.ToolErrorInvalidArgs},
		{"a glob with no pattern", "glob", `{}`, protocol.ToolErrorInvalidArgs},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, kind, _ := runKind(t, b, s, tc.tool, tc.args)
			if kind != tc.want {
				t.Fatalf("kind = %q, want %q", kind, tc.want)
			}
		})
	}
}

// An unclassified failure must not be given a confident wrong kind: the spec
// reads an absent kind as "unknown", which is the honest answer.
func TestAnUnclassifiedErrorHasNoKind(t *testing.T) {
	kind, code := module.ClassifyToolError(errPlain{})
	if kind != "" {
		t.Fatalf("kind = %q, want empty", kind)
	}
	if code != nil {
		t.Fatalf("exit code = %v, want nil", *code)
	}
}

type errPlain struct{}

func (errPlain) Error() string { return "something went wrong" }

// The kinds a tool reports have to be ones the wire contract admits, or the
// event is rejected when it is appended.
func TestEveryKindToolsUseIsInTheSpec(t *testing.T) {
	used := []string{
		protocol.ToolErrorExit, protocol.ToolErrorTimeout, protocol.ToolErrorDenied,
		protocol.ToolErrorInvalidArgs, protocol.ToolErrorNotFound, protocol.ToolErrorIO,
	}
	for _, k := range used {
		if !protocol.IsToolErrorKind(k) {
			t.Errorf("kind %q is not one the spec defines", k)
		}
	}
	if protocol.IsToolErrorKind("exploded") {
		t.Error("an invented kind should not validate")
	}
	if strings.Join(protocol.ToolErrorKinds, ",") == "" {
		t.Error("ToolErrorKinds should not be empty")
	}
}
