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
	t.Logf("response: %q", resp.Content)
}
