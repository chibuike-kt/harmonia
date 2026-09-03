package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chibuike-kt/harmonia/internal/provider"
)

// TestGenerate_RequestShapeAndResponseParsing proves the client is
// structurally right — correct endpoint, headers, and Messages API body
// shape, and that a canned response parses into GenerateResponse. It does
// NOT prove the client works against the real Anthropic API; there is no
// ANTHROPIC_API_KEY available to verify that with (see
// TestIntegration_Generate, which skips without one).
func TestGenerate_RequestShapeAndResponseParsing(t *testing.T) {
	var gotPath, gotAPIKey, gotVersion, gotContentType string
	var gotBody messagesRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(messagesResponse{
			Content: []contentBlock{
				{Type: "text", Text: "Hello, "},
				{Type: "text", Text: "world."},
			},
		})
	}))
	defer srv.Close()

	c := &Client{apiKey: "test-key", model: defaultModel, baseURL: srv.URL, httpClient: srv.Client()}

	resp, err := c.Generate(context.Background(), provider.GenerateRequest{
		SystemPrompt: "You are a helpful assistant.",
		Messages: []provider.Message{
			{Role: "user", Content: "Say hi."},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if gotPath != "/v1/messages" {
		t.Errorf("path = %q, want %q", gotPath, "/v1/messages")
	}
	if gotAPIKey != "test-key" {
		t.Errorf("x-api-key = %q, want %q", gotAPIKey, "test-key")
	}
	if gotVersion != anthropicVersion {
		t.Errorf("anthropic-version = %q, want %q", gotVersion, anthropicVersion)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", gotContentType, "application/json")
	}
	if gotBody.Model != defaultModel {
		t.Errorf("body.Model = %q, want %q", gotBody.Model, defaultModel)
	}
	if gotBody.MaxTokens != defaultMaxTokens {
		t.Errorf("body.MaxTokens = %d, want %d", gotBody.MaxTokens, defaultMaxTokens)
	}
	if gotBody.System != "You are a helpful assistant." {
		t.Errorf("body.System = %q, want %q", gotBody.System, "You are a helpful assistant.")
	}
	if len(gotBody.Messages) != 1 || gotBody.Messages[0].Role != "user" || gotBody.Messages[0].Content != "Say hi." {
		t.Errorf("body.Messages = %+v, want one user message %q", gotBody.Messages, "Say hi.")
	}

	if resp.Content != "Hello, world." {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello, world.")
	}
}

func TestGenerate_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "error",
			"error": map[string]string{
				"type":    "authentication_error",
				"message": "invalid x-api-key",
			},
		})
	}))
	defer srv.Close()

	c := &Client{apiKey: "bad-key", model: defaultModel, baseURL: srv.URL, httpClient: srv.Client()}

	_, err := c.Generate(context.Background(), provider.GenerateRequest{
		Messages: []provider.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected an error on a non-200 response")
	}
}
