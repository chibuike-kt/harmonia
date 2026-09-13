// Package openai implements provider.Agent against the OpenAI API, using
// the Chat Completions endpoint (not Responses) — simpler request/response
// shape for a single non-streaming call. Tool use goes through Chat
// Completions' own function-calling fields (tools/tool_calls), the same
// endpoint as everything else here — no need for Responses just for this.
package openai

import (
	"bytes"
	"context"
	"encoding/base64"
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

// message is response-only: Chat Completions' own message shape, always
// a plain string Content (the API never sends content blocks back,
// text is text). Kept separate from requestMessage below rather than
// widening this one to `any` — that would mean every response-parsing
// read of .Content had to reckon with a shape check to get a string
// back out, for a case (attachments) that only ever exists on the
// outbound side.
type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// ToolCalls is only ever populated on a response message — never set
	// when a message is used to build an outbound request, so omitempty
	// keeps it out of the request body entirely.
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
}

// requestMessage is request-only. Content is `any`, not `string`: Chat
// Completions accepts either a plain string or an array of content
// parts, and an attachment (ADR-008 batch A) needs the array shape — a
// real image_url/file part alongside the message's own text. buildContent
// below is the only place that decides which shape a given message
// actually needs.
type requestMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// contentPart is one part in a Chat Completions content array — covers
// both shapes this client ever sends: a plain text part, and an
// attachment part (image_url or file, chosen by attachmentPart below).
type contentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *imageURLPart `json:"image_url,omitempty"`
	File     *filePart     `json:"file,omitempty"`
}

type imageURLPart struct {
	URL string `json:"url"`
}

type filePart struct {
	Filename string `json:"filename"`
	FileData string `json:"file_data"`
}

// buildContent decides requestMessage.Content's actual shape: a plain
// string when there's no attachment (every message before this feature,
// and the overwhelming majority after it), or a content-part array when
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

// attachmentPart translates one provider.Attachment into its real Chat
// Completions content part. image_url (a data: URI) and file (also a
// data: URI, via file_data) are Chat Completions' own documented
// shapes for an image and a real PDF respectively; there's no
// dedicated block type for arbitrary plain text the way Anthropic's
// Messages API has a document(text) block — Chat Completions' own real
// capability boundary, not a gap in this translation, so the common "a
// snippet" case is genuinely just inlined as a text part instead. See
// provider.Attachment.Kind's own doc comment for why this classifies by
// MIME type alone.
func attachmentPart(a provider.Attachment) contentPart {
	dataURI := "data:" + a.MimeType + ";base64," + base64.StdEncoding.EncodeToString(a.Content)
	switch a.Kind() {
	case provider.AttachmentImage:
		return contentPart{Type: "image_url", ImageURL: &imageURLPart{URL: dataURI}}
	case provider.AttachmentPDF:
		return contentPart{Type: "file", File: &filePart{Filename: a.Filename, FileData: dataURI}}
	default:
		return contentPart{Type: "text", Text: fmt.Sprintf("Attached file %q:\n%s", a.Filename, string(a.Content))}
	}
}

type chatCompletionsRequest struct {
	Model      string           `json:"model"`
	Messages   []requestMessage `json:"messages"`
	Tools      []toolDef        `json:"tools,omitempty"`
	ToolChoice string           `json:"tool_choice,omitempty"`
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
	messages := make([]requestMessage, 0, len(req.Messages)+1)
	if req.SystemPrompt != "" {
		messages = append(messages, requestMessage{Role: "system", Content: req.SystemPrompt})
	}
	for _, m := range req.Messages {
		messages = append(messages, requestMessage{Role: m.Role, Content: buildContent(m.Content, m.Attachment)})
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
