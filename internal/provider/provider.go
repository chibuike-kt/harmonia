// Package provider defines the model-agnostic Agent interface. Only
// Generate is implemented for Milestone 1 — Stream and Cancel are real
// future methods on this interface but are left off rather than stubbed,
// since nothing exercises them yet. Generate now also carries optional
// native tool-use (ADR-006 batches B and C: agent-to-agent mentions and
// the create_task/request_handoff tools all go through this one
// mechanism, Anthropic's tool_use and OpenAI's function calling
// underneath) — a single-turn capability, not a multi-turn agentic loop:
// a caller passes Tools the model may call, and reads any calls back off
// the response alongside whatever text content came with them. Nothing
// here feeds a tool's result back to the model for a second turn, since
// none of this milestone's tools need the model to see one.
package provider

import "context"

type GenerateRequest struct {
	SystemPrompt string
	Messages     []Message
	// Tools the model may call this turn. Nil/empty means no tool-use
	// capability is offered at all — not merely unused: a model can only
	// call a tool that was actually declared for this specific request.
	Tools []ToolDef
	// RequireToolCall forces the model to call one of Tools rather than
	// leaving that to its own judgment (each provider's default, "auto",
	// left in place when this is false). No production caller in this
	// codebase sets it: an agent deciding *not* to call mention_agent (or,
	// batch C, create_task/request_handoff) is a legitimate, expected
	// outcome, never something to force past. It exists for exactly one
	// real use — a test that needs a live provider call to deterministically
	// exercise the tool-call parsing path can't rely on prompt-following
	// alone to guarantee that.
	RequireToolCall bool
}

type Message struct {
	Role    string // "user" | "assistant"
	Content string
}

// ToolDef describes one tool the model may call, translated verbatim
// into each provider's own tool-definition shape — both Anthropic's
// input_schema and OpenAI's function.parameters already speak plain
// JSON Schema, so there's nothing provider-specific to translate here.
type ToolDef struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// ToolCall is one call the model made against a declared ToolDef, read
// back off GenerateResponse. Input is the tool's arguments, already
// decoded from whatever wire shape the provider used (Anthropic returns
// a JSON object directly; OpenAI returns a JSON-encoded string that the
// client decodes here) — a caller never touches either provider's raw
// tool-call encoding.
type ToolCall struct {
	Name  string
	Input map[string]any
}

type GenerateResponse struct {
	Content string
	// ToolCalls holds every tool call the model made this turn, in the
	// order the provider returned them — empty when no Tools were
	// offered, or the model chose not to call any.
	ToolCalls []ToolCall
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
