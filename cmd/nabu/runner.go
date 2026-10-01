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
	"github.com/corporealshift/nabu/clients/runs"
)

// cmdRunner runs orchestrated runs, and the GitHub watcher's jobs alongside
// them when <root>/github/config.json exists, sharing one set of job slots
// (docs/specs/2026-09-30-orchestrated-runs-design.md).
func cmdRunner(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("runner", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := rootFlag(fs)
	once := fs.Bool("once", false, "keep going until every run has finished and nothing new starts, then exit")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	dir, err := resolveRoot(*root)
	if err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	cfg, err := runs.LoadConfig(dir)
	if err != nil {
		fmt.Fprintf(stderr, "nabu: %v\n", err)
		return exitError
	}
	log := stampedWriter{stderr}
	rn := &runs.Runner{
		Cfg:    cfg,
		Root:   dir,
		Git:    runs.GitCLI{Run: github.ExecRunner},
		GH:     runs.GHCLI{Run: github.ExecRunner},
		Claude: runs.ClaudeCLI{Path: cfg.Claude.Path, Model: cfg.Claude.Model, Timeout: time.Duration(cfg.Claude.Timeout)},
		Shell:  runs.Bash{},
		Now:    time.Now,
		Log:    log,
	}

	// The GitHub watcher joins in when it is configured. Its slots and poll
	// are the runner's, so one process has one budget.
	var w *github.Watcher
	if _, err := os.Stat(github.ConfigPath(dir)); err == nil {
		gcfg, err := github.Load(dir)
		if err != nil {
			fmt.Fprintf(stderr, "nabu: %v\n", err)
			return exitError
		}
		gcfg.MaxJobs, gcfg.Poll = cfg.MaxJobs, cfg.Poll
		w = &github.Watcher{Cfg: gcfg, Root: dir, GH: github.GH{Run: github.ExecRunner}, Git: github.GitCLI{Run: github.ExecRunner},
			Now: time.Now, Log: log, Out: stdout}
	}

	tick := func(ctx context.Context) (int, error) {
		c, err := connect(ctx, dir, log)
		if err != nil {
			return 0, fmt.Errorf("reaching the daemon: %w", err)
		}
		defer c.Close()
		rd := runs.Client{C: c}
		advErr := rn.Advance(ctx, rd)
		var started int
		var pollErr error
		if w != nil {
			w.Dial = func(context.Context) (github.Daemon, error) { return shared{github.Client{C: c}}, nil }
			w.Runs = runsSlots{rn, rd}
			started, pollErr = w.Poll(ctx)
		} else if free := cfg.MaxJobs - rn.Busy(); free > 0 {
			started, pollErr = rn.Start(ctx, rd, free)
		}
		return started, errors.Join(advErr, pollErr)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	wait := time.Duration(cfg.Poll)
	if *once {
		wait = 10 * time.Second
	} else {
		fmt.Fprintf(log, "runner: polling every %s, %d job slots, github watcher %v\n", wait, cfg.MaxJobs, w != nil)
	}
	for {
		started, err := tick(ctx)
		if err != nil {
			fmt.Fprintf(log, "runner: %v\n", err)
		}
		if *once && started == 0 && rn.Active() == 0 && (w == nil || w.Running() == 0) {
			return exitOK
		}
		select {
		case <-ctx.Done():
			return exitOK
		case <-time.After(wait):
		}
	}
}

// shared is the tick's connection as the watcher's Daemon. The tick closes
// it, not the watcher.
type shared struct{ github.Client }

func (shared) Close() {}

// runsSlots is the runner as the watcher's Runs.
type runsSlots struct {
	rn *runs.Runner
	d  runs.Daemon
}

func (s runsSlots) Busy() int { return s.rn.Busy() }

func (s runsSlots) Start(ctx context.Context, n int) (int, error) { return s.rn.Start(ctx, s.d, n) }
