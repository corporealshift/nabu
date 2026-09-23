package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// Bounds on a wait, so one call cannot hold a session for hours.
const (
	waitDefaultInterval = 10 * time.Second
	waitDefaultTimeout  = 5 * time.Minute
	waitMaxTimeout      = 30 * time.Minute
)

// waitTool polls a command until its output changes, matches a pattern, or
// time runs out, and returns once.
//
// Without it, waiting on CI or a server is the model calling the same command
// turn after turn, which costs a turn of context per look and is hard to tell
// apart from a loop. With it, real waiting has somewhere to go.
func (b *Builtins) waitTool() module.Tool {
	type args struct {
		Command         string `json:"command"`
		Until           string `json:"until"`
		IntervalSeconds int    `json:"interval_seconds"`
		TimeoutSeconds  int    `json:"timeout_seconds"`
	}
	return module.Tool{
		Name: "wait",
		Description: "Wait for something to change instead of running the same command again and again. " +
			"Runs command in the workspace every interval_seconds (default 10) and returns once: when its output " +
			"differs from the first run, or, if until is given, when the output matches that regular expression. " +
			"Gives up after timeout_seconds (default 300, at most 1800). Use until when the output contains a " +
			"clock or elapsed time, which would otherwise count as a change.",
		Schema: schema(`{"type":"object","required":["command"],"properties":{
			"command":{"type":"string"},"until":{"type":"string"},
			"interval_seconds":{"type":"integer"},"timeout_seconds":{"type":"integer"}}}`),
		Run: func(ctx context.Context, s module.Session, raw json.RawMessage) (string, error) {
			a, err := decode[args](raw)
			if err != nil {
				return "", err
			}
			if a.Command == "" {
				return "", module.Fail(protocol.ToolErrorInvalidArgs, "command is required")
			}
			var until *regexp.Regexp
			if a.Until != "" {
				if until, err = regexp.Compile(a.Until); err != nil {
					return "", module.Fail(protocol.ToolErrorInvalidArgs, "bad until pattern: %s", err)
				}
			}
			interval := waitDefaultInterval
			if a.IntervalSeconds > 0 {
				interval = time.Duration(a.IntervalSeconds) * time.Second
			}
			timeout := waitDefaultTimeout
			if a.TimeoutSeconds > 0 {
				timeout = min(time.Duration(a.TimeoutSeconds)*time.Second, waitMaxTimeout)
			}
			return b.poll(ctx, s, a.Command, until, interval, timeout)
		},
	}
}

// poll runs command until it reports something new. Only a shell that cannot
// start is an error: a command that fails is output like any other, and a
// change in its exit status counts as a change.
func (b *Builtins) poll(ctx context.Context, s module.Session, command string, until *regexp.Regexp, interval, timeout time.Duration) (string, error) {
	runTimeout := b.BashTimeout
	if runTimeout == 0 {
		runTimeout = 120 * time.Second
	}
	once := func() (string, error) {
		out, err := b.runShell(ctx, s, command, runTimeout)
		// An interrupt kills the command, and its truncated output must not
		// read as a change.
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err != nil {
			// No shell, or one that could not start: nothing to wait on.
			if kind, _ := module.ClassifyToolError(err); kind == protocol.ToolErrorNotFound || kind == protocol.ToolErrorIO {
				return "", err
			}
		}
		return out, nil
	}
	start := time.Now()
	first, err := once()
	if err != nil {
		return "", err
	}
	if until != nil && until.MatchString(first) {
		return "[matched on the first check]\n" + first, nil
	}
	last, checks := first, 1
	for {
		left := timeout - time.Since(start)
		if left <= 0 {
			break
		}
		t := time.NewTimer(min(interval, left))
		select {
		case <-ctx.Done():
			t.Stop()
			return "", ctx.Err()
		case <-t.C:
		}
		if last, err = once(); err != nil {
			return "", err
		}
		checks++
		took := time.Since(start).Round(time.Second)
		switch {
		case until != nil && until.MatchString(last):
			return fmt.Sprintf("[matched after %s, %d checks]\n%s", took, checks, last), nil
		case until == nil && last != first:
			return fmt.Sprintf("[changed after %s, %d checks]\n%s", took, checks, last), nil
		}
	}
	took := time.Since(start).Round(time.Second)
	if until != nil {
		return fmt.Sprintf("[no match for %q after %s, %d checks; latest output:]\n%s", until.String(), took, checks, last), nil
	}
	return fmt.Sprintf("[no change after %s, %d checks]\n%s", took, checks, last), nil
}
