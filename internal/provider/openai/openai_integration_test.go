package openai

import (
	"context"
	"os"
	"testing"

	"github.com/chibuike-kt/harmonia/internal/provider"
)

// TestIntegration_Generate calls the real OpenAI Chat Completions API.
// Skips without OPENAI_API_KEY. Requires network access to api.openai.com
// and will incur a small real cost when it runs.
func TestIntegration_Generate(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set; skipping integration test")
	}

	c := New(apiKey)

	resp, err := c.Generate(context.Background(), provider.GenerateRequest{
		SystemPrompt: "Reply with exactly one word.",
		Messages: []provider.Message{
			{Role: "user", Content: "Say hello."},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Content == "" {
		t.Fatal("expected a non-empty response")
	}
	if resp.InputTokens == 0 || resp.OutputTokens == 0 {
		t.Fatalf("expected real non-zero token usage from a live response, got input=%d output=%d", resp.InputTokens, resp.OutputTokens)
	}
	t.Logf("response: %q (input_tokens=%d output_tokens=%d)", resp.Content, resp.InputTokens, resp.OutputTokens)
}

// TestIntegration_Generate_ToolCallParsing calls the real Chat
// Completions API with a tool declared and RequireToolCall set, proving
// Generate's tool_calls parsing (function.arguments as a JSON-encoded
// string, decoded here into ToolCall.Input) matches the real wire shape
// OpenAI actually returns, not just the shape a fake provider in
// internal/message's own tests is written to produce. ADR-006 batch B's
// mention_agent tool is the first real caller of this path; batch C's
// create_task/request_handoff inherit it unchanged, so a mismatch here
// would otherwise carry forward silently into both.
//
// RequireToolCall (tool_choice: "required") is deliberate, not just a
// prompt: OpenAI's default tool_choice is "auto", so without it this
// test would depend on the model's own judgment call every run — a
// real, if small, flake risk that has nothing to do with what this test
// is actually checking (the parsing, not whether the model feels like
// mentioning Pong today). Forcing the call removes that variable
// entirely; RequireToolCall is never set outside tests like this one —
// see its own doc comment on provider.GenerateRequest.
//
// Skips without OPENAI_API_KEY, same convention as every other real-
// provider test in this codebase; incurs a small real cost when it runs.
func TestIntegration_Generate_ToolCallParsing(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set; skipping integration test")
	}

	c := New(apiKey)

	resp, err := c.Generate(context.Background(), provider.GenerateRequest{
		SystemPrompt: "Bring \"Pong\" into the conversation.",
		Messages: []provider.Message{
			{Role: "user", Content: "Bring Pong into this conversation."},
		},
		RequireToolCall: true,
		Tools: []provider.ToolDef{
			{
				Name:        "mention_agent",
				Description: "Bring another agent already in this room into the conversation by name.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"agent_name": map[string]any{
							"type":        "string",
							"description": "The exact display name of the agent to mention.",
						},
					},
					"required": []string{"agent_name"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(resp.ToolCalls) == 0 {
		t.Fatalf("expected the model to call mention_agent, got zero tool calls (content=%q)", resp.Content)
	}
	call := resp.ToolCalls[0]
	if call.Name != "mention_agent" {
		t.Fatalf("ToolCalls[0].Name = %q, want %q", call.Name, "mention_agent")
	}
	name, ok := call.Input["agent_name"].(string)
	if !ok || name == "" {
		t.Fatalf("ToolCalls[0].Input[%q] = %v (ok=%v), want a non-empty string — real arguments failed to decode into the expected shape", "agent_name", call.Input["agent_name"], ok)
	}
	t.Logf("real tool call parsed: %s(agent_name=%q)", call.Name, name)
}
