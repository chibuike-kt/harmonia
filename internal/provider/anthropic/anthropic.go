// Package anthropic implements provider.Agent against the Anthropic API.
package anthropic

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/chibuike-kt/harmonia/internal/provider"
)

const (
	defaultBaseURL   = "https://api.anthropic.com"
	defaultModel     = "claude-sonnet-5"
	anthropicVersion = "2023-06-01"
	defaultMaxTokens = 4096
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

// message's Content is `any`, not `string`: the Messages API accepts
// either a plain string or an array of content blocks, and an
// attachment (ADR-008 batch A) needs the block-array shape — a real
// image/document block alongside the message's own text, not text with
// the file's bytes pasted in. buildContent below is the only place that
// decides which shape a given message actually needs. This type is
// request-only (Anthropic's response uses its own separate
// messagesResponse/contentBlock types below, never this one), so
// widening Content here has no effect on response parsing.
type message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// contentPart is one block in a Messages API content array — covers
// both shapes this client ever sends: a plain text block, and an
// attachment block (image or document, chosen by attachmentPart below).
type contentPart struct {
	Type   string          `json:"type"`
	Text   string          `json:"text,omitempty"`
	Source *documentSource `json:"source,omitempty"`
	// Title labels a document block with the attachment's own real
	// filename — optional per the API, included because it's exactly
	// the kind of detail that lets a reply plausibly reference "the
	// file you attached" by name instead of a generic "the document."
	Title string `json:"title,omitempty"`
}

// documentSource covers the two source shapes this client sends: "text"
// (the data field is the raw decoded string, no base64 — used for the
// common "a snippet" case) and "base64" (used for a real image or PDF,
// where the bytes genuinely aren't text).
type documentSource struct {
	Type      string `json:"type"` // "base64" | "text"
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data"`
}

// buildContent decides message.Content's actual shape: a plain string
// when there's no attachment (every message before this feature, and
// the overwhelming majority after it), or a content-block array when
// there is one. Returning the bare string rather than a one-element
// array in the common case keeps every existing call's request body
// byte-for-byte identical to before this feature existed.
func buildContent(text string, attachment *provider.Attachment) any {
	if attachment == nil {
		return text
	}
	var parts []contentPart
	if text != "" {
		parts = append(parts, contentPart{Type: "text", Text: text})
	}
	parts = append(parts, attachmentPart(*attachment))
	return parts
}

// attachmentPart translates one provider.Attachment into its real
// Messages API content block — image and document (base64) blocks per
// Anthropic's own documented shapes, a document (text) block for the
// plain-text case that's the common "a snippet" attachment. See
// provider.Attachment.Kind's own doc comment for why this classifies by
// MIME type alone.
func attachmentPart(a provider.Attachment) contentPart {
	switch a.Kind() {
	case provider.AttachmentImage:
		return contentPart{
			Type:   "image",
			Source: &documentSource{Type: "base64", MediaType: a.MimeType, Data: base64.StdEncoding.EncodeToString(a.Content)},
		}
	case provider.AttachmentPDF:
		return contentPart{
			Type:   "document",
			Source: &documentSource{Type: "base64", MediaType: a.MimeType, Data: base64.StdEncoding.EncodeToString(a.Content)},
			Title:  a.Filename,
		}
	default:
		return contentPart{
			Type:   "document",
			Source: &documentSource{Type: "text", MediaType: "text/plain", Data: string(a.Content)},
			Title:  a.Filename,
		}
	}
}

type messagesRequest struct {
	Model      string      `json:"model"`
	MaxTokens  int         `json:"max_tokens"`
	System     string      `json:"system,omitempty"`
	Messages   []message   `json:"messages"`
	Tools      []tool      `json:"tools,omitempty"`
	ToolChoice *toolChoice `json:"tool_choice,omitempty"`
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// toolChoice's Type "any" forces some tool call; the zero value (an
// absent field, via messagesRequest's own omitempty) leaves the
// Messages API's default "auto" in place — a model choosing not to call
// a declared tool.
type toolChoice struct {
	Type string `json:"type"`
}

// contentBlock covers both shapes the Messages API returns in one
// response's content array: a text block (Type "text", Text set) and a
// tool_use block (Type "tool_use", Name/Input set) — the same array can
// hold either or both, so this isn't two separate response shapes to
// switch on, just one block type left blank where it doesn't apply.
type contentBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type messagesResponse struct {
	Content []contentBlock `json:"content"`
	Usage   usage          `json:"usage"`
}

type errorEnvelope struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Generate calls the Messages API, non-streaming. Single-turn tool use
// only (see package provider's own doc comment) — no retries beyond what
// net/http gives for free.
func (c *Client) Generate(ctx context.Context, req provider.GenerateRequest) (provider.GenerateResponse, error) {
	messages := make([]message, 0, len(req.Messages))
	for _, m := range req.Messages {
		messages = append(messages, message{Role: m.Role, Content: buildContent(m.Content, m.Attachment)})
	}

	var tools []tool
	for _, t := range req.Tools {
		tools = append(tools, tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}

	var choice *toolChoice
	if req.RequireToolCall {
		choice = &toolChoice{Type: "any"}
	}

	model := c.model
	if req.Model != "" {
		model = req.Model
	}

	body, err := json.Marshal(messagesRequest{
		Model:      model,
		MaxTokens:  defaultMaxTokens,
		System:     req.SystemPrompt,
		Messages:   messages,
		Tools:      tools,
		ToolChoice: choice,
	})
	if err != nil {
		return provider.GenerateResponse{}, fmt.Errorf("anthropic: encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return provider.GenerateResponse{}, fmt.Errorf("anthropic: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return provider.GenerateResponse{}, fmt.Errorf("anthropic: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return provider.GenerateResponse{}, fmt.Errorf("anthropic: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp errorEnvelope
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error.Message != "" {
			return provider.GenerateResponse{}, fmt.Errorf("anthropic: %s: %s", resp.Status, errResp.Error.Message)
		}
		return provider.GenerateResponse{}, fmt.Errorf("anthropic: %s: %s", resp.Status, string(respBody))
	}

	var parsed messagesResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return provider.GenerateResponse{}, fmt.Errorf("anthropic: decode response: %w", err)
	}

	var content strings.Builder
	var toolCalls []provider.ToolCall
	for _, block := range parsed.Content {
		switch block.Type {
		case "text":
			content.WriteString(block.Text)
		case "tool_use":
			toolCalls = append(toolCalls, provider.ToolCall{Name: block.Name, Input: block.Input})
		}
	}

	return provider.GenerateResponse{
		Content:      content.String(),
		ToolCalls:    toolCalls,
		InputTokens:  parsed.Usage.InputTokens,
		OutputTokens: parsed.Usage.OutputTokens,
	}, nil
}
