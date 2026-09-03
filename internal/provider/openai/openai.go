// Package openai implements provider.Agent against the OpenAI API, using
// the Chat Completions endpoint (not Responses) — simpler request/response
// shape for a single non-streaming call with no tool use.
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
}

type chatCompletionsRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
}

type choice struct {
	Message message `json:"message"`
}

type chatCompletionsResponse struct {
	Choices []choice `json:"choices"`
}

type errorEnvelope struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Generate calls the Chat Completions API, non-streaming. No tool use, no
// retries beyond what net/http gives for free.
func (c *Client) Generate(ctx context.Context, req provider.GenerateRequest) (provider.GenerateResponse, error) {
	messages := make([]message, 0, len(req.Messages)+1)
	if req.SystemPrompt != "" {
		messages = append(messages, message{Role: "system", Content: req.SystemPrompt})
	}
	for _, m := range req.Messages {
		messages = append(messages, message{Role: m.Role, Content: m.Content})
	}

	body, err := json.Marshal(chatCompletionsRequest{
		Model:    c.model,
		Messages: messages,
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

	return provider.GenerateResponse{Content: parsed.Choices[0].Message.Content}, nil
}
