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
			Usage: usage{PromptTokens: 20, CompletionTokens: 7},
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
		if gotBody.Messages[i].Role != want.Role || gotBody.Messages[i].Content != want.Content {
			t.Errorf("body.Messages[%d] = %+v, want %+v", i, gotBody.Messages[i], want)
		}
	}

	if resp.Content != "Hello, world." {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello, world.")
	}
	if resp.InputTokens != 20 || resp.OutputTokens != 7 {
		t.Errorf("InputTokens/OutputTokens = %d/%d, want 20/7", resp.InputTokens, resp.OutputTokens)
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

// TestGenerate_WebSearchEnabled_UsesResponsesEndpoint proves the
// dispatcher itself: a call with WebSearchEnabled routes to /v1/responses
// with the Responses API's own request shape, not /v1/chat/completions.
func TestGenerate_WebSearchEnabled_UsesResponsesEndpoint(t *testing.T) {
	var gotPath string
	var gotBody responsesRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(responsesResponse{
			Output: []responsesOutputItem{
				{Type: "message", Content: []responsesContentPart{{Type: "output_text", Text: "Hello, world."}}},
			},
			Usage: responsesUsage{InputTokens: 15, OutputTokens: 5},
		})
	}))
	defer srv.Close()

	c := &Client{apiKey: "test-key", model: defaultModel, baseURL: srv.URL, httpClient: srv.Client()}

	resp, err := c.Generate(context.Background(), provider.GenerateRequest{
		SystemPrompt:     "You are a helpful assistant.",
		Messages:         []provider.Message{{Role: "user", Content: "what's new today?"}},
		WebSearchEnabled: true,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if gotPath != "/v1/responses" {
		t.Errorf("path = %q, want %q", gotPath, "/v1/responses")
	}
	if len(gotBody.Tools) != 1 || gotBody.Tools[0].Type != "web_search" {
		t.Fatalf("body.Tools = %+v, want exactly one web_search tool", gotBody.Tools)
	}
	if len(gotBody.Input) != 2 || gotBody.Input[0].Role != "system" || gotBody.Input[1].Role != "user" {
		t.Errorf("body.Input = %+v, want system then user", gotBody.Input)
	}
	if resp.Content != "Hello, world." {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello, world.")
	}
	if resp.InputTokens != 15 || resp.OutputTokens != 5 {
		t.Errorf("InputTokens/OutputTokens = %d/%d, want 15/5", resp.InputTokens, resp.OutputTokens)
	}
}

// TestGenerate_WebSearchEnabled_FunctionToolsSentUnstrict proves the
// critical, easy-to-get-wrong detail: every function tool sent through
// the Responses path carries an explicit "strict": false, not an
// omitted field — an omitted field would let the API's default strict
// mode silently apply and break tools with genuinely optional
// properties (see requestHandoffTool in internal/message/actions.go).
func TestGenerate_WebSearchEnabled_FunctionToolsSentUnstrict(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(responsesResponse{})
	}))
	defer srv.Close()

	c := &Client{apiKey: "test-key", model: defaultModel, baseURL: srv.URL, httpClient: srv.Client()}

	_, err := c.Generate(context.Background(), provider.GenerateRequest{
		Messages:         []provider.Message{{Role: "user", Content: "hi"}},
		WebSearchEnabled: true,
		Tools: []provider.ToolDef{
			{Name: "create_task", Description: "d", InputSchema: map[string]any{"type": "object"}},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	tools, _ := gotBody["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools = %+v, want web_search + create_task", tools)
	}
	fn, ok := tools[1].(map[string]any)
	if !ok || fn["type"] != "function" {
		t.Fatalf("tools[1] = %+v, want the function tool", tools[1])
	}
	strict, present := fn["strict"]
	if !present {
		t.Fatal("function tool has no \"strict\" field at all — must be explicitly false, not omitted")
	}
	if strict != false {
		t.Errorf("strict = %v, want false", strict)
	}
}

// TestGenerate_WebSearchEnabled_CitationsFromRuneOffsets proves
// annotation extraction slices on rune (Unicode code point) offsets, not
// byte offsets — this client's own AfterText must land on the exact
// substring OpenAI's start_index/end_index describe even when the text
// contains multi-byte characters ahead of the citation.
func TestGenerate_WebSearchEnabled_CitationsFromRuneOffsets(t *testing.T) {
	// "café " is 5 runes but 6 bytes (é is 2 bytes in UTF-8) — a
	// byte-offset slice here would land one rune short.
	text := "café is great"
	runes := []rune(text)
	start, end := 6, 13 // "is great" — after the multi-byte rune

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(responsesResponse{
			Output: []responsesOutputItem{
				{
					Type: "message",
					Content: []responsesContentPart{
						{
							Type: "output_text",
							Text: text,
							Annotations: []responsesAnnotation{
								{Type: "url_citation", StartIndex: start, EndIndex: end, Title: "Cafe Facts", URL: "https://example.com/cafe"},
							},
						},
					},
				},
			},
		})
	}))
	defer srv.Close()

	c := &Client{apiKey: "test-key", model: defaultModel, baseURL: srv.URL, httpClient: srv.Client()}

	resp, err := c.Generate(context.Background(), provider.GenerateRequest{
		Messages:         []provider.Message{{Role: "user", Content: "tell me about cafes"}},
		WebSearchEnabled: true,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(resp.Citations) != 1 {
		t.Fatalf("Citations = %+v, want exactly one", resp.Citations)
	}
	want := string(runes[start:end])
	if resp.Citations[0].AfterText != want {
		t.Errorf("AfterText = %q, want %q", resp.Citations[0].AfterText, want)
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
