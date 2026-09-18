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
			Usage: usage{InputTokens: 12, OutputTokens: 4},
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
	if resp.InputTokens != 12 || resp.OutputTokens != 4 {
		t.Errorf("InputTokens/OutputTokens = %d/%d, want 12/4", resp.InputTokens, resp.OutputTokens)
	}
}

// TestGenerate_ToolUseResponse_CapturesRealID proves a tool_use block's
// own real id survives into provider.ToolCall.ID — the multi-turn
// building block ADR-011's agent loop echoes back on a later assistant
// message so a matching tool_result resolves against something the API
// actually issued, not a client-invented value.
func TestGenerate_ToolUseResponse_CapturesRealID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(messagesResponse{
			Content: []contentBlock{
				{Type: "tool_use", ID: "toolu_01ABC", Name: "read_file", Input: map[string]any{"path": "main.go"}},
			},
			Usage: usage{InputTokens: 5, OutputTokens: 2},
		})
	}))
	defer srv.Close()

	c := &Client{apiKey: "test-key", model: defaultModel, baseURL: srv.URL, httpClient: srv.Client()}
	resp, err := c.Generate(context.Background(), provider.GenerateRequest{
		Messages: []provider.Message{{Role: "user", Content: "read main.go"}},
		Tools:    []provider.ToolDef{{Name: "read_file", InputSchema: map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "toolu_01ABC" || resp.ToolCalls[0].Name != "read_file" {
		t.Fatalf("ToolCalls = %+v, want one call with ID toolu_01ABC", resp.ToolCalls)
	}
}

// TestGenerate_MultiTurnToolRoundTrip proves the real second-turn wire
// shape: an assistant message carrying ToolCalls becomes a real tool_use
// content block, and a message with ToolCallID set becomes a real
// tool_result block on a user-role message keyed by the same id — the
// exact round trip ADR-011's sustained loop depends on to feed a tool's
// result back to the model.
func TestGenerate_MultiTurnToolRoundTrip(t *testing.T) {
	var gotBody messagesRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(messagesResponse{
			Content: []contentBlock{{Type: "text", Text: "done"}},
			Usage:   usage{InputTokens: 1, OutputTokens: 1},
		})
	}))
	defer srv.Close()

	c := &Client{apiKey: "test-key", model: defaultModel, baseURL: srv.URL, httpClient: srv.Client()}
	_, err := c.Generate(context.Background(), provider.GenerateRequest{
		Messages: []provider.Message{
			{Role: "user", Content: "read main.go"},
			{Role: "assistant", ToolCalls: []provider.ToolCall{
				{ID: "toolu_01ABC", Name: "read_file", Input: map[string]any{"path": "main.go"}},
			}},
			{Role: "tool", ToolCallID: "toolu_01ABC", Content: "package main"},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(gotBody.Messages) != 3 {
		t.Fatalf("got %d messages, want 3", len(gotBody.Messages))
	}

	assistantMsg := gotBody.Messages[1]
	if assistantMsg.Role != "assistant" {
		t.Fatalf("messages[1].Role = %q, want assistant", assistantMsg.Role)
	}
	assistantParts, ok := assistantMsg.Content.([]any)
	if !ok || len(assistantParts) != 1 {
		t.Fatalf("messages[1].Content = %#v, want one tool_use block", assistantMsg.Content)
	}
	toolUse, _ := assistantParts[0].(map[string]any)
	if toolUse["type"] != "tool_use" || toolUse["id"] != "toolu_01ABC" || toolUse["name"] != "read_file" {
		t.Fatalf("tool_use block = %+v, want type/id/name for toolu_01ABC/read_file", toolUse)
	}

	resultMsg := gotBody.Messages[2]
	if resultMsg.Role != "user" {
		t.Fatalf("messages[2].Role = %q, want user (Anthropic has no native tool role)", resultMsg.Role)
	}
	resultParts, ok := resultMsg.Content.([]any)
	if !ok || len(resultParts) != 1 {
		t.Fatalf("messages[2].Content = %#v, want one tool_result block", resultMsg.Content)
	}
	toolResult, _ := resultParts[0].(map[string]any)
	if toolResult["type"] != "tool_result" || toolResult["tool_use_id"] != "toolu_01ABC" || toolResult["content"] != "package main" {
		t.Fatalf("tool_result block = %+v, want type/tool_use_id/content for toolu_01ABC", toolResult)
	}
}

// TestGenerate_WebSearchDisabled_ToolsUntouched proves that a call with
// WebSearchEnabled false — every call before ADR-008 batch B, and the
// overwhelming majority after it — sends exactly the tools array it
// always did, with no web_search tool appended.
func TestGenerate_WebSearchDisabled_ToolsUntouched(t *testing.T) {
	var gotBody messagesRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(messagesResponse{
			Content: []contentBlock{{Type: "text", Text: "ok"}},
		})
	}))
	defer srv.Close()

	c := &Client{apiKey: "test-key", model: defaultModel, baseURL: srv.URL, httpClient: srv.Client()}

	_, err := c.Generate(context.Background(), provider.GenerateRequest{
		Messages: []provider.Message{{Role: "user", Content: "hi"}},
		Tools:    []provider.ToolDef{{Name: "create_task", Description: "d", InputSchema: map[string]any{}}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(gotBody.Tools) != 1 || gotBody.Tools[0].Name != "create_task" {
		t.Errorf("body.Tools = %+v, want exactly the one declared custom tool, no web_search", gotBody.Tools)
	}
	if gotBody.Tools[0].Type != "" {
		t.Errorf("body.Tools[0].Type = %q, want empty (custom tool)", gotBody.Tools[0].Type)
	}
}

// TestGenerate_WebSearchEnabled_DeclaresToolAndExtractsCitations proves
// the other half: a search-enabled call declares the native web_search
// tool alongside any custom tools, and a citations array on a text block
// is translated into provider.Citation with the cited text carried
// verbatim as AfterText.
func TestGenerate_WebSearchEnabled_DeclaresToolAndExtractsCitations(t *testing.T) {
	var gotBody messagesRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(messagesResponse{
			Content: []contentBlock{
				{
					Type: "text",
					Text: "The sky is blue.",
					Citations: []citationBlock{
						{URL: "https://example.com/sky", Title: "Sky Facts", CitedText: "The sky is blue."},
					},
				},
			},
			Usage: usage{InputTokens: 30, OutputTokens: 8},
		})
	}))
	defer srv.Close()

	c := &Client{apiKey: "test-key", model: defaultModel, baseURL: srv.URL, httpClient: srv.Client()}

	resp, err := c.Generate(context.Background(), provider.GenerateRequest{
		Messages:         []provider.Message{{Role: "user", Content: "why is the sky blue?"}},
		WebSearchEnabled: true,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(gotBody.Tools) != 1 || gotBody.Tools[0].Type != webSearchToolType || gotBody.Tools[0].Name != "web_search" {
		t.Fatalf("body.Tools = %+v, want one web_search tool of type %q", gotBody.Tools, webSearchToolType)
	}
	if gotBody.Tools[0].MaxUses != webSearchMaxUses {
		t.Errorf("body.Tools[0].MaxUses = %d, want %d", gotBody.Tools[0].MaxUses, webSearchMaxUses)
	}

	if len(resp.Citations) != 1 {
		t.Fatalf("Citations = %+v, want exactly one", resp.Citations)
	}
	got := resp.Citations[0]
	if got.URL != "https://example.com/sky" || got.Title != "Sky Facts" || got.AfterText != "The sky is blue." {
		t.Errorf("Citations[0] = %+v, want URL/Title/AfterText from the response's citation block", got)
	}
}

// TestGenerate_WebSearchEnabled_RequireToolCall_ForcesSearch proves the
// per-message forced-search case (RequireToolCall + WebSearchEnabled,
// with no other Tools) sends tool_choice "any" against a tools array
// holding only web_search — the model has no other declared tool to
// dodge into.
func TestGenerate_WebSearchEnabled_RequireToolCall_ForcesSearch(t *testing.T) {
	var gotBody messagesRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(messagesResponse{Content: []contentBlock{{Type: "text", Text: "ok"}}})
	}))
	defer srv.Close()

	c := &Client{apiKey: "test-key", model: defaultModel, baseURL: srv.URL, httpClient: srv.Client()}

	_, err := c.Generate(context.Background(), provider.GenerateRequest{
		Messages:         []provider.Message{{Role: "user", Content: "search the web for X"}},
		WebSearchEnabled: true,
		RequireToolCall:  true,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(gotBody.Tools) != 1 || gotBody.Tools[0].Name != "web_search" {
		t.Fatalf("body.Tools = %+v, want exactly one web_search tool", gotBody.Tools)
	}
	if gotBody.ToolChoice == nil || gotBody.ToolChoice.Type != "any" {
		t.Errorf("body.ToolChoice = %+v, want type \"any\"", gotBody.ToolChoice)
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
