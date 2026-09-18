package module

import (
	"errors"
	"fmt"

	"github.com/corporealshift/nabu/protocol"
)

// ToolError is a tool failure that says how it failed.
//
// A tool returning a plain error still works: the result is logged with
// status "error" and no kind, which the spec defines as unclassified. This type
// is for the failures a tool can actually name — a timeout is not a missing
// file, and a model that has to tell them apart should not be reading prose to
// do it.
type ToolError struct {
	// Kind is one of the protocol.ToolError* constants.
	Kind string
	// ExitCode is set only where a process ran. A pointer because 0 is a real
	// exit code and "no process" is not 0.
	ExitCode *int
	Err      error
}

func (e *ToolError) Error() string { return e.Err.Error() }

func (e *ToolError) Unwrap() error { return e.Err }

// Fail builds a ToolError of the given kind.
func Fail(kind string, format string, args ...any) *ToolError {
	return &ToolError{Kind: kind, Err: fmt.Errorf(format, args...)}
}

// FailWith wraps an existing error, keeping it unwrappable so callers can still
// test it with errors.Is against things like fs.ErrNotExist.
func FailWith(kind string, err error) *ToolError {
	return &ToolError{Kind: kind, Err: err}
}

// Exited is the non-zero exit of a process the tool ran.
func Exited(code int, format string, args ...any) *ToolError {
	return &ToolError{Kind: protocol.ToolErrorExit, ExitCode: &code, Err: fmt.Errorf(format, args...)}
}

// ClassifyToolError reports how err failed, for the tool_result event.
//
// An error nothing classified yields an empty kind, which the spec reads as
// unclassified rather than as success. Guessing a kind here would put a
// confident wrong answer in an append-only log.
func ClassifyToolError(err error) (kind string, exitCode *int) {
	var te *ToolError
	if errors.As(err, &te) {
		return te.Kind, te.ExitCode
	}
	return "", nil
}
