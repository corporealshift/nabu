# nabu P1c — `skills` Module Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> This plan specifies contracts and behaviours, not code. Write the failing test first
> from the stated behaviour, then the implementation. If a requirement is ambiguous or
> contradicts the real code, STOP and report rather than guessing.

**Goal:** Let the model discover and read skills, so nabu can use the same skill library Claude Code already has on this machine.

**Architecture:** `skills` is an in-process module implementing `SessionStarter`, `ToolProvider` and `CompactionHook`. It discovers skills from configured paths, injects an index of them as a prefix context block at session start and again after a `summarize` compaction, and registers a tool the model calls to read one skill body on demand.

**Tech Stack:** Go 1.26, standard library only. No new dependencies.

---

## Decisions settled by this plan

The spec leaves several policy details open. This plan decides them:

### Discovery paths

The default search paths are:

1. `~/.claude/skills` — Claude Code compatibility. This is mandatory.
2. `<workspace>/.nabu/skills/` — per-workspace skills, one level below the workspace root under the `.nabu/` directory (consistent with how `~/.nabu/` is the user-level storage and `<workspace>/.nabu/` is the workspace overlay, per spec §8).

Each path may be overridden or extended via config:

```json
{
  "modules": {
    "skills": {
      "enabled": true,
      "paths": ["/custom/skills/dir", "~/.my-skills"]
    }
  }
}
```

When `paths` is set, it **replaces** the defaults entirely (not appends). A missing `paths` key or an absent config section uses the two defaults above.

Discovery recurses into subdirectories (depth-unbounded), follows symlinks, and considers a directory a skill if and only if it contains a file named `SKILL.md`. Files named `SKILL.md` in subdirectories of a skill directory are ignored.

### Index block format

The index is a plain-text block, one line per skill, injected as a `prefix` context. It is compact so the standing cost of including it on every request is small (a few hundred bytes per skill).

```
## Skills

The following skills are available. To read a skill's body, call `skill.load` with the skill name.

- `delegating-to-pi`: Use when delegating work to pi (the local pi coding-agent CLI) — writing a delegation brief, launching a run, or verifying its results.
- `verifying-work`: Use when about to report work as working, passing, fixed, done, or verified — and before writing any verification report or PR description.
- `agent-browser`: Browser automation CLI for AI agents. Use when the user needs to interact with websites...
```

Each entry is one line: the skill name in backticks, a colon, a space, then the description (truncated to 200 characters if longer). The total block is capped at 8 KB; if the index would exceed that, the shortest descriptions are dropped first until it fits.

### `skill.load` tool

The tool takes one argument:

```json
{ "name": "delegating-to-pi" }
```

It returns the full content of the skill's `SKILL.md` file (frontmatter and body, exactly as stored on disk). The tool result is capped at 32 KB; if the body exceeds that, the first 30 KB are returned followed by a note that the file is truncated and the model should request it in sections.

Unknown skill name → error result: `"no skill named <name>"`.

### Malformed skills

A bad skill on disk must not stop the others from being usable:

- **Missing `SKILL.md`:** the directory is ignored.
- **Missing or malformed YAML frontmatter:** the directory is ignored.
- **Missing `name` in frontmatter:** the directory is ignored.
- **Missing `description` in frontmatter:** the directory is accepted but the description defaults to the empty string (the index line will show `(no description)`).
- **Name collision (two skills with the same `name`):** the first one found (in filesystem walk order) wins; the duplicate is silently skipped with a log warning.
- **Unreadable `SKILL.md` (permissions, I/O error):** the directory is ignored.

### When discovery happens

Discovery runs **once at `Init`**, not per session. A long-lived daemon will outlive edits to the skill directory. The `SessionStarter.SessionStart` and `CompactionHook.AfterCompaction` hooks return the same cached index.

If the model needs to discover new skills added since daemon start, it can call `skill.load` on a name it already knows about. Future work may add a `skill.reload` tool, but that is out of scope.

### Frontmatter format

A skill's `SKILL.md` opens with YAML frontmatter delimited by `---` lines:

```
---
name: delegating-to-pi
description: Use when delegating work to pi — writing a delegation brief, launching a run, or verifying its results.
---
```

Only `name` and `description` are required. Any other frontmatter keys are ignored. The frontmatter is parsed line-by-line (not with a YAML library) to keep the dependency count at zero — each `key: value` line is split on the first colon, trimmed, and stored as a map. This is sufficient for the flat key-value pairs the spec uses.

---

## File structure

| Path | Responsibility |
|---|---|
| `daemon/modules/skills/skills.go` | **New.** The skills module: implements `Module`, `SessionStarter`, `ToolProvider`, `CompactionHook`. Discovery, index generation, config parsing, and the `skill.load` tool. |
| `daemon/modules/skills/skills_test.go` | **New.** Table-driven tests for discovery, index generation, `skill.load` tool, config parsing, malformed skills, symlink following, path precedence, and error cases. |
| `daemon/modules/all.go` | **Modify.** Add skills to the All registration list. |

---

## Task 1: Module skeleton and registration

**Files:**
- Create: `daemon/modules/skills/skills.go` (skeleton only — Module interface, no hooks yet)
- Create: `daemon/modules/skills/skills_test.go` (config reading tests only)

**Goal:** The skills module initialises from config, and is registered in all.go. No discovery or hooks yet — just the module structure.

- [ ] **Step 1: Write the failing test for module name and empty init**

Append to `daemon/modules/skills/skills_test.go` a test that creates a zero-value skills module and verifies:
- `Name()` returns the string "skills"
- `Init(nil, nil)` returns nil (no error with empty config)

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./daemon/modules/skills/ -run TestName -v`
Expected: compile error `undefined: Module`.

- [ ] **Step 3: Create the module skeleton**

Create `daemon/modules/skills/skills.go` with:
- A `Module` struct (zero fields for now)
- A `Name()` method returning "skills"
- An `Init(_ module.Host, cfg module.Config) error` method that sets default paths and returns nil

The `Module` struct will later hold a slice of discovered skills and the cached index string.

- [ ] **Step 4: Run the tests — they should pass**

Run: `go test ./daemon/modules/skills/ -run TestName -v`
Expected: both tests pass.

- [ ] **Step 5: Register skills in all.go**

Modify `daemon/modules/all.go` to import the skills package and add a `&skills.Module{}` to the `All` registration list. The import must be blank-identified so the init-time registration happens.

- [ ] **Step 6: Run the boundary test**

Run: `go test ./daemon/module/ -run TestModulesImportOnlyTheContract -v`
Expected: pass (skills only imports `daemon/module` and `protocol`).

- [ ] **Step 7: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

---

## Task 2: Skill discovery

**Files:**
- Modify: `daemon/modules/skills/skills.go` (add discovery logic)

**Goal:** The skills module can discover skills from configured paths: follow symlinks, recurse into subdirectories, parse frontmatter, and build a deduplicated list.

- [ ] **Step 1: Write the failing test for basic discovery**

Append to `daemon/modules/skills/skills_test.go` a test that:
1. Creates a temp directory with two subdirectories, each containing a `SKILL.md` with valid frontmatter
2. Calls the discovery function with that temp directory as the only path
3. Verifies exactly two skills are returned, each with the correct name and description from frontmatter

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./daemon/modules/skills/ -run TestDiscover -v`
Expected: compile error `undefined: discover`.

- [ ] **Step 3: Implement discovery**

Add to `daemon/modules/skills/skills.go`:

A `Skill` value carrying three things: its name, its description, and the absolute path
to its `SKILL.md`. Nothing else — the body is read on demand, never held.

A `discover(dirs []string) ([]Skill, error)` function that:
1. Iterates each directory in `dirs`
2. Walks the directory tree with `filepath.WalkDir`, following symlinks
3. For each directory containing a `SKILL.md` file, reads the frontmatter
4. Parses frontmatter: splits on `---` delimiters, then each `key: value` line on the first colon
5. Extracts `name` (required) and `description` (defaults to empty string if missing)
6. Skips entries with missing `name`, unreadable files, or parse errors, logging a warning via the host logger
7. Deduplicates by `name`: first occurrence wins
8. Returns the sorted (by name) list of skills

- [ ] **Step 4: Write the failing test for symlink following**

Append a test that:
1. Creates a temp directory with a real subdirectory containing `SKILL.md`
2. Creates a symlink in the search directory pointing to that subdirectory
3. Verifies the skill is discovered through the symlink

- [ ] **Step 5: Implement symlink following**

`filepath.WalkDir` follows symlinks by default when the entry is a directory. Ensure the walk function does not skip symlinked directories.

- [ ] **Step 6: Write the failing test for malformed skills**

Append table-driven tests:

**Missing SKILL.md:** A directory with no `SKILL.md` is ignored.

**Missing frontmatter:** A `SKILL.md` with no `---` delimiters is ignored.

**Missing name:** A `SKILL.md` with frontmatter but no `name` key is ignored.

**Missing description:** A `SKILL.md` with frontmatter, a `name`, but no `description` — the skill is accepted with an empty description.

**Name collision:** Two directories with the same `name` in frontmatter — the first one (in filesystem order) wins, the duplicate is skipped.

- [ ] **Step 7: Run the malformed skill tests**

Run: `go test ./daemon/modules/skills/ -run TestMalformedSkills -v`
Expected: all pass.

- [ ] **Step 8: Write the failing test for config paths**

Append tests:

**Default paths:** With no config, the module uses `~/.claude/skills` and `<workspace>/.nabu/skills/`.

**Custom paths:** With `paths` set in config, those paths replace the defaults.

**Empty paths:** With `paths` set to an empty list, no paths are used (no skills discovered).

- [ ] **Step 9: Implement config path parsing**

The `Init` method:
1. Reads `paths` from config as `[]string` via `cfg.Strings("paths", nil)`
2. If `paths` is nil (absent), sets the default paths: `~/.claude/skills` and `<workspace>/.nabu/skills/`
3. If `paths` is an empty slice, sets paths to nil (no discovery)
4. Stores the paths in the module struct

The workspace path comes from the `Session` at hook time, not from `Init`. Therefore, the workspace-specific path is resolved lazily in `SessionStart`, not in `Init`. The module stores the config paths separately and prepends the workspace path at session start.

- [ ] **Step 10: Run all discovery tests**

Run: `go test ./daemon/modules/skills/ -v`
Expected: all tests pass.

- [ ] **Step 11: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

---

## Task 3: Index generation and prefix context block

**Files:**
- Modify: `daemon/modules/skills/skills.go` (add index generation, SessionStarter)

**Goal:** The module generates a compact text index from discovered skills and implements `SessionStarter` to return it as a prefix context block.

- [ ] **Step 1: Write the failing test for index format**

Append to `daemon/modules/skills/skills_test.go` a test that creates three skills with known names and descriptions, calls the index generation function, and verifies the output matches the expected format:

```
## Skills

The following skills are available. To read a skill's body, call `skill.load` with the skill name.

- `name1`: description one.
- `name2`: description two.
- `name3`: description three.
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./daemon/modules/skills/ -run TestIndexFormat -v`
Expected: compile error `undefined: index`.

- [ ] **Step 3: Implement index generation**

Add to `daemon/modules/skills/skills.go`:

A `formatIndex(skills []Skill) string` function that:
1. Builds the header lines exactly as specified above
2. For each skill, formats a line: `- \`<name>\`: <description>`
3. Truncates descriptions longer than 200 characters (with no ellipsis — the raw text is cut at 200 chars)
4. Sorts skills alphabetically by name
5. Caps the total output at 8 KB; if exceeded, drops the longest descriptions first until it fits

- [ ] **Step 4: Write the failing test for 8 KB cap**

Append a test that creates many skills with long descriptions and verifies the index does not exceed 8 KB.

- [ ] **Step 5: Run the cap test**

Run: `go test ./daemon/modules/skills/ -run TestIndexCap -v`
Expected: pass.

- [ ] **Step 6: Implement SessionStarter**

Add to `daemon/modules/skills/skills.go`:

`SessionStart` satisfies `module.SessionStarter`: it takes a context and a session, and
returns context blocks and an error.

The method:
1. Uses the skills discovered once at `Init`. It does **not** re-discover, and it does
   not consult the session's workspace. Search paths are global, so the index is the
   same for every session and can be computed once.
2. Generates the index via `formatIndex()`.
3. Returns a single `ContextBlock` with the prefix slot and the index as content.
4. Returns no block at all when no skills were found, rather than an empty index. A
   block that says "no skills" is context spent to say nothing.

**Why not workspace-local skills.** A `<workspace>/.nabu/skills/` path would be useful,
but it makes the index workspace-dependent, which contradicts discovering once at
`Init` and would require per-session state on a module struct shared by every
concurrent session. Spec 14.7 names configured paths and `~/.claude/skills` only.
Workspace-local skills are a deliberate non-goal here; revisit them with a design that
does not put per-session state on a shared module.

- [ ] **Step 7: Write the failing test for SessionStart**

Append a test that:
1. Creates a temp directory with a skill
2. Creates a fake session with that temp directory as the workspace
3. Calls `SessionStart` and verifies it returns one prefix context block containing the skill's name and description

- [ ] **Step 8: Run the SessionStart test**

Run: `go test ./daemon/modules/skills/ -run TestSessionStart -v`
Expected: pass.

- [ ] **Step 9: Run all skills tests**

Run: `go test ./daemon/modules/skills/ -v`
Expected: all tests pass.

- [ ] **Step 10: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

---

## Task 4: skill.load tool

**Files:**
- Modify: `daemon/modules/skills/skills.go` (add ToolProvider, skill.load)

**Goal:** The module registers a `skill.load` tool that the model calls to read a skill body on demand.

- [ ] **Step 1: Write the failing test for skill.load**

Append to `daemon/modules/skills/skills_test.go` a test that:
1. Creates a temp directory with a `SKILL.md` containing known frontmatter and body
2. Calls `skill.load` with the correct name
3. Verifies the result is the full file content (frontmatter + body, exactly as stored)

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./daemon/modules/skills/ -run TestSkillLoad -v`
Expected: compile error `undefined: (*Module).Tools` and `undefined: skillLoad`.

- [ ] **Step 3: Implement skill.load**

Add to `daemon/modules/skills/skills.go`:

A `skillLoad(ctx context.Context, s module.Session, args json.RawMessage) (string, error)` function:
1. Unmarshals args into `struct { Name string }`
2. Looks up the skill by name in the discovered skills list
3. If not found, returns an error: `"no skill named <name>"`
4. Reads the `SKILL.md` file
5. If the content exceeds 32 KB, returns the first 30 KB followed by a note: `"[... truncated; file is larger than 32 KB]"`
6. Returns the content as a string

A `Tools() []module.Tool` method on `Module`:
1. Returns a single `module.Tool` with:
   - `Name: "skill.load"`
   - `Description: "Read the full body of a discovered skill by name."`
   - `Schema: json.RawMessage(`{"type":"object","required":["name"],"properties":{"name":{"type":"string"}}}`)`
   - `Run: skillLoad`

- [ ] **Step 4: Write the failing test for unknown name**

Append a test that calls `skill.load` with a name that does not exist and verifies the error message is `"no skill named <unknown-name>"`.

- [ ] **Step 5: Write the failing test for large skill body**

Append a test that creates a `SKILL.md` with 40 KB of content and verifies `skill.load` returns the first 30 KB plus the truncation note.

- [ ] **Step 6: Run all skill.load tests**

Run: `go test ./daemon/modules/skills/ -run TestSkillLoad -v`
Expected: all pass.

- [ ] **Step 7: Run all skills tests**

Run: `go test ./daemon/modules/skills/ -v`
Expected: all tests pass.

- [ ] **Step 8: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

---

## Task 5: CompactionHook — re-inject index after summarize

**Files:**
- Modify: `daemon/modules/skills/skills.go` (add CompactionHook)

**Goal:** After a `summarize` compaction, the skills module re-injects its index as a prefix context block. The prefix is fixed between compactions, so the index must be re-injected when the prefix is retired.

- [ ] **Step 1: Write the failing test for AfterCompaction**

Append to `daemon/modules/skills/skills_test.go` a test that:
1. Creates a skills module with discovered skills
2. Calls `AfterCompaction` (with a dummy session)
3. Verifies it returns one prefix context block containing the index

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./daemon/modules/skills/ -run TestAfterCompaction -v`
Expected: compile error `undefined: (*Module).AfterCompaction`.

- [ ] **Step 3: Implement CompactionHook**

Add to `daemon/modules/skills/skills.go`:

Both methods of `module.CompactionHook`. `BeforeCompaction` takes a context, a session
and the range being compacted, and returns the strings the summary must preserve.
`AfterCompaction` takes a context and a session, and returns context blocks and an
error.

`BeforeCompaction` returns `nil` (no strings to preserve in the summary).

`AfterCompaction` regenerates the index from the cached discovered skills and returns it as a single prefix `ContextBlock`. If no skills were discovered, it returns an empty slice (no context block).

- [ ] **Step 4: Run the AfterCompaction test**

Run: `go test ./daemon/modules/skills/ -run TestAfterCompaction -v`
Expected: pass.

- [ ] **Step 5: Run all skills tests**

Run: `go test ./daemon/modules/skills/ -v`
Expected: all tests pass.

- [ ] **Step 6: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

---

## Task 6: Integration tests and edge cases

**Files:**
- Modify: `daemon/modules/skills/skills_test.go`

**Goal:** Verify the module integrates correctly with the registry dispatch and handle remaining edge cases.

- [ ] **Step 1: Write the failing integration test**

Append to `daemon/modules/skills/skills_test.go` a test that:
1. Creates a skills module with skills from a temp directory
2. Creates a `module.Registry` with just the skills module
3. Creates a session with a workspace
4. Verifies:
   - `Registry.SessionStart()` returns one prefix context block containing the skills index
   - `Registry.Tools()` returns the `skill.load` tool with the correct name and schema
   - Calling `skill.load` through the registry tool dispatch returns the correct body

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./daemon/modules/skills/ -run TestSkillsIntegratesWithRegistry -v`
Expected: fails because the fakeSession needs to implement the full `module.Session` interface with a proper `State()` method.

- [ ] **Step 3: Fix the test**

The `fakeSession` used in tests must implement the full `module.Session` interface. The `State()` method must return a `protocol.State` with the correct `Options.PermissionMode`. The `Workspace()` method must return the correct path.

- [ ] **Step 4: Run the integration test**

Run: `go test ./daemon/modules/skills/ -run TestSkillsIntegratesWithRegistry -v`
Expected: pass.

- [ ] **Step 5: Write the failing test for edge cases**

Append tests:

**No skills found:** A search path that exists but contains no `SKILL.md` files returns zero skills and an empty index.

**Non-existent path:** A search path that does not exist on disk is silently skipped (not an error).

**Empty skill name in frontmatter:** A skill with `name: ""` is ignored.

**Unicode in description:** A skill with non-ASCII characters in its description is handled correctly.

**Config disabled:** `Init` with `enabled: false` returns nil and the registry will not call hooks on a dead module.

- [ ] **Step 6: Run all edge case tests**

Run: `go test ./daemon/modules/skills/ -v`
Expected: all pass.

- [ ] **Step 7: Run the full gate**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages pass, no vet errors.

- [ ] **Step 8: Run gofmt check**

Run: `gofmt -l daemon/modules/skills/`
Expected: no output.

---

## Summary

**Files created:**
- `daemon/modules/skills/skills.go` — the skills module
- `daemon/modules/skills/skills_test.go` — comprehensive tests

**Files modified:**
- `daemon/modules/all.go` — register skills in the module list

**Tasks:** 6 ordered tasks, each independently verifiable

**Decisions made:**

**Discovery paths:**
- Default: `~/.claude/skills` and `<workspace>/.nabu/skills/`
- Config `paths` replaces defaults entirely
- Recurse into subdirectories, follow symlinks
- Only directories with `SKILL.md` are skills

**Index format:**
- Plain text, one line per skill: `- \`name\`: description`
- Sorted alphabetically, capped at 8 KB
- Descriptions truncated to 200 chars

**skill.load tool:**
- Takes `name` (string), returns full `SKILL.md` content
- Unknown name → error
- Body capped at 32 KB with truncation note

**Malformed skills:**
- Missing frontmatter, missing name → skip
- Missing description → accept with empty description
- Name collision → first wins
- Unreadable file → skip

**Discovery timing:**
- Once at `Init`, cached for the session
- `AfterCompaction` re-injects the same cached index

**Symbols confirmed in source:**
- `module.Module` → `daemon/module/module.go` — `Name() string`, `Init(Host, Config) error`
- `module.SessionStarter` → `daemon/module/module.go` — `SessionStart(ctx, Session) ([]ContextBlock, error)`
- `module.CompactionHook` → `daemon/module/module.go` — `BeforeCompaction(ctx, Session, Range) []string`, `AfterCompaction(ctx, Session) ([]ContextBlock, error)`
- `module.ToolProvider` → `daemon/module/module.go` — `Tools() []Tool`
- `module.ContextBlock` → `daemon/module/module.go` — `Slot string`, `Content string`
- `module.Tool` → `daemon/module/module.go` — `Name`, `Description`, `Schema json.RawMessage`, `Run func`
- `module.Session` → `daemon/module/module.go` — `ID()`, `Workspace()`, `Events()`, `State()`, `Append()`
- `module.Workspace` → `daemon/module/module.go` — `Path string`, `Key string`
- `module.Config` → `daemon/module/module.go` — `map[string]any` with `Bool`, `String`, `Int`, `Strings`, `Enabled`
- `module.Registry.SessionStart` → `daemon/module/registry.go` — returns `[]SourcedBlock`
- `module.Registry.AfterCompaction` → `daemon/module/registry.go` — returns `[]SourcedBlock`
- `module.Registry.Tools` → `daemon/module/registry.go` — returns `[]Tool`, error on duplicate names
- `module.SourcedBlock` → `daemon/module/registry.go` — `Module string`, `Block ContextBlock`
- `protocol.ToolCallData` → `protocol/types.go` — `CallID`, `Tool`, `Arguments json.RawMessage`, `Source`
- `protocol.ToolResultData` → `protocol/types.go` — `CallID`, `Tool`, `Content`, `Status`
- `protocol.State` → `protocol/types.go` — `Options Options` with `PermissionMode PermissionMode`
- `protocol.Options` → `protocol/types.go` — `Model`, `CompactionEnabled`, `PermissionMode`
- `protocol.PermissionMode` → `protocol/types.go` — `PermissionAsk`, `PermissionAuto`, `PermissionBypass`
- `tools.Builtins` → `daemon/tools/builtins.go` — tool registration via `module.ToolProvider`
- `tools.decode[T]` → `daemon/tools/builtins.go` — lenient JSON unmarshalling for tool args
- `tools.truncate` → `daemon/tools/builtins.go` — truncates oversized output
- `module.All` → `daemon/modules/all.go` — registration list
- `module.boundary_test.go` → `daemon/module/boundary_test.go` — enforces module import policy
- `protocol.SegmentKind`, `protocol.Assemble` → `protocol/assemble.go` — context segments in request assembly

**Symbols NOT confirmed (gaps):**
- No gaps — all needed symbols confirmed in source. The skills module only imports `daemon/module` and `protocol`, both fully confirmed.
