package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// Builtins is the module that provides nabu's built-in tools. It registers
// through module.ToolProvider like any other module: there is no privileged
// path (spec §14.5).
type Builtins struct {
	// Tasks records task.update snapshots. nil disables the task tool.
	Tasks TaskStore
	// Shell runs bash commands; "" auto-detects (bash, sh, then cmd).
	Shell string
	// BashTimeout bounds a command unless the call overrides it. 0 = 120s.
	BashTimeout time.Duration
	// MaxOutput caps tool output bytes. 0 = 32 KiB.
	MaxOutput int
	// Roots are other repositories this daemon may read, by name. Reads only:
	// write, edit and bash never consult it.
	Roots Roots
}

// Name implements module.Module.
func (b *Builtins) Name() string { return "builtins" }

// Init implements module.Module; reads shell, bash_timeout_seconds, max_output.
func (b *Builtins) Init(_ module.Host, cfg module.Config) error {
	if b.Shell == "" {
		b.Shell = cfg.String("shell", "")
	}
	if b.BashTimeout == 0 {
		b.BashTimeout = time.Duration(cfg.Int("bash_timeout_seconds", 120)) * time.Second
	}
	if b.MaxOutput == 0 {
		b.MaxOutput = cfg.Int("max_output", 32<<10)
	}
	if b.Roots == nil {
		b.Roots = parseRoots(cfg)
	}
	return nil
}

// Tools implements module.ToolProvider.
func (b *Builtins) Tools() []module.Tool {
	tools := []module.Tool{b.readTool(), b.writeTool(), b.editTool(), b.globTool(), b.grepTool(), b.bashTool()}
	if b.Tasks != nil {
		tools = append(tools, b.taskTool())
	}
	return tools
}

func (b *Builtins) maxOutput() int {
	if b.MaxOutput > 0 {
		return b.MaxOutput
	}
	return 32 << 10
}

// decode unmarshals tool arguments leniently (unknown fields ignored).
//
// Every built-in funnels its arguments through here, so classifying the failure
// once covers all of them.
func decode[T any](args json.RawMessage) (T, error) {
	var v T
	if len(args) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(args, &v); err != nil {
		return v, module.Fail(protocol.ToolErrorInvalidArgs, "invalid arguments: %s", err)
	}
	return v, nil
}

// osFail classifies a filesystem error. A path that is not there is a different
// problem from one the OS refused, and the model can act on the difference:
// create the file, or stop trying.
func osFail(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return module.FailWith(protocol.ToolErrorNotFound, err)
	}
	return module.FailWith(protocol.ToolErrorIO, err)
}

// resolve turns a tool path into an absolute path under the workspace unless
// it is already absolute.
func resolve(s module.Session, p string) (string, error) {
	if p == "" {
		return "", module.Fail(protocol.ToolErrorInvalidArgs, "path is required")
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	return filepath.Join(s.Workspace().Path, p), nil
}

// rel renders a path relative to the workspace with forward slashes.
func rel(s module.Session, abs string) string {
	return relTo(s.Workspace().Path, abs)
}

// relTo names a path relative to the root it was found under. A hit in another
// workspace reported relative to this one would come back as a pile of "..",
// which is unreadable and, pasted into a tool call, wrong.
func relTo(root, abs string) string {
	if r, err := filepath.Rel(root, abs); err == nil {
		return filepath.ToSlash(r)
	}
	return filepath.ToSlash(abs)
}

// truncate keeps the head and tail of oversized output with a marker.
func truncate(out string, max int) string {
	if len(out) <= max {
		return out
	}
	head := max * 3 / 4
	tail := max - head
	return out[:head] + fmt.Sprintf("\n[... %d bytes truncated ...]\n", len(out)-max) + out[len(out)-tail:]
}

func schema(s string) json.RawMessage { return json.RawMessage(s) }

// bashTool is in bash.go; taskTool and TaskStore are in tasks.go.
