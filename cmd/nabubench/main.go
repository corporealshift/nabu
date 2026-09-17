// Command nabubench compares how well nabu, pi and Claude Code complete the
// same tasks.
//
// It is run by hand and never in CI. Its exit code says whether the suite ran,
// not whether any harness did well: there is no score to fail.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/corporealshift/nabu/bench"
)

func main() { os.Exit(run()) }

func run() int {
	var (
		tasksDir   = flag.String("tasks", "bench/tasks", "directory of task fixtures")
		resultsDir = flag.String("results", "bench/results", "where to write the result file")
		only       = flag.String("only", "nabu,pi", "comma-separated harnesses to run")
		withClaude = flag.Bool("claude", false, "also run Claude, and let it judge craft (uses your Claude quota)")
		task       = flag.String("task", "", "run one task by id")
		repeat     = flag.Int("repeat", 3, "attempts per task per harness")
		compare    = flag.String("compare", "", "an earlier result file to measure movement against")
		noJudge    = flag.Bool("no-judge", false, "skip craft scoring")
		judgeModel = flag.String("judge-model", "sonnet", "model that scores craft")
		claudeMdl  = flag.String("claude-model", "sonnet", "model the claude harness runs on")
		nabuExe    = flag.String("nabu", "nabu", "the nabu binary to measure")
		configFrom = flag.String("config", "", "config.json to take the provider block from (default: ~/.nabu/config.json)")
	)
	flag.Parse()

	// --compare on its own reads two files and prints; it runs nothing.
	if *compare != "" && flag.NArg() > 0 {
		return compareOnly(*compare, flag.Arg(0))
	}

	tasks, err := bench.LoadTasks(*tasksDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabubench: %v\n", err)
		return 1
	}
	if *task != "" {
		tasks = filterTasks(tasks, *task)
	}
	if len(tasks) == 0 {
		fmt.Fprintln(os.Stderr, "nabubench: no tasks to run")
		return 1
	}

	providers, defaultModel, err := providerBlock(*configFrom)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabubench: %v\n", err)
		return 1
	}

	root, err := os.MkdirTemp("", "nabubench-root-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabubench: %v\n", err)
		return 1
	}
	defer os.RemoveAll(root)

	nabu := &bench.Nabu{Exe: *nabuExe, Root: root, Model: defaultModel}
	if err := nabu.WriteConfig(providers, defaultModel); err != nil {
		fmt.Fprintf(os.Stderr, "nabubench: writing the isolated config: %v\n", err)
		return 1
	}
	// The suite's daemon must not outlive it. A leftover one holds its port, and
	// the next suite then waits for a daemon that can never start — which is
	// exactly how this tool first reported nabu as scoring zero.
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := nabu.Shutdown(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "nabubench: the benchmark daemon may still be running: %v\n", err)
		}
	}()

	// Claude is opt-in. It is the reference, but it spends a quota that a
	// routine comparison of the two local harnesses has no need of.
	wanted := *only
	if *withClaude && !strings.Contains(wanted, "claude") {
		wanted += ",claude"
	}
	harnesses := pick(wanted, []bench.Harness{
		nabu,
		&bench.Pi{},
		&bench.Claude{Model: *claudeMdl},
	})
	if len(harnesses) == 0 {
		fmt.Fprintln(os.Stderr, "nabubench: no harnesses selected")
		return 1
	}

	// The judge is Claude too, so it follows the same opt-in rather than
	// quietly spending a quota on a run that asked for neither.
	var judge bench.Judge = bench.NoJudge{}
	if *withClaude && !*noJudge {
		judge = &bench.ClaudeJudge{Model: *judgeModel}
	}

	// Ctrl-C stops after the run in flight rather than losing the hours
	// already spent.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	runner := bench.NewRunner(bench.Options{
		Tasks:     tasks,
		Harnesses: harnesses,
		Judge:     judge,
		Repeat:    *repeat,
		Reference: "claude",
		Models:    bench.Models{Local: defaultModel, Claude: *claudeMdl},
		Progress:  os.Stderr,
	})

	results, runErr := runner.Run(ctx)
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "\nnabubench: stopped: %v\n", runErr)
	}

	// The report is printed whatever happened: a partial suite is still data.
	bench.Report(os.Stdout, results)

	path, err := results.Save(*resultsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabubench: saving results: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "\nsaved %s\n", path)

	if *compare != "" {
		earlier, err := bench.LoadResults(*compare)
		if err != nil {
			fmt.Fprintf(os.Stderr, "nabubench: %v\n", err)
			return 1
		}
		bench.Compare(os.Stdout, earlier, results)
	}
	return 0
}

// compareOnly reports movement between two saved runs without running anything.
func compareOnly(oldPath, newPath string) int {
	old, err := bench.LoadResults(oldPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabubench: %v\n", err)
		return 1
	}
	now, err := bench.LoadResults(newPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nabubench: %v\n", err)
		return 1
	}
	bench.Report(os.Stdout, now)
	bench.Compare(os.Stdout, old, now)
	return 0
}

func filterTasks(tasks []bench.Task, id string) []bench.Task {
	for _, t := range tasks {
		if t.ID == id {
			return []bench.Task{t}
		}
	}
	return nil
}

func pick(only string, all []bench.Harness) []bench.Harness {
	if strings.TrimSpace(only) == "" {
		return all
	}
	want := map[string]bool{}
	for _, n := range strings.Split(only, ",") {
		want[strings.ToLower(strings.TrimSpace(n))] = true
	}

	var out []bench.Harness
	for _, h := range all {
		if want[h.Name()] {
			out = append(out, h)
		}
	}
	return out
}

// providerBlock borrows the owner's provider configuration so the benchmark
// talks to the same model server, without reading anything else from it.
func providerBlock(path string) (json.RawMessage, string, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, "", err
		}
		path = filepath.Join(home, ".nabu", "config.json")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("reading %s: %w", path, err)
	}
	var cfg struct {
		Daemon struct {
			DefaultModel string `json:"default_model"`
		} `json:"daemon"`
		Providers json.RawMessage `json:"providers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, "", fmt.Errorf("parsing %s: %w", path, err)
	}
	if len(cfg.Providers) == 0 {
		return nil, "", fmt.Errorf("%s has no providers block", path)
	}
	return cfg.Providers, cfg.Daemon.DefaultModel, nil
}
