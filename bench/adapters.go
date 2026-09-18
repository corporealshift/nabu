package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// --------------------------------------------------------------------- nabu

// Nabu drives the nabu CLI. It is given an isolated root so a benchmark run
// never touches the owner's real sessions, and a config with the web module
// off so results do not move with the internet.
type Nabu struct {
	Exe   string // default "nabu"
	Root  string // isolated --root
	Model string
	// Port is chosen when the config is written. A fixed one collides with a
	// daemon left over from an earlier suite, and every run then waits for a
	// daemon that can never start.
	Port int
}

func (n *Nabu) Name() string { return "nabu" }

func (n *Nabu) Preflight(context.Context) error {
	if n.Root == "" {
		return fmt.Errorf("nabu: no isolated root configured")
	}
	return nil
}

func (n *Nabu) Run(ctx context.Context, ws *Workspace, task Task) (Attempt, error) {
	exe := n.Exe
	if exe == "" {
		exe = "nabu"
	}
	argv := []string{exe, "run", "--root", n.Root, "--workspace", ws.Dir, "--json"}
	if task.MaxTurns > 0 {
		argv = append(argv, "--max-turns", strconv.Itoa(task.MaxTurns))
	}
	argv = append(argv, task.Prompt)

	a, err := run(ctx, ws.Dir, task.Timeout(), argv, nil)
	if err != nil {
		return a, err
	}
	// A daemon that never came up is not a harness that tried and failed. Left
	// unclassified it scores zero and reads as the harness being bad at the job.
	if reason := nabuDidNotStart(a.Output); reason != "" {
		return a, fmt.Errorf("nabu: %s", reason)
	}
	a.Cost.Turns, a.Cost.InputTokens, a.Cost.OutputTokens = nabuUsage(a.Output)
	return a, nil
}

// nabuDidNotStart reports why the run never reached the model, or "".
func nabuDidNotStart(out string) string {
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.Contains(line, "none listening under"):
			return line
		case strings.Contains(line, "address already in use"):
			return line
		}
	}
	return ""
}

// Shutdown stops the daemon this harness started. Without it the port stays
// held and the next suite collides with it, which is exactly how this adapter
// first reported nabu as scoring zero.
func (n *Nabu) Shutdown(ctx context.Context) error {
	exe := n.Exe
	if exe == "" {
		exe = "nabu"
	}
	cmd := exec.CommandContext(ctx, exe, "daemon", "stop", "--root", n.Root)
	return cmd.Run()
}

// nabuUsage reads the turn count and token totals out of nabu's --json stream.
// Assistant messages carry usage; counting them is also the turn count.
func nabuUsage(out string) (turns, in, outTok int) {
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var ev struct {
			Type string `json:"type"`
			Data struct {
				Role  string `json:"role"`
				Usage *struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev.Type == "message" && ev.Data.Role == "assistant" {
			turns++
			if ev.Data.Usage != nil {
				in += ev.Data.Usage.InputTokens
				outTok += ev.Data.Usage.OutputTokens
			}
		}
	}
	return turns, in, outTok
}

// WriteConfig writes the isolated config nabu runs under: the owner's provider
// block, the web module off, memory off so one run cannot teach the next.
func (n *Nabu) WriteConfig(providers json.RawMessage, defaultModel string) error {
	if err := os.MkdirAll(n.Root, 0o755); err != nil {
		return err
	}
	if n.Port == 0 {
		port, err := freePort()
		if err != nil {
			return err
		}
		n.Port = port
	}
	cfg := map[string]any{
		"daemon": map[string]any{
			"bind":          fmt.Sprintf("127.0.0.1:%d", n.Port),
			"log_level":     "warn",
			"default_model": defaultModel,
		},
		"providers": providers,
		"modules": map[string]any{
			"memory": map[string]any{"enabled": false},
			"web":    map[string]any{"enabled": false},
		},
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(n.Root, "config.json"), raw, 0o644)
}

// freePort asks the operating system for an unused port by taking one and
// giving it straight back.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// ----------------------------------------------------------------------- pi

// Pi drives the pi CLI.
type Pi struct {
	Exe      string // default: the pi.cmd shim
	Provider string
	Model    string
}

func (p *Pi) Name() string { return "pi" }

func (p *Pi) exe() string {
	if p.Exe != "" {
		return p.Exe
	}
	// The .ps1 shim is blocked by execution policy on this machine; .cmd is not.
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "AppData", "Roaming", "npm", "pi.cmd")
	}
	return "pi.cmd"
}

// Preflight refuses to start while a pi from an earlier run is still alive.
// pi outlives the command that launched it, and a leftover agent edits the
// next task's workspace.
func (p *Pi) Preflight(ctx context.Context) error {
	pids, err := piProcesses(ctx)
	if err != nil {
		return err
	}
	if len(pids) > 0 {
		return fmt.Errorf("pi: %d pi process(es) still running from an earlier run: %v", len(pids), pids)
	}
	return nil
}

func (p *Pi) Run(ctx context.Context, ws *Workspace, task Task) (Attempt, error) {
	argv := []string{p.exe(), "-p", "--mode", "json"}
	if p.Provider != "" {
		argv = append(argv, "--provider", p.Provider)
	}
	if p.Model != "" {
		argv = append(argv, "--model", p.Model)
	}
	argv = append(argv, task.Prompt)

	a, err := run(ctx, ws.Dir, task.Timeout(), argv, nil)
	if err != nil {
		return a, err
	}
	a.Cost.Turns, a.Cost.InputTokens, a.Cost.OutputTokens = piUsage(a.Output)

	// pi's exit code is not evidence: its connection drops mid-run routinely.
	// What the workspace holds is the evidence, and verification reads that.
	a.ExitCode = 0
	return a, nil
}

// piUsage reads whatever pi's JSON reports. Its shape has not been pinned, so
// several plausible spellings are accepted and anything absent stays zero.
func piUsage(out string) (turns, in, outTok int) {
	line := lastJSONObject(out)
	if line == "" {
		return 0, 0, 0
	}
	var wire struct {
		Turns int `json:"turns"`
		Steps int `json:"steps"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			Prompt       int `json:"prompt_tokens"`
			Completion   int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal([]byte(line), &wire) != nil {
		return 0, 0, 0
	}
	turns = wire.Turns
	if turns == 0 {
		turns = wire.Steps
	}
	in = wire.Usage.InputTokens
	if in == 0 {
		in = wire.Usage.Prompt
	}
	outTok = wire.Usage.OutputTokens
	if outTok == 0 {
		outTok = wire.Usage.Completion
	}
	return turns, in, outTok
}

// piProcesses lists live pi agents by command line. Never by process name: the
// binary is node.exe, and killing those would take the owner's own work with it.
func piProcesses(ctx context.Context) ([]string, error) {
	const script = `Get-CimInstance Win32_Process -Filter "Name='node.exe'" | ` +
		`Where-Object { $_.CommandLine -like '*pi-coding-agent*' } | ` +
		`Select-Object -ExpandProperty ProcessId`

	cmd := exec.CommandContext(ctx, "powershell.exe", "-NonInteractive", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		// No powershell, or nothing matched: not a reason to stop the suite.
		return nil, nil
	}

	var pids []string
	for _, line := range strings.Fields(string(out)) {
		if line = strings.TrimSpace(line); line != "" {
			pids = append(pids, line)
		}
	}
	return pids, nil
}

// ------------------------------------------------------------------- claude

// Claude drives the Claude Code CLI. Its model is pinned rather than left to
// the CLI default: it is the reference ceiling, and a ceiling that moves makes
// every earlier comparison meaningless.
type Claude struct {
	Exe   string // default "claude"
	Model string
}

func (c *Claude) Name() string { return "claude" }

func (c *Claude) Preflight(context.Context) error {
	if c.Model == "" {
		return fmt.Errorf("claude: no model pinned")
	}
	return nil
}

func (c *Claude) Run(ctx context.Context, ws *Workspace, task Task) (Attempt, error) {
	exe := c.Exe
	if exe == "" {
		exe = "claude"
	}
	argv := []string{
		exe, "-p", task.Prompt,
		"--output-format", "json",
		"--permission-mode", "bypassPermissions",
		"--model", c.Model,
	}

	a, err := run(ctx, ws.Dir, task.Timeout(), argv, nil)
	if err != nil {
		return a, err
	}
	// A refusal comes back in under a second and looks exactly like a harness
	// that tried and produced nothing. Scoring it as a failure is how a rate
	// limit came to read as Claude being unable to do the task.
	if reason := claudeRefused(a.Output); reason != "" {
		return a, fmt.Errorf("claude: %s", reason)
	}
	a.Cost = claudeCost(a.Output, a.Cost.Duration)
	return a, nil
}

// claudeRefused reports why the CLI declined to run, or "".
func claudeRefused(out string) string {
	line := lastJSONObject(out)
	if line == "" {
		return "no result was printed: " + firstLine(strings.TrimSpace(out))
	}
	var wire struct {
		IsError bool   `json:"is_error"`
		Subtype string `json:"subtype"`
		Result  string `json:"result"`
		Error   string `json:"error"`
	}
	if json.Unmarshal([]byte(line), &wire) != nil {
		return "unreadable result"
	}
	if wire.IsError {
		reason := wire.Error
		if reason == "" {
			reason = wire.Result
		}
		if reason == "" {
			reason = wire.Subtype
		}
		return firstLine(reason)
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	if len(s) > 160 {
		return s[:160]
	}
	return s
}

func claudeCost(out string, fallback time.Duration) Cost {
	cost := Cost{Duration: fallback}
	line := lastJSONObject(out)
	if line == "" {
		return cost
	}
	var wire struct {
		NumTurns   int     `json:"num_turns"`
		DurationMS int64   `json:"duration_ms"`
		CostUSD    float64 `json:"total_cost_usd"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal([]byte(line), &wire) != nil {
		return cost
	}
	cost.Turns = wire.NumTurns
	cost.USD = wire.CostUSD
	cost.InputTokens = wire.Usage.InputTokens
	cost.OutputTokens = wire.Usage.OutputTokens
	if wire.DurationMS > 0 {
		cost.Duration = time.Duration(wire.DurationMS) * time.Millisecond
	}
	return cost
}
