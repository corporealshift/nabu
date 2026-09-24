// Package guard is the tool gate policy: allow, deny or ask by command pattern, path
// and risk tier, honouring the session's permission_mode. It is always compiled in;
// disabling it appends a notice. git is rated by its verb and flags: a command that
// can destroy work or rewrite history is high risk, so even auto mode asks.
package guard
