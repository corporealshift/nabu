// Package verify is the verification policy: require_done_when, mechanical task
// checks, the open-task veto, the run-level goal judged in a fresh context, the
// workspace gate command, the dirty-tree veto, and a veto on build output the
// session committed (both under require_clean_tree). It contributes the checks
// summary to the run report.
package verify
