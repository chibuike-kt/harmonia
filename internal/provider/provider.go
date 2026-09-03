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
}

type Agent interface {
	Generate(ctx context.Context, req GenerateRequest) (GenerateResponse, error)
}
