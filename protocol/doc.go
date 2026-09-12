// Package protocol is nabu's wire contract: the session event types, session
// states and options, cursor semantics, JSON-RPC method and error definitions, and the
// conformance vector runner. It is language-neutral by construction: the same contract
// is expressed as JSON Schema under schema/ and exercised by the vectors under
// vectors/, which the Go and Kotlin implementations both run. The normative text is
// spec.md in this directory.
//
// Nothing in this package knows about the daemon, networking, or models.
package protocol
