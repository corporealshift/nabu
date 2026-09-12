// Package module defines the extension contract: the Module interface, the optional
// hook interfaces (SessionStarter, RequestHook, ToolGate, ToolProvider, ToolObserver,
// TurnObserver, StopGate, CompactionHook, ResumeHook, SessionEnder, Reporter), the
// Host API modules see the daemon through, the registry, hook dispatch with recover()
// and per-hook timeouts, and the import-boundary test that keeps modules from
// reaching into daemon internals.
package module
