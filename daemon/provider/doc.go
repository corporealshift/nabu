// Package provider is the OpenAI-compatible streaming chat-completions client with
// retries, per-provider concurrency limits (max_in_flight), and usage accounting.
// Every model call the daemon makes, including calls made by modules, goes through
// here.
package provider
