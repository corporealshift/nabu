package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/corporealshift/nabu/clients/github"
)

// cmdGitHub runs the GitHub watcher: it reviews open pull requests in the
// repositories listed in <root>/github/config.json
// (docs/specs/2026-09-28-github-review-design.md).
func cmdGitHub(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("github", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := rootFlag(fs)
	once := fs.Bool("once", false, "poll once, finish what that starts, and exit")
	dryRun := fs.Bool("dry-run", false, "run sessions but print what would be posted instead of posting it")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	dir, err := resolveRoot(*root)
	if err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	cfg, err := github.Load(dir)
	if err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}

	log := stampedWriter{stderr}
	w := &github.Watcher{
		Cfg:  cfg,
		Root: dir,
		GH:   github.GH{Run: github.ExecRunner},
		Git:  github.GitCLI{Run: github.ExecRunner},
		Dial: func(ctx context.Context) (github.Daemon, error) {
			c, err := connect(ctx, dir, log)
			if err != nil {
				return nil, err
			}
			return github.Client{C: c}, nil
		},
		Now:    time.Now,
		DryRun: *dryRun,
		Log:    log,
		Out:    stdout,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if *once {
		if err := w.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			return exitError
		}
		return exitOK
	}
	fmt.Fprintf(log, "github: watching %d repositories every %s\n", len(cfg.Repos), time.Duration(cfg.Poll))
	_ = w.Run(ctx)
	return exitOK
}

// stampedWriter puts the time in front of each line the watcher logs, since
// it runs for days and its log is read after the fact.
type stampedWriter struct{ w io.Writer }

func (s stampedWriter) Write(p []byte) (int, error) {
	if _, err := fmt.Fprintf(s.w, "%s %s", time.Now().Format("2006-01-02 15:04:05"), p); err != nil {
		return 0, err
	}
	return len(p), nil
}
