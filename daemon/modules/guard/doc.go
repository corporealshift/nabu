// Package guard is the tool gate policy: allow, deny or ask by command pattern, path
// and risk tier, honouring the session's permission_mode. It is always compiled in;
// disabling it appends a notice. git is rated by its verb and flags: a command that
// can destroy work or rewrite history is high risk, so even auto mode asks. A session
// its client labels guard:no-push may not push or post to GitHub in any mode: the
// runner and the GitHub watcher do that themselves.
package guard
