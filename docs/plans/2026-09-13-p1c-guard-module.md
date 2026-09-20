# nabu P1c — `guard` Module Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> This plan specifies contracts and behaviours, not code. Write the failing test first
> from the stated behaviour, then the implementation. If a requirement is ambiguous or
> contradicts the real code, STOP and report rather than guessing.

**Goal:** Give nabu a tool gate, so an agent is guarded from its first run instead of approving everything by default.

**Architecture:** `guard` is an in-process module implementing the `ToolGate` hook. The agent already asks the module registry to gate every tool call, so this slots into an existing seam and changes nothing in core. It decides allow, deny or ask from the session's permission mode plus rules over command patterns, paths and risk tiers.

**Tech Stack:** Go 1.26, standard library only. No new dependencies.

---

## Decisions settled by this plan

The spec leaves several policy details open. This plan decides them:

### Risk tiers

| Tier | What qualifies | Examples |
|---|---|---|
| **low** | Read-only operations: `read`, `glob`, `grep`, `task.update`; bash commands that only read or list files (cat, type, dir, ls, get-content, echo, printf); bash commands with no file modification (true, false, date, whoami, pwd, env) | cat file.txt, grep pattern ., glob **/*.go, task.update |
| **medium** | Write operations: `write`, `edit` tools; bash commands that create or modify files within the workspace (sed, awk, tee, tr, cut, sort, uniq, head, tail, wc, mkdir, touch, cp, mv); bash commands that install or download packages (pip install, go get, npm install, cargo install, brew install, wget, curl with output redirection) | write path content, edit path old new, sed -i ..., pip install requests |
| **high** | Destructive operations: rm, del, Remove-Item, rm -rf, shred, mkfs, dd; bash commands with network side-effects or privilege escalation (curl/wget without output redirection, sudo, su, ssh, scp, rsync, nc, nmap, ping); commands that modify system state (apt, yum, pacman, chown, chmod, systemctl); any bash command executed outside the workspace; edit with replace_all: true | rm -rf /tmp, curl https://... | bash, sudo reboot |

### Workspace path matching

A path is "inside the workspace" when the resolved absolute path starts with the workspace path. On Windows, paths may use backslashes or forward slashes; matching is done by normalising both to forward slashes with `filepath.ToSlash` before comparing. A path is inside the workspace if the normalised resolved path equals the normalised workspace path or starts with the normalised workspace path followed by a forward slash.

### Default behaviour when no rule matches

The default verdict depends on the session's `permission_mode`:
- `ask` → **Ask** (the guard's default: be cautious)
- `auto` → **Allow** for low-risk, **Ask** for medium-risk, **Deny** for high-risk
- `bypass` → **Allow** (everything is approved, per spec section 8)

### Rule evaluation order

**`bypass` is checked before anything else and short-circuits to Allow.** Spec section 8
is normative: bypass "approves everything and appends a `notice` when set". No rule,
built-in or configured, overrides it. The notice — appended by core when the mode is
set — is the safeguard, not a rule exception. A session in bypass mode is deliberately
unguarded and the log records that it is.

For every other mode, rules are evaluated in the order they appear, built-in defaults
first and configured rules after. The **first matching rule** determines the verdict. A
rule matches if **all** of its match criteria are satisfied (AND logic). An empty match
section matches everything. If no rule matches, the permission-mode default applies.

### Rule configuration format

Rules live in the module's config section under `modules.guard`. Each rule has:

- `verdict`: one of "allow", "deny", "ask"
- `match`: an object with optional fields. If `match` is absent or empty, the rule matches every tool call.
  - `tool`: the tool name string (e.g. "bash", "write")
  - `command_pattern`: a Go regular expression matched against the bash command string (only relevant for the bash tool)
  - `path`: a prefix path to match against the file path (for read, write, edit tools)
  - `risk`: one of "low", "medium", "high" — matches calls at this risk tier or above

A rule with an empty match object (or no match key) is a catch-all that matches every tool call.

### Default rules (no configuration)

When the guard config is absent or empty, the following built-in rules apply (in order):

1. **Deny** any bash command matching a high-risk pattern (destructive commands, privilege escalation, network execution).
2. **Deny** any tool call where the resolved file path is outside the workspace — prevents writing to arbitrary locations.
3. **Ask** for any edit with replace_all: true — warns about potential mass replacement.
4. No match — falls through to the permission-mode default above.

These defaults ensure the agent cannot destroy the system or escape the workspace, while still asking for every write and medium-risk operation.

**These rules do not apply in `bypass` mode**, which short-circuits to Allow before any
rule is consulted. That is what spec section 8 means by "approves everything", and it is
why setting bypass appends a notice: the log has to show that a session ran unguarded.

### What guard gates

The following tools are gated:
- bash — the command string, timeout, and working directory
- read — the file path
- write — the file path and content size
- edit — the file path, old/new text, and replace_all flag
- glob — the search pattern and path
- grep — the search pattern and path
- task.update — the task operations

---

## File structure

| Path | Responsibility |
|---|---|
| `daemon/modules/guard/guard.go` | **New.** The guard module: implements `Module` and `ToolGate`. Reads config, builds the rule engine, evaluates tool calls. |
| `daemon/modules/guard/guard_test.go` | **New.** Table-driven tests for every risk tier, permission mode, path matching, rule evaluation order, default rules, and error cases. |
| `daemon/modules/all.go` | **Modify.** Add guard to the All registration list. |

---

## Task 1: Module skeleton and registration

**Files:**
- Create: `daemon/modules/guard/guard.go` (skeleton only — Module interface, no ToolGate yet)
- Create: `daemon/modules/guard/guard_test.go` (config reading tests only)

**Goal:** The guard module initialises from config, reads its rules, and is registered in all.go. No gating logic yet — just the module structure.

- [ ] **Step 1: Write the failing test for module name and empty init**

Append to `daemon/modules/guard/guard_test.go` a test that creates a zero-value guard module and verifies:
- `Name()` returns the string "guard"
- `Init(nil, nil)` returns nil (no error with empty config)

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./daemon/modules/guard/ -run TestName -v`
Expected: compile error `undefined: Module`.

- [ ] **Step 3: Create the module skeleton**

Create `daemon/modules/guard/guard.go` with:
- A `Module` struct (zero fields for now)
- A `Name()` method returning "guard"
- An `Init(_ module.Host, cfg module.Config) error` method that sets default rules and returns nil

The `Module` struct holds a slice of compiled rules internally.

- [ ] **Step 4: Run the tests — they should pass**

Run: `go test ./daemon/modules/guard/ -run TestName -v`
Expected: both tests pass.

- [ ] **Step 5: Register guard in all.go**

Modify `daemon/modules/all.go` to import the guard package and add a zero-value `guard.Module{}` to the `All` registration list. The import must be blank-identified so the init-time registration happens.

- [ ] **Step 6: Run the boundary test**

Run: `go test ./daemon/module/ -run TestModulesImportOnlyTheContract -v`
Expected: pass (guard only imports `daemon/module` and `protocol`).

- [ ] **Step 7: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

---

## Task 2: Rule engine and risk tier classification

**Files:**
- Modify: `daemon/modules/guard/guard.go` (add rule struct, rule engine, risk classification)

**Goal:** The guard module can classify tool calls into risk tiers and evaluate rules against them. This is the core logic.

- [ ] **Step 1: Write the failing test for risk classification**

Append to `daemon/modules/guard/guard_test.go` table-driven tests for:

**Low-risk bash commands:** cat file.txt, ls -la, pwd, whoami, echo hello, date, true, false, env — each must return tierLow.

**Medium-risk bash commands:** sed -i ... file.txt, awk ... file.txt, tee /dev/null, sort file.txt > out.txt, mkdir -p newdir, touch newfile, cp src.txt dst.txt, mv src.txt dst.txt, head -n 10 file.txt, tail -n 10 file.txt, pip install requests, go get github.com/user/repo, npm install lodash, wget https://example.com/file, curl -o file https://example.com/file — each must return tierMedium.

**High-risk bash commands:** rm file.txt, rm -rf /tmp, del file.txt, Remove-Item file.txt, shred file.txt, mkfs.ext4 /dev/sda, dd if=/dev/zero of=/dev/sda, sudo reboot, sudo rm -rf /, su -, ssh user@host, scp file user@host:/tmp, rsync -avz . user@host:/backup, nc -l 1234, nmap 192.168.1.1, ping 8.8.8.8, curl https://example.com/script | bash, apt install vim, yum install vim, pacman -S vim, chown root file.txt, chmod 777 file.txt, systemctl restart nginx — each must return tierHigh.

**Tool call classification:** read, glob, grep, task.update → tierLow; write, edit → tierMedium; bash → delegate to classifyBashCommand.

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./daemon/modules/guard/ -run TestClassifyBashLowRisk -v`
Expected: compile error `undefined: classifyBashCommand` and `undefined: tierLow`.

- [ ] **Step 3: Implement risk classification**

Add to `daemon/modules/guard/guard.go`:

An unexported `tier` type with three constants: tierLow, tierMedium, tierHigh.

A `classifyBashCommand(command string) tier` function that:
1. Extracts the base command (first word of the command string)
2. Checks for high-risk patterns first (destructive commands, privilege escalation, network execution)
3. Then checks for medium-risk patterns (file modification, package installation, download commands)
4. Defaults to low risk for everything else

A `classifyToolCall(tool string, args json.RawMessage) tier` function that:
1. Returns tierLow for read, glob, grep, task.update
2. Returns tierMedium for write, edit
3. Returns tierHigh for bash with a high-risk command (delegates to classifyBashCommand)
4. Returns tierLow for unknown tool names (treat as read-only by default)

- [ ] **Step 4: Run the classification tests**

Run: `go test ./daemon/modules/guard/ -run TestClassify -v`
Expected: all classification tests pass.

- [ ] **Step 5: Write the failing test for workspace path matching**

Append to `daemon/modules/guard/guard_test.go` table-driven tests for `isInsideWorkspace(workspace, path)`:

- "C:/Users/kyle/proj" + "C:/Users/kyle/proj/file.txt" → true
- "C:/Users/kyle/proj" + "C:/Users/kyle/proj" → true
- "C:/Users/kyle/proj" + "C:/Users/kyle/other/file.txt" → false
- "C:/Users/kyle/proj" + "C:/Users/kyle/proj2/file.txt" → false (proj2 is not inside proj)
- "/home/user/proj" + "/home/user/proj/file.txt" → true
- "/home/user/proj" + "/home/user/other/file.txt" → false
- "C:/Users/kyle/proj" + "C:\Users\kyle\proj\file.txt" → true (backslash normalisation)
- "C:/Users/kyle/proj" + "C:\Users\kyle\other\file.txt" → false

- [ ] **Step 6: Implement workspace path matching**

The `isInsideWorkspace(workspace, path string) bool` function:
1. Normalises both paths using `filepath.ToSlash`
2. Checks if the resolved path equals the workspace path or starts with workspace + "/"
3. Returns true if inside, false otherwise

- [ ] **Step 7: Write the failing test for rule evaluation order**

Append to `daemon/modules/guard/guard_test.go` tests for:

**First match wins:** A deny rule for bash matching "rm" followed by an allow-everything rule. "rm file" → Deny (first match wins). "cat file" → Allow (first match doesn't match, second does).

**Tool filter:** A rule with match.tool = "write" matches only write calls, not bash or read.

**Command pattern:** A rule with match.command_pattern = "rm -rf" matches the command "rm -rf /tmp" but not "cat file".

**Path prefix:** A rule with match.path = "/etc" matches a write to "/etc/passwd" but not a write to "/home/user/file".

- [ ] **Step 8: Implement rule evaluation**

Add to `daemon/modules/guard/guard.go`:

An unexported `rule` struct with:
- `verdict` (verdictAllow, verdictDeny, or verdictAsk)
- `match` (ruleMatch struct with optional tool, commandPattern, pathPrefix, minRisk fields)

An unexported `ruleMatch` struct with:
- `tool` string (empty means match any tool)
- `commandPattern *regexp.Regexp` (nil means no command filter)
- `pathPrefix string` (empty means no path filter)
- `minRisk tier` (tierLow means match any risk)

A `rule.matches(call) bool` method that checks:
1. If match.tool is set, the call's tool name must match
2. If match.commandPattern is set, the bash command must match the regex
3. If match.pathPrefix is set, the resolved file path must start with the prefix
4. If match.minRisk is set, the call's risk tier must be >= the threshold

An empty match (all fields zero-value) matches everything.

A `rules.evaluate(call, session) verdict` method that iterates rules in order, returns the first match's verdict, or the permission-mode default if no rule matches.

- [ ] **Step 9: Run all guard tests**

Run: `go test ./daemon/modules/guard/ -v`
Expected: all tests pass.

- [ ] **Step 10: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

---

## Task 3: ToolGate implementation and permission-mode defaults

**Files:**
- Modify: `daemon/modules/guard/guard.go` (add GateTool method, permission-mode logic)

**Goal:** The guard module implements module.ToolGate and returns the correct Verdict based on permission mode, risk tier, and rules.

- [ ] **Step 1: Write the failing test for permission mode defaults**

Append to `daemon/modules/guard/guard_test.go` tests for:

**Ask mode:** An unmatched call (e.g. read tool, no matching rule) should return Ask.

**Auto mode:** Low-risk unmatched call → Allow. Medium-risk unmatched call → Ask. High-risk unmatched call → Deny.

**Bypass mode:** Every call → Allow, even high-risk bash commands.

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./daemon/modules/guard/ -run TestDefaultVerdict -v`
Expected: compile error `undefined: Module.GateTool`.

- [ ] **Step 3: Implement GateTool**

Add to `daemon/modules/guard/guard.go` the method:

`GateTool(ctx context.Context, s module.Session, call protocol.ToolCallData) module.Verdict`

The method:
1. Gets the session's permission mode from s.State().Options.PermissionMode
2. Classifies the call's risk tier via classifyToolCall
3. Extracts the command string from call.Arguments for bash calls (unmarshal into a struct with Command string and TimeoutSeconds int fields, matching the bash tool's args struct)
4. Extracts the path from call.Arguments for write/edit/read/glob/grep calls
5. Checks if the call is inside the workspace
6. Evaluates rules in order — first match wins
7. If no rule matches, applies the permission-mode default
8. Returns the appropriate Verdict with Reason for Deny and Summary/Risk for Ask

For the Verdict fields:
- Reason is required for Deny — set to a human-readable description of why the call was denied
- Summary is for Ask — a one-line description of the action (e.g. "Write to src/main.go")
- Risk is for Ask — the risk tier string ("low", "medium", "high")

- [ ] **Step 4: Write the failing test for deny-short-circuit**

Append tests:

**Deny short-circuits Ask:** A deny rule followed by an ask rule. A matching call should return Deny (first match wins), not Ask.

**Ask aggregation first-Ask-wins:** Multiple ask rules matching the same call. The first Ask verdict is returned.

- [ ] **Step 5: Run it to see it fail**

Run: `go test ./daemon/modules/guard/ -run TestDenyShortCircuit -v`
Expected: fails because GateTool doesn't short-circuit on deny yet.

- [ ] **Step 6: Implement deny-short-circuit and ask aggregation**

The GateTool method must:
1. Return immediately on the first verdictDeny match (per spec section 14.3)
2. If any rule matches with verdictAsk (and no deny before it), return the first Ask
3. If no rule matches, apply the permission-mode default

- [ ] **Step 7: Write the failing test for built-in default rules**

Append tests:

**Default deny high-risk bash:** A high-risk bash command (e.g. rm -rf /) should be denied even in ask mode.

**Default deny outside workspace:** A write to a path outside the workspace (e.g. /etc/passwd when workspace is /home/user/proj) should be denied.

**Default ask write inside workspace:** A write inside the workspace should Ask (not deny).

- [ ] **Step 8: Implement default rules**

The defaultRules() function returns a slice of built-in rules:
1. A deny rule for high-risk bash commands (using a compiled regex of all high-risk patterns)
2. A deny rule for writes outside the workspace (checked against the session's workspace path)
3. An ask rule for edit with replace_all: true
4. A catch-all ask rule

- [ ] **Step 9: Run all guard tests**

Run: `go test ./daemon/modules/guard/ -v`
Expected: all tests pass.

- [ ] **Step 10: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

---

## Task 4: Edge cases, error handling, and config parsing

**Files:**
- Modify: `daemon/modules/guard/guard.go`
- Modify: `daemon/modules/guard/guard_test.go`

**Goal:** Handle all edge cases: empty arguments, unknown tools, malformed JSON, the edit replace_all check, config rule parsing, and invalid regex.

- [ ] **Step 1: Write the failing test for unknown tools**

Append tests:

**Unknown tool in ask mode:** A tool call for an unknown tool name should return Ask.

**Unknown tool in auto mode:** An unknown tool should return Allow (treated as low risk).

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./daemon/modules/guard/ -run TestUnknownTool -v`
Expected: fails because unknown tools are not classified.

- [ ] **Step 3: Handle unknown tools**

The classifyToolCall function should return tierLow for unknown tool names (treat as read-only by default). The GateTool method should handle empty or missing call.Arguments gracefully.

- [ ] **Step 4: Write the failing test for malformed arguments**

Append tests:

**Malformed JSON:** A bash call with malformed JSON arguments should not panic; it should fall back to classifying by tool name only.

**Empty arguments:** A bash call with empty arguments should not panic; it should fall back to classifying by tool name only.

- [ ] **Step 5: Run it to see it fail**

Run: `go test ./daemon/modules/guard/ -run TestMalformedArguments -v`
Expected: may panic or return unexpected verdict.

- [ ] **Step 6: Handle malformed arguments**

When unmarshalling arguments fails, treat the call as having no extractable path or command. The risk classification falls back to the tool's default tier.

- [ ] **Step 7: Write the failing test for config rule parsing**

Append tests:

**Config rules loaded:** A config with a "rules" key containing two rule objects should result in two compiled rules.

**Config empty rules uses defaults:** An empty config should use the default rules (not zero rules).

**Config enabled false:** Init with enabled=false should return nil (the registry won't call GateTool on a dead module).

- [ ] **Step 8: Run it to see it fail**

Run: `go test ./daemon/modules/guard/ -run TestConfigRulesLoaded -v`
Expected: fails because config rules are not parsed yet.

- [ ] **Step 9: Implement config rule parsing**

The Init method:
1. Reads the "rules" key from the config as []map[string]any
2. For each rule map, extracts "verdict" and "match" fields
3. From match, extracts "tool", "command_pattern", "path", and "risk"
4. Compiles command_pattern as a Go regex (returns error if invalid)
5. If the rules list is empty or absent, calls defaultRules()

- [ ] **Step 10: Write the failing test for invalid regex in config**

Append test: A config with an invalid regex pattern (e.g. "[invalid") in command_pattern should cause Init to return an error.

- [ ] **Step 11: Run it to see it fail**

Run: `go test ./daemon/modules/guard/ -run TestConfigInvalidRegex -v`
Expected: fails because invalid regex is not validated.

- [ ] **Step 12: Validate regex during Init**

Return an error from Init if any command_pattern fails to compile. The module becomes dead and the registry appends a notice.

- [ ] **Step 13: Write the failing test for Verdict fields**

Append test: A deny verdict must have a non-empty Reason. An ask verdict must have a non-empty Summary and Risk.

- [ ] **Step 14: Set Verdict fields**

Ensure GateTool always sets the appropriate Verdict fields based on the decision type.

- [ ] **Step 15: Run all guard tests**

Run: `go test ./daemon/modules/guard/ -v`
Expected: all tests pass.

- [ ] **Step 16: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

- [ ] **Step 17: Run gofmt check**

Run: `gofmt -l daemon/modules/guard/`
Expected: no output.

---

## Task 5: Registry integration test

**Files:**
- Modify: `daemon/modules/guard/guard_test.go`

**Goal:** Verify that the guard module integrates correctly with the Registry.GateTool dispatch: deny short-circuits, ask aggregation, and the order of evaluation.

- [ ] **Step 1: Write the failing integration test**

Append to `daemon/modules/guard/guard_test.go` a test that:
1. Creates a guard module with two custom rules: deny bash matching "rm -rf", allow tool "read"
2. Creates a module.Registry with just the guard module
3. Creates a session with ask mode and workspace path "/home/user/proj"
4. Verifies:
   - bash "rm -rf /tmp" → Deny (custom deny rule matches)
   - bash "cat file" → Ask (no deny matches, falls through to ask-mode default)
   - read tool → Allow (custom allow rule matches)

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./daemon/modules/guard/ -run TestGuardIntegratesWithRegistry -v`
Expected: fails because the fakeSession needs to implement the full module.Session interface with a proper State() method.

- [ ] **Step 3: Fix the test**

The fakeSession used in tests must implement the full module.Session interface. The State() method must return a protocol.State with the correct Options.PermissionMode. The Workspace() method must return the correct path.

- [ ] **Step 4: Run the integration test**

Run: `go test ./daemon/modules/guard/ -run TestGuardIntegratesWithRegistry -v`
Expected: pass.

- [ ] **Step 5: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

---

## Summary

**Files created:**
- `daemon/modules/guard/guard.go` — the guard module
- `daemon/modules/guard/guard_test.go` — comprehensive tests

**Files modified:**
- `daemon/modules/all.go` — register guard in the module list

**Tasks:** 5 ordered tasks, each independently verifiable

**Risk-tier decisions:**
- Low: read-only tools (read, glob, grep, task.update) and bash read commands
- Medium: write tools (write, edit) and bash file-modification/package-install commands
- High: destructive commands (rm, shred, mkfs), privilege escalation (sudo, su), network commands (ssh, scp, curl | bash), system commands (apt, systemctl)

**Default-rule decisions:**
1. Deny high-risk bash commands (always, regardless of permission mode)
2. Deny writes outside the workspace
3. Ask for edit with replace_all: true
4. Ask everything else (falls through to permission-mode default)

**Permission-mode default decisions:**
- ask → Ask (cautious default)
- auto → Allow low, Ask medium, Deny high
- bypass → Allow everything

**Symbols confirmed in source:**
- `module.Module` → `daemon/module/module.go` — `Name() string`, `Init(Host, Config) error`
- `module.ToolGate` → `daemon/module/module.go` — `GateTool(ctx, Session, protocol.ToolCallData) Verdict`
- `module.Verdict` → `daemon/module/module.go` — `Decision Decision`, `Reason string`, `Summary string`, `Risk string`
- `module.Decision` → `daemon/module/module.go` — `Allow`, `Deny`, `Ask` constants
- `module.Session` → `daemon/module/module.go` — `ID()`, `Workspace()`, `Events()`, `State()`, `Append()`
- `module.Workspace` → `daemon/module/module.go` — `Path string`, `Key string`
- `module.Config` → `daemon/module/module.go` — `map[string]any` with `Bool`, `String`, `Int`, `Strings`, `Enabled`
- `module.Registry.GateTool` → `daemon/module/registry.go` — first Deny short-circuits, Ask aggregates (first Ask wins)
- `protocol.ToolCallData` → `protocol/types.go` — `CallID`, `Tool`, `Arguments json.RawMessage`, `Source`
- `protocol.PermissionMode` → `protocol/types.go` — `PermissionAsk`, `PermissionAuto`, `PermissionBypass`
- `protocol.State` → `protocol/project.go` — `Options Options` with `PermissionMode PermissionMode`
- `tools.Builtins` → `daemon/tools/builtins.go` — tool registration via `module.ToolProvider`
- `tools.resolve` → `daemon/tools/files.go` — resolves relative paths to workspace-absolute
- `tools.rel` → `daemon/tools/files.go` — renders relative paths with forward slashes
- `tools.decode[T]` → `daemon/tools/builtins.go` — lenient JSON unmarshalling for tool args
- `module.All` → `daemon/modules/all.go` — registration list
- `module.Config.Enabled()` → `daemon/module/module.go` — default true
- `agent.Manager.executeTool` → `daemon/agent/manager.go` — calls `Registry.GateTool`, handles Deny/Ask
- `module.boundary_test.go` → `daemon/module/boundary_test.go` — enforces module import policy

**Symbols NOT confirmed (gaps):**
- No confirmed symbol needed beyond what's listed above. The guard module only imports `daemon/module` and `protocol`, both fully confirmed.
