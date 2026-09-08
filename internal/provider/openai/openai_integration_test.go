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

// TestIntegration_Generate_CreateTaskToolCallParsing is ADR-006 batch C's
// same live proof for create_task's tool shape — a single required
// string field, the simplest of the three tools this codebase declares,
// but still real wire data, not a fake's.
//
// Skips without OPENAI_API_KEY; incurs a small real cost when it runs.
func TestIntegration_Generate_CreateTaskToolCallParsing(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set; skipping integration test")
	}

	c := New(apiKey)

	resp, err := c.Generate(context.Background(), provider.GenerateRequest{
		SystemPrompt: "Create a task to write the release notes.",
		Messages: []provider.Message{
			{Role: "user", Content: "Create a task to write the release notes."},
		},
		RequireToolCall: true,
		Tools: []provider.ToolDef{
			{
				Name:        "create_task",
				Description: "Create a new task in this room.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"objective": map[string]any{
							"type":        "string",
							"description": "A concise, actionable statement of what the task is.",
						},
					},
					"required": []string{"objective"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(resp.ToolCalls) == 0 {
		t.Fatalf("expected the model to call create_task, got zero tool calls (content=%q)", resp.Content)
	}
	call := resp.ToolCalls[0]
	if call.Name != "create_task" {
		t.Fatalf("ToolCalls[0].Name = %q, want %q", call.Name, "create_task")
	}
	objective, ok := call.Input["objective"].(string)
	if !ok || objective == "" {
		t.Fatalf("ToolCalls[0].Input[%q] = %v (ok=%v), want a non-empty string", "objective", call.Input["objective"], ok)
	}
	t.Logf("real tool call parsed: %s(objective=%q)", call.Name, objective)
}

// TestIntegration_Generate_RequestHandoffToolCallParsing is ADR-006 batch
// C's live proof for request_handoff's tool shape — the most complex of
// the three, with string-array fields (completed/remaining/risks)
// alongside plain strings. Arrays are exactly the shape most likely to
// silently break if OpenAI's JSON-encoded arguments string ever decoded
// differently than assumed (a string array member that isn't actually a
// string, say), so this is the one real check most worth having, not
// just the simpler single-string tools.
//
// Skips without OPENAI_API_KEY; incurs a small real cost when it runs.
func TestIntegration_Generate_RequestHandoffToolCallParsing(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set; skipping integration test")
	}

	c := New(apiKey)

	fakeTaskID := "11111111-1111-1111-1111-111111111111"
	resp, err := c.Generate(context.Background(), provider.GenerateRequest{
		SystemPrompt: "Propose handing off task " + fakeTaskID + " to \"Pong\", with a summary and at least one completed item and one remaining item.",
		Messages: []provider.Message{
			{Role: "user", Content: "Hand this off to Pong."},
		},
		RequireToolCall: true,
		Tools: []provider.ToolDef{
			{
				Name:        "request_handoff",
				Description: "Propose handing off an open task to another agent. Does not execute immediately.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"task_id":       map[string]any{"type": "string", "description": "The id of the task to hand off."},
						"to_agent_name": map[string]any{"type": "string", "description": "The agent to hand the task off to."},
						"summary":       map[string]any{"type": "string", "description": "A summary of the work so far."},
						"completed":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "What's already been done."},
						"remaining":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "What's left to do."},
					},
					"required": []string{"task_id", "to_agent_name", "summary"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(resp.ToolCalls) == 0 {
		t.Fatalf("expected the model to call request_handoff, got zero tool calls (content=%q)", resp.Content)
	}
	call := resp.ToolCalls[0]
	if call.Name != "request_handoff" {
		t.Fatalf("ToolCalls[0].Name = %q, want %q", call.Name, "request_handoff")
	}
	taskID, ok := call.Input["task_id"].(string)
	if !ok || taskID == "" {
		t.Fatalf("Input[%q] = %v (ok=%v), want a non-empty string", "task_id", call.Input["task_id"], ok)
	}
	toAgentName, ok := call.Input["to_agent_name"].(string)
	if !ok || toAgentName == "" {
		t.Fatalf("Input[%q] = %v (ok=%v), want a non-empty string", "to_agent_name", call.Input["to_agent_name"], ok)
	}
	summary, ok := call.Input["summary"].(string)
	if !ok || summary == "" {
		t.Fatalf("Input[%q] = %v (ok=%v), want a non-empty string", "summary", call.Input["summary"], ok)
	}
	// completed/remaining are optional in the schema — assert on shape
	// only when the model actually included one, but require it decode
	// to a real []any of strings, not silently fail the type assertion.
	for _, field := range []string{"completed", "remaining"} {
		raw, present := call.Input[field]
		if !present {
			continue
		}
		items, ok := raw.([]any)
		if !ok {
			t.Fatalf("Input[%q] = %v (%T), want a JSON array", field, raw, raw)
		}
		for i, item := range items {
			if _, ok := item.(string); !ok {
				t.Fatalf("Input[%q][%d] = %v (%T), want a string", field, i, item, item)
			}
		}
	}
	t.Logf("real tool call parsed: %s(task_id=%q, to_agent_name=%q, summary=%q, completed=%v, remaining=%v)",
		call.Name, taskID, toAgentName, summary, call.Input["completed"], call.Input["remaining"])
}
