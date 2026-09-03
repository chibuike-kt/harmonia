package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chibuike-kt/harmonia/internal/provider"
)

func TestGenerate_RequestShapeAndResponseParsing(t *testing.T) {
	var gotPath, gotAuth, gotContentType string
	var gotBody chatCompletionsRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(chatCompletionsResponse{
			Choices: []choice{
				{Message: message{Role: "assistant", Content: "Hello, world."}},
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

	if gotPath != "/v1/chat/completions" {
		t.Errorf("path = %q, want %q", gotPath, "/v1/chat/completions")
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-key")
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", gotContentType, "application/json")
	}
	if gotBody.Model != defaultModel {
		t.Errorf("body.Model = %q, want %q", gotBody.Model, defaultModel)
	}
	wantMessages := []message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "Say hi."},
	}
	if len(gotBody.Messages) != len(wantMessages) {
		t.Fatalf("body.Messages = %+v, want %+v", gotBody.Messages, wantMessages)
	}
	for i, want := range wantMessages {
		if gotBody.Messages[i] != want {
			t.Errorf("body.Messages[%d] = %+v, want %+v", i, gotBody.Messages[i], want)
		}
	}

	if resp.Content != "Hello, world." {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello, world.")
	}
}

func TestGenerate_NoSystemPrompt(t *testing.T) {
	var gotBody chatCompletionsRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(chatCompletionsResponse{
			Choices: []choice{{Message: message{Role: "assistant", Content: "ok"}}},
		})
	}))
	defer srv.Close()

	c := &Client{apiKey: "test-key", model: defaultModel, baseURL: srv.URL, httpClient: srv.Client()}

	_, err := c.Generate(context.Background(), provider.GenerateRequest{
		Messages: []provider.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(gotBody.Messages) != 1 || gotBody.Messages[0].Role != "user" {
		t.Errorf("body.Messages = %+v, want exactly one user message, no system message", gotBody.Messages)
	}
}

func TestGenerate_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"message": "Invalid API key"},
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
