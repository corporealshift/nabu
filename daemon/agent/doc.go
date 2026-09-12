// Package agent is the agent loop: assemble the model request as a pure function of
// the log, stream from the provider, dispatch tool calls through the gate, run the stop
// gate, compact, enforce the budget, and survive restarts. Every state transition it
// makes is an appended event. Policy lives in modules; this package only knows how to
// ask them.
package agent
