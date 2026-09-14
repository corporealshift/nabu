package guard

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// Tier is how dangerous a tool call is.
type Tier int

const (
	// TierLow is read-only: nothing observable changes.
	TierLow Tier = iota
	// TierMedium writes inside the workspace, or installs something.
	TierMedium
	// TierHigh destroys, escalates privilege, reaches the network, or leaves
	// the workspace.
	TierHigh
)

func (t Tier) String() string {
	switch t {
	case TierMedium:
		return "medium"
	case TierHigh:
		return "high"
	default:
		return "low"
	}
}

// parseTier maps a config string to a Tier, reporting whether it was valid.
func parseTier(s string) (Tier, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low":
		return TierLow, true
	case "medium":
		return TierMedium, true
	case "high":
		return TierHigh, true
	}
	return TierLow, false
}

// Match is what a rule matches on. Every populated field must match; an empty
// Match matches every call.
type Match struct {
	Tool           string
	CommandPattern *regexp.Regexp
	Path           string
	// MinRisk matches calls at this tier or above. Nil means any tier.
	MinRisk *Tier
}

// Rule is one gate decision and what it applies to.
type Rule struct {
	Decision module.Decision
	Reason   string
	Match    Match
}

// Module is the tool gate. It is always compiled in, so its behaviour with no
// configuration at all is what most sessions get.
type Module struct {
	rules []Rule
}

// Name implements module.Module.
func (m *Module) Name() string { return "guard" }

// Init implements module.Module. With no rules configured it installs the
// built-in defaults; a configured list replaces them.
func (m *Module) Init(_ module.Host, cfg module.Config) error {
	rules, err := rulesFromConfig(cfg)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		rules = DefaultRules()
	}
	m.rules = rules
	return nil
}

// DefaultRules is the policy with no configuration: stop and ask before
// anything destructive or anything outside the workspace, and let ordinary
// work inside it through.
//
// These ask rather than deny on purpose. A refusal the human never sees makes
// legitimate work impossible without editing config; a prompt puts the
// decision where it belongs. Deny is reserved for rules someone configured
// deliberately.
func DefaultRules() []Rule {
	high := TierHigh
	return []Rule{
		{
			Decision: module.Ask,
			Reason:   "destructive, escalates privilege, or reaches the network",
			Match:    Match{Tool: "bash", MinRisk: &high},
		},
		{
			Decision: module.Ask,
			Reason:   "outside the workspace",
			Match:    Match{Path: outsideWorkspace},
		},
	}
}

// outsideWorkspace is a sentinel Path value meaning "anything not under the
// session's workspace", which a literal prefix cannot express.
const outsideWorkspace = "\x00outside-workspace"

// GateTool implements module.ToolGate.
func (m *Module) GateTool(_ context.Context, s module.Session, call protocol.ToolCallData) module.Verdict {
	mode := protocol.PermissionAsk
	if s != nil {
		if got := s.State().Options.PermissionMode; got != "" {
			mode = got
		}
	}

	// Spec §8: bypass "approves everything". It short-circuits before any
	// rule, built-in or configured. Core appends a notice when the mode is
	// set, and that notice — not a rule exception — is the safeguard.
	if mode == protocol.PermissionBypass {
		return module.Verdict{Decision: module.Allow}
	}

	info := inspect(s, call)

	for _, r := range m.rules {
		if !r.matches(info) {
			continue
		}
		switch r.Decision {
		case module.Deny:
			return module.Verdict{Decision: module.Deny, Reason: r.denyReason(info)}
		case module.Ask:
			summary := info.summary()
			if r.Reason != "" {
				summary = r.Reason + " — " + summary
			}
			return module.Verdict{
				Decision: module.Ask,
				Summary:  summary,
				Risk:     info.tier.String(),
			}
		case module.Allow:
			return module.Verdict{Decision: module.Allow}
		}
	}

	return m.defaultVerdict(mode, info)
}

// defaultVerdict is what happens when no rule matches.
//
// Spec 8: auto "lets the guard module approve edits inside the workspace and
// low-risk commands without asking". So under auto the only thing that still
// stops is high risk — a destructive command, or a path outside the workspace.
// Everything else is ordinary work and goes through silently.
func (m *Module) defaultVerdict(mode protocol.PermissionMode, info callInfo) module.Verdict {
	ask := module.Verdict{Decision: module.Ask, Summary: info.summary(), Risk: info.tier.String()}
	if mode != protocol.PermissionAuto {
		return ask
	}
	if info.tier == TierHigh {
		return ask
	}
	return module.Verdict{Decision: module.Allow}
}

// denyReason prefers the rule's own reason and falls back to something the
// model can act on, since Reason is what it sees as the tool result.
func (r Rule) denyReason(info callInfo) string {
	if r.Reason != "" {
		return r.Reason
	}
	return "denied by guard: " + info.summary()
}

// callInfo is everything a rule can match on, extracted once per call.
type callInfo struct {
	tool       string
	command    string
	path       string
	replaceAll bool
	tier       Tier
	// inWorkspace is false when the call names a path outside the workspace.
	// A call naming no path at all is treated as inside.
	inWorkspace bool
}

// summary is the one-line description a human sees in a permission prompt.
func (c callInfo) summary() string {
	switch {
	case c.command != "":
		return c.tool + ": " + truncate(c.command, 120)
	case c.path != "":
		return c.tool + ": " + c.path
	default:
		return c.tool
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// toolArgs is the union of the built-in tools' argument shapes. Fields absent
// from a given tool simply stay empty.
type toolArgs struct {
	Command    string `json:"command"`
	Path       string `json:"path"`
	ReplaceAll bool   `json:"replace_all"`
}

// inspect extracts what the rules match on. Malformed or absent arguments are
// not an error: the call is classified by tool name alone.
func inspect(s module.Session, call protocol.ToolCallData) callInfo {
	info := callInfo{tool: call.Tool, inWorkspace: true}

	var args toolArgs
	if len(call.Arguments) > 0 {
		_ = json.Unmarshal(call.Arguments, &args)
	}
	info.command = args.Command
	info.path = args.Path
	info.replaceAll = args.ReplaceAll

	if info.path != "" && s != nil {
		info.inWorkspace = withinWorkspace(s.Workspace().Path, info.path)
	}
	info.tier = classify(info)
	return info
}

// withinWorkspace reports whether path resolves inside root. A relative path
// is resolved against the workspace; a rooted one is compared as given. Both
// sides are cleaned and normalised to forward slashes, so a Windows path
// written with either separator compares correctly.
func withinWorkspace(root, path string) bool {
	if root == "" {
		return true
	}
	if !rooted(path) {
		// Join cleans as it goes, so "../.." still escapes and is caught below.
		path = filepath.Join(root, path)
	}
	r := normalizePath(root)
	p := normalizePath(path)
	if strings.EqualFold(p, r) {
		return true
	}
	return strings.HasPrefix(strings.ToLower(p), strings.ToLower(r)+"/")
}

// rooted reports whether p names a location from a filesystem root rather than
// relative to somewhere. On Windows a leading separator is rooted even though
// filepath.IsAbs also wants a drive letter — without this, a model asking for
// "/etc/passwd" would be read as a path inside the workspace.
func rooted(p string) bool {
	if filepath.IsAbs(p) {
		return true
	}
	return strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`)
}

// normalizePath cleans a path and puts it in one comparable form.
func normalizePath(p string) string {
	return strings.TrimSuffix(filepath.ToSlash(filepath.Clean(p)), "/")
}

// Command families, matched against the first word of a bash command.
var (
	// destructive, privilege-escalating, or network-reaching.
	highRiskCommands = words(`rm del erase rmdir rd shred mkfs dd format
		sudo su doas runas chown chmod icacls takeown
		ssh scp rsync nc ncat netcat nmap telnet ftp
		systemctl service launchctl reg regedit
		apt apt-get yum dnf pacman apk brew choco winget`)

	// writes, installs, or downloads.
	mediumRiskCommands = words(`sed awk tee mkdir touch cp copy mv move ln mklink
		git npm yarn pnpm pip pip3 go cargo gem bundle make cmake ninja
		curl wget docker podman kubectl helm terraform`)

	// read-only.
	lowRiskCommands = words(`cat type dir ls ll tree head tail wc sort uniq cut tr
		grep rg find fd echo printf pwd whoami date env true false which where
		diff stat file less more`)
)

func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(s) {
		out[w] = true
	}
	return out
}

// toolTiers is the baseline risk of each built-in tool before its arguments
// are considered. An unknown tool is read-only until proven otherwise.
var toolTiers = map[string]Tier{
	"read": TierLow, "glob": TierLow, "grep": TierLow, "task.update": TierLow,
	"write": TierMedium, "edit": TierMedium,
}

// classify assigns a risk tier. A path outside the workspace is high risk
// whatever the tool, because leaving the workspace is the escape that matters.
func classify(info callInfo) Tier {
	if !info.inWorkspace {
		return TierHigh
	}
	if info.tool == "bash" {
		return classifyCommand(info.command)
	}
	// A sweeping edit is riskier than a targeted one.
	if info.tool == "edit" && info.replaceAll {
		return TierHigh
	}
	if t, ok := toolTiers[info.tool]; ok {
		return t
	}
	return TierLow
}

// classifyCommand rates a shell command by the riskiest word in it, so a
// pipeline or a chain is judged by its worst part rather than its first.
func classifyCommand(command string) Tier {
	if strings.TrimSpace(command) == "" {
		return TierLow
	}
	worst := TierLow
	for _, w := range commandWords(command) {
		switch {
		case highRiskCommands[w]:
			return TierHigh
		case mediumRiskCommands[w]:
			if worst < TierMedium {
				worst = TierMedium
			}
		case lowRiskCommands[w]:
			// leaves worst where it is
		}
	}
	return worst
}

// commandWords splits a command into the words that could name a program:
// the first word, and the first word after each separator.
func commandWords(command string) []string {
	seps := func(r rune) bool {
		return r == '|' || r == ';' || r == '&' || r == '\n' || r == '(' || r == ')'
	}
	var out []string
	for _, segment := range strings.FieldsFunc(command, seps) {
		fields := strings.Fields(segment)
		if len(fields) == 0 {
			continue
		}
		w := strings.ToLower(fields[0])
		// Strip a path so /usr/bin/rm still reads as rm.
		if i := strings.LastIndexAny(w, `/\`); i >= 0 {
			w = w[i+1:]
		}
		out = append(out, strings.TrimSuffix(w, ".exe"))
	}
	return out
}

// matches reports whether every populated criterion of the rule holds.
func (r Rule) matches(info callInfo) bool {
	m := r.Match
	if m.Tool != "" && !strings.EqualFold(m.Tool, info.tool) {
		return false
	}
	if m.CommandPattern != nil && !m.CommandPattern.MatchString(info.command) {
		return false
	}
	switch {
	case m.Path == outsideWorkspace:
		if info.path == "" || info.inWorkspace {
			return false
		}
	case m.Path != "":
		if info.path == "" || !strings.HasPrefix(
			strings.ToLower(filepath.ToSlash(info.path)),
			strings.ToLower(filepath.ToSlash(m.Path))) {
			return false
		}
	}
	if m.MinRisk != nil && info.tier < *m.MinRisk {
		return false
	}
	return true
}

// rulesFromConfig reads the "rules" list. An absent or empty list yields no
// rules, which Init turns into the defaults.
func rulesFromConfig(cfg module.Config) ([]Rule, error) {
	raw, ok := cfg["rules"]
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("guard: rules must be a list, got %T", raw)
	}

	var out []Rule
	for i, entry := range list {
		obj, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("guard: rule %d must be an object, got %T", i, entry)
		}
		r, err := ruleFromMap(obj)
		if err != nil {
			return nil, fmt.Errorf("guard: rule %d: %w", i, err)
		}
		out = append(out, r)
	}
	return out, nil
}

func ruleFromMap(obj map[string]any) (Rule, error) {
	var r Rule

	verdict, _ := obj["verdict"].(string)
	switch strings.ToLower(strings.TrimSpace(verdict)) {
	case "allow":
		r.Decision = module.Allow
	case "deny":
		r.Decision = module.Deny
	case "ask":
		r.Decision = module.Ask
	case "":
		return r, fmt.Errorf("verdict is required")
	default:
		return r, fmt.Errorf("unknown verdict %q (allow|deny|ask)", verdict)
	}
	r.Reason, _ = obj["reason"].(string)

	match, ok := obj["match"].(map[string]any)
	if !ok {
		return r, nil // no match section: a catch-all
	}
	r.Match.Tool, _ = match["tool"].(string)
	r.Match.Path, _ = match["path"].(string)

	if pattern, _ := match["command_pattern"].(string); pattern != "" {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return r, fmt.Errorf("command_pattern %q: %w", pattern, err)
		}
		r.Match.CommandPattern = re
	}
	if risk, _ := match["risk"].(string); risk != "" {
		tier, ok := parseTier(risk)
		if !ok {
			return r, fmt.Errorf("unknown risk %q (low|medium|high)", risk)
		}
		r.Match.MinRisk = &tier
	}
	return r, nil
}
