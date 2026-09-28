// Package verify is the verification policy: require_done_when, mechanical task
// checks, and the stop gate. Without a goal, the stop gate sends one reminder a
// turn listing what is uncommitted, unpushed or still open, and then allows the
// stop. With a goal it vetoes until open tasks, failed checks, the workspace
// gate, the tree and committed build output are all clear, and the goal judge
// agrees. It contributes the checks summary to the run report.
package verify
