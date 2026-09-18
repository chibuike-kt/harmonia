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
	// webSearchToolType is the base, non-dated web search tool version
	// (ADR-008 batch B) — Anthropic also publishes dated variants
	// (20260209, 20260318) with newer defaults, but nothing in this
	// feature's scope needs those, and pinning the base version avoids
	// silently picking up behavior changes on Anthropic's own schedule.
	webSearchToolType = "web_search_20250305"
	// webSearchMaxUses caps searches per turn — a real budget knob, not a
	// formality: each search is its own $10/1000 line item (see
	// GenerateResponse's doc comment on the token/dollar-cost gap this
	// opens), independent of and in addition to token cost.
	webSearchMaxUses = 5
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
// every shape this client ever sends: a plain text block, an attachment
// block (image or document, chosen by attachmentPart below), and, since
// ADR-011's multi-turn tool-calling, a tool_use block (echoing an
// assistant's own earlier call back to the model — ID/Name/Input) and a
// tool_result block (that call's real result — ToolUseID/Content).
type contentPart struct {
	Type   string          `json:"type"`
	Text   string          `json:"text,omitempty"`
	Source *documentSource `json:"source,omitempty"`
	// Title labels a document block with the attachment's own real
	// filename — optional per the API, included because it's exactly
	// the kind of detail that lets a reply plausibly reference "the
	// file you attached" by name instead of a generic "the document."
	Title string `json:"title,omitempty"`
	// ID/Name/Input build a tool_use block — Anthropic's own real
	// identifier for one assistant tool call, echoed back exactly as the
	// API returned it (see Generate's response parsing) so a later
	// tool_result can reference it.
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`
	// ToolUseID/Content build a tool_result block — Content here is a
	// plain string (the API also accepts a content-block array; nothing
	// this client's tools produce needs anything richer than text).
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
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

// tool covers both shapes the Messages API's tools array accepts: a
// custom tool (Name/Description/InputSchema, Type left blank — the API
// treats an absent type as "custom") and Anthropic's server-side web
// search tool (Type/Name/MaxUses set, Description/InputSchema blank).
// Both shapes live in the same array in the same request when a call
// needs custom tools and search together, so this is one type with
// unused fields left as their zero value per shape, not two.
type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
	Type        string         `json:"type,omitempty"`
	MaxUses     int            `json:"max_uses,omitempty"`
}

func webSearchTool() tool {
	return tool{Type: webSearchToolType, Name: "web_search", MaxUses: webSearchMaxUses}
}

// toolChoice's Type "any" forces some tool call; the zero value (an
// absent field, via messagesRequest's own omitempty) leaves the
// Messages API's default "auto" in place — a model choosing not to call
// a declared tool.
type toolChoice struct {
	Type string `json:"type"`
}

// contentBlock covers every shape the Messages API returns in one
// response's content array: a text block (Type "text", Text and,
// when web search grounded it, Citations set), a tool_use block (Type
// "tool_use", Name/Input set), and — only when WebSearchEnabled — the
// server-side search's own server_tool_use/web_search_tool_result
// blocks, which this client deliberately ignores (see Generate's parse
// loop): their content is Anthropic's own search bookkeeping, already
// surfaced to us pre-digested as each text block's Citations.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
	// ID is a tool_use block's own real identifier — captured so a
	// multi-turn caller (internal/agentloop) can echo this exact call back
	// on a later Message.ToolCalls and key its result on Message.ToolCallID
	// (ADR-011). Empty on every other block type.
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     map[string]any  `json:"input"`
	Citations []citationBlock `json:"citations"`
}

// citationBlock is one web_search_result_location entry on a text
// block's citations array. CitedText is the exact substring of that
// same block's Text the citation covers — handed to us directly, no
// offset math needed (contrast OpenAI's Responses-API annotations,
// which give rune offsets instead and require the client to derive this
// same substring itself).
type citationBlock struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	CitedText string `json:"cited_text"`
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

// Generate calls the Messages API, non-streaming. No retries beyond what
// net/http gives for free.
func (c *Client) Generate(ctx context.Context, req provider.GenerateRequest) (provider.GenerateResponse, error) {
	messages := make([]message, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch {
		// ADR-011 multi-turn: an assistant turn that called a tool is
		// echoed back as its own real tool_use block(s), never collapsed
		// into plain text — the Messages API rejects a later tool_result
		// whose tool_use_id doesn't match a tool_use block it can see
		// earlier in the same request.
		case len(m.ToolCalls) > 0:
			var parts []contentPart
			if m.Content != "" {
				parts = append(parts, contentPart{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				parts = append(parts, contentPart{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: tc.Input})
			}
			messages = append(messages, message{Role: "assistant", Content: parts})
		// Anthropic has no native "tool" role — a tool's result is a
		// tool_result block on a user-role message instead.
		case m.ToolCallID != "":
			messages = append(messages, message{Role: "user", Content: []contentPart{
				{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content},
			}})
		default:
			messages = append(messages, message{Role: m.Role, Content: buildContent(m.Content, m.Attachment)})
		}
	}

	var tools []tool
	for _, t := range req.Tools {
		tools = append(tools, tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	// Same endpoint, same request shape as every other call — declaring
	// search is just one more entry in the same tools array custom
	// tools already use (see tool's own doc comment). Nothing else about
	// this request changes for a search-enabled call.
	if req.WebSearchEnabled {
		tools = append(tools, webSearchTool())
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
	var citations []provider.Citation
	for _, block := range parsed.Content {
		switch block.Type {
		case "text":
			content.WriteString(block.Text)
			for _, c := range block.Citations {
				citations = append(citations, provider.Citation{Title: c.Title, URL: c.URL, AfterText: c.CitedText})
			}
		case "tool_use":
			toolCalls = append(toolCalls, provider.ToolCall{ID: block.ID, Name: block.Name, Input: block.Input})
		// server_tool_use / web_search_tool_result blocks (only present
		// when WebSearchEnabled) fall through with no case and are
		// silently skipped — that's Anthropic's own search bookkeeping,
		// already surfaced to us pre-digested as each text block's own
		// Citations above.
		default:
		}
	}

	return provider.GenerateResponse{
		Content:      content.String(),
		ToolCalls:    toolCalls,
		Citations:    citations,
		InputTokens:  parsed.Usage.InputTokens,
		OutputTokens: parsed.Usage.OutputTokens,
	}, nil
}
