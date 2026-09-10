// Package openai implements provider.Agent against the OpenAI API, using
// the Chat Completions endpoint (not Responses) — simpler request/response
// shape for a single non-streaming call. Tool use goes through Chat
// Completions' own function-calling fields (tools/tool_calls), the same
// endpoint as everything else here — no need for Responses just for this.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/chibuike-kt/harmonia/internal/provider"
)

const (
	defaultBaseURL = "https://api.openai.com"
	defaultModel   = "gpt-4o-mini"
)

type Client struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

func New(apiKey string) *Client {
	return &Client{
		apiKey:     apiKey,
		model:      defaultModel,
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
	}
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// ToolCalls is only ever populated on a response message — never set
	// when this struct is used to build an outbound request message, so
	// omitempty keeps it out of the request body entirely.
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
}

type chatCompletionsRequest struct {
	Model      string    `json:"model"`
	Messages   []message `json:"messages"`
	Tools      []toolDef `json:"tools,omitempty"`
	ToolChoice string    `json:"tool_choice,omitempty"`
}

type toolDef struct {
	Type     string      `json:"type"`
	Function functionDef `json:"function"`
}

type functionDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// toolCall mirrors Chat Completions' function-calling response shape:
// arguments come back as a JSON-encoded string, not a nested object, so
// Generate decodes it separately rather than unmarshaling straight into
// provider.ToolCall.Input.
type toolCall struct {
	Type     string       `json:"type"`
	Function functionCall `json:"function"`
}

type functionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type choice struct {
	Message message `json:"message"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type chatCompletionsResponse struct {
	Choices []choice `json:"choices"`
	Usage   usage    `json:"usage"`
}

type errorEnvelope struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Generate calls the Chat Completions API, non-streaming. Single-turn
// tool use only (see package provider's own doc comment) — no retries
// beyond what net/http gives for free.
func (c *Client) Generate(ctx context.Context, req provider.GenerateRequest) (provider.GenerateResponse, error) {
	messages := make([]message, 0, len(req.Messages)+1)
	if req.SystemPrompt != "" {
		messages = append(messages, message{Role: "system", Content: req.SystemPrompt})
	}
	for _, m := range req.Messages {
		messages = append(messages, message{Role: m.Role, Content: m.Content})
	}

	var tools []toolDef
	for _, t := range req.Tools {
		tools = append(tools, toolDef{
			Type: "function",
			Function: functionDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	var toolChoice string
	if req.RequireToolCall {
		toolChoice = "required"
	}

	model := c.model
	if req.Model != "" {
		model = req.Model
	}

	body, err := json.Marshal(chatCompletionsRequest{
		Model:      model,
		Messages:   messages,
		Tools:      tools,
		ToolChoice: toolChoice,
	})
	if err != nil {
		return provider.GenerateResponse{}, fmt.Errorf("openai: encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return provider.GenerateResponse{}, fmt.Errorf("openai: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return provider.GenerateResponse{}, fmt.Errorf("openai: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return provider.GenerateResponse{}, fmt.Errorf("openai: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp errorEnvelope
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error.Message != "" {
			return provider.GenerateResponse{}, fmt.Errorf("openai: %s: %s", resp.Status, errResp.Error.Message)
		}
		return provider.GenerateResponse{}, fmt.Errorf("openai: %s: %s", resp.Status, string(respBody))
	}

	var parsed chatCompletionsResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return provider.GenerateResponse{}, fmt.Errorf("openai: decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return provider.GenerateResponse{}, fmt.Errorf("openai: response had no choices")
	}

	var toolCalls []provider.ToolCall
	for _, tc := range parsed.Choices[0].Message.ToolCalls {
		var input map[string]any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
			return provider.GenerateResponse{}, fmt.Errorf("openai: decode tool call arguments for %q: %w", tc.Function.Name, err)
		}
		toolCalls = append(toolCalls, provider.ToolCall{Name: tc.Function.Name, Input: input})
	}

	return provider.GenerateResponse{
		Content:      parsed.Choices[0].Message.Content,
		ToolCalls:    toolCalls,
		InputTokens:  parsed.Usage.PromptTokens,
		OutputTokens: parsed.Usage.CompletionTokens,
	}, nil
}
