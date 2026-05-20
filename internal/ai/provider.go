// Package ai defines the Provider interface and shared types for AI
// model access in klyne.
//
// Providers fall into two camps:
//
//   - CLI subprocess providers (`claude-cli`, `codex-cli`) shell out to
//     the user's already-authenticated CLI binary. Auth is handled by
//     the CLI itself — klyne NEVER reads ~/.claude or ~/.codex
//     credential files directly (spec §8 — Anthropic enforcement of
//     OAuth-token reuse, 2026-04-04 — and spec §17).
//   - Local providers (`ollama`) probe a local server and use it
//     directly. No credential needed.
//
// API-key BYOK is gone: klyne's product promise is "no separate API
// key", so every cloud provider goes through the user's CLI
// subscription.
package ai

import (
	"context"
	"errors"
)

// Provider is the interface every AI backend must implement.
// Name returns a stable lower-case identifier used in config / UI.
// Chat performs a single-turn or multi-turn completion.
// Embed computes a dense vector embedding; return ErrUnsupported if the
// provider does not offer embeddings.
// Models returns the list of model identifiers the user can choose from.
type Provider interface {
	Name() string
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	Embed(ctx context.Context, req EmbedRequest) (*EmbedResponse, error)
	Models() []string
}

// ChatRequest holds the input parameters for a Chat call.
type ChatRequest struct {
	// Model is the model identifier (e.g. "claude-sonnet-4-6").
	Model string
	// Messages is the ordered conversation history.
	Messages []Message
	// MaxTokens caps the completion length. 0 means "provider default".
	MaxTokens int
	// Temperature controls sampling stochasticity. 0 means "provider default".
	Temperature float64
	// SystemPrompt is an optional system / developer message injected before
	// the conversation. Providers that do not have a dedicated system field
	// prepend it as a user message.
	SystemPrompt string
}

// ChatResponse holds the output from a Chat call.
type ChatResponse struct {
	// Text is the assistant's reply.
	Text string
	// TokensIn is the number of prompt tokens consumed, if reported.
	TokensIn int64
	// TokensOut is the number of completion tokens generated, if reported.
	TokensOut int64
	// Model echoes back the model identifier used.
	Model string
	// StopReason is the provider-native stop reason (e.g. "end_turn",
	// "stop", "length").
	StopReason string
}

// EmbedRequest holds the input for an Embed call.
type EmbedRequest struct {
	// Model is the embedding model identifier.
	Model string
	// Input is the text to embed.
	Input string
}

// EmbedResponse holds the output of an Embed call.
type EmbedResponse struct {
	// Vector is the dense float32 embedding.
	Vector []float32
	// Model echoes back the embedding model used.
	Model string
	// TokensIn is the number of tokens consumed, if reported.
	TokensIn int64
}

// Message is a single turn in a conversation. The Role field must be one of
// "user", "assistant", "system", or "tool". This type is local to the ai
// package and is distinct from connectors.Message (which is the canonical
// wire format for stored session messages).
type Message struct {
	// Role is one of: "user", "assistant", "system", "tool".
	Role string
	// Content is the text content of the message.
	Content string
}

// ProviderInfo describes a discovered provider and its availability. It is
// returned by providers.DetectAvailable and consumed by the wizard UI (W15).
type ProviderInfo struct {
	// Name is the stable provider identifier ("claude-cli",
	// "codex-cli", "ollama").
	Name string
	// Available is true when the provider can accept requests right now.
	Available bool
	// Reason is a human-readable explanation shown in the wizard UI.
	// Examples: "`claude` binary on PATH — using your Claude Code
	// subscription", "`codex` binary not on PATH — install OpenAI
	// Codex CLI to enable", "Ollama server unreachable at <url>".
	Reason string
	// Models is the default model list for this provider.
	Models []string
	// SupportsEmbed is true when the provider implements Embed.
	SupportsEmbed bool
}

// Sentinel errors returned by Provider implementations.
var (
	// ErrNoCredential is the legacy "no API key" signal. Post 2026-05-20
	// no provider depends on an API key — the CLI subprocess providers
	// pick up the user's existing subscription auth, and Ollama needs
	// no credential at all. Kept for the noopProvider fallback and any
	// remaining tests that simulate "everything is offline".
	ErrNoCredential = errors.New("ai: no usable provider — install `claude`/`codex` CLI or start Ollama")

	// ErrProviderUnavailable is returned when the provider endpoint
	// cannot be reached: Ollama is not running, or the CLI subprocess
	// is missing or exits non-zero. Callers usually treat this as
	// "skip this turn, retry on next worker tick".
	ErrProviderUnavailable = errors.New("ai: provider unavailable")

	// ErrUnsupported is returned by Embed on providers that do not
	// offer an embeddings API (which is now everyone except Ollama).
	ErrUnsupported = errors.New("ai: operation not supported by this provider")
)
