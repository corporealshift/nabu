# nabu

Go coding-agent harness: a daemon that owns append-only session logs, thin clients
over JSON-RPC/WebSocket, policy in in-process modules. Owner: Kyle. Single developer.

## Read first

- `ARCHITECTURE.md` — the map and the seven invariants.
- `docs/superpowers/specs/2026-09-11-nabu-architecture-design.md` — every decision and
  its rejected alternatives. Do not re-litigate settled decisions; do propose changes
  as a new dated spec if a decision no longer holds.
- `protocol/spec.md` — the normative wire contract. Changing an event or method means
  changing the spec, the JSON schema, and a conformance vector together.

## Build and test

```
go build ./... && go vet ./... && go test ./...
```

Go 1.26 on Windows. Standard library where practical. Table-driven tests. Run the
full gate, not a single package, before claiming anything works.

## Conventions

- Commit messages: `area: lowercase summary` (`protocol: cursor semantics`,
  `agent: stop gate`, `docs: p1 plan`). Never conventional-commits prefixes.
- Stage named files, never `git add -A`.
- Mechanism in core, policy in modules. If a change adds an opinion to
  `daemon/agent` or `daemon/session`, it probably belongs in a module.
- Every model-visible injection is a `context` event. Every plugin-like side effect is
  a logged event with a `source`.
- Modules import only `daemon/module` and `protocol` from this repo; the boundary test
  in `daemon/module` enforces it.
- Line endings are LF (see `.gitattributes`).

## Layout

See `ARCHITECTURE.md`. New modules go under `daemon/modules/<name>/` and get one line
in `daemon/modules/all.go`.
