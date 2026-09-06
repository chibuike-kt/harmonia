// Package provider defines the model-agnostic Agent interface. Only
// Generate is implemented for Milestone 1 — Stream, ToolCall, and Cancel
// are real future methods on this interface but are left off rather than
// stubbed, since nothing exercises them until tool execution (Phase 4).
package provider

import "context"

type GenerateRequest struct {
	SystemPrompt string
	Messages     []Message
}

type Message struct {
	Role    string // "user" | "assistant"
	Content string
}

type GenerateResponse struct {
	Content string
	// InputTokens/OutputTokens are the real usage counts each provider's
	// own response already includes — captured here so a caller can
	// meter a generation without a second API call. Zero for a provider
	// client that doesn't populate them (there currently isn't one, but
	// nothing here requires every implementation to report usage).
	InputTokens  int
	OutputTokens int
}

type Agent interface {
	Generate(ctx context.Context, req GenerateRequest) (GenerateResponse, error)
}
