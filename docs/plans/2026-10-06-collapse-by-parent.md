# Collapsing sessions by parent: plan

Spec: `docs/specs/2026-10-06-collapse-by-parent-design.md`. One branch,
`collapse-by-parent`, with one commit per step. Each step leaves the gate green.

## 1. Archive and restore take the family

- **Changes:**
  - `daemon/agent/manager.go`: `Archive` collects the descendants from `Store.List`,
    refuses if any is running, and archives each with its notice. `Restore` brings back
    the descendants whose last notice names the parent. `ArchiveIdle` sweeps families.
  - `protocol/spec.md` §7.19 and §7.20.
- **Proves it:** new tests in `daemon/agent/archive_test.go`; the full gate.
- **Depends on:** nothing.

## 2. TUI collapse

- **Changes:** `clients/go-tui/`: expanded state in the model, `visibleSessions` derived
  from `groupByParent` and that state, `→`/`←` in the picker, a child count on the row.
- **Proves it:** picker tests; the full gate.
- **Depends on:** nothing.

## 3. Android collapse

- **Changes:** `ui/Runs.kt`: `visible(placed, expanded)` and child counts. `Screens.kt`:
  a chevron and the expanded set.
- **Proves it:** unit tests; `gradlew.sh :app:testDebugUnitTest :app:assembleDebug`; a
  look on the phone.
- **Depends on:** nothing.
