// Package openai implements provider.Agent against the OpenAI API. Every
// call that doesn't need web search goes through Chat Completions — the
// simpler request/response shape, and the endpoint every non-search
// feature in this codebase has always used. Web search (ADR-008 batch B)
// only exists on OpenAI's separate Responses API, so Generate dispatches
// a search-enabled call to a second, additive implementation
// (generateViaResponses) instead; generateViaChatCompletions, and every
// call that doesn't set WebSearchEnabled, is untouched by that addition.
package openai

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
//
// ToolCalls/ToolCallID are ADR-011's multi-turn addition: an assistant
// message that made tool calls carries them here (Chat Completions' own
// real "assistant turn that called a tool" shape), and a "tool" role
// message carries the id of the call it answers — both omitempty, so
// every request built before this feature existed is byte-for-byte
// unchanged.
type requestMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
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

// toolCall mirrors Chat Completions' function-calling shape on both
// sides: arguments travel as a JSON-encoded string, not a nested object,
// so Generate decodes/encodes it separately rather than unmarshaling (or
// marshaling) straight into provider.ToolCall.Input. ID is the real
// wire-level call id — present on every response, and required back on
// an outbound request that echoes an assistant's tool call (ADR-011
// multi-turn), so a later "tool" message's own tool_call_id resolves to
// something the API actually issued.
type toolCall struct {
	ID       string       `json:"id,omitempty"`
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

// Generate dispatches to whichever of this client's two real
// implementations a call actually needs — a hard branch at the top, not
// a merged code path, so that every call not requesting search is
// provably running the same generateViaChatCompletions this client has
// always used, byte-for-byte, with generateViaResponses never entered.
func (c *Client) Generate(ctx context.Context, req provider.GenerateRequest) (provider.GenerateResponse, error) {
	if req.WebSearchEnabled {
		return c.generateViaResponses(ctx, req)
	}
	return c.generateViaChatCompletions(ctx, req)
}

// generateViaChatCompletions calls the Chat Completions API,
// non-streaming. Single-turn tool use only (see package provider's own
// doc comment) — no retries beyond what net/http gives for free.
func (c *Client) generateViaChatCompletions(ctx context.Context, req provider.GenerateRequest) (provider.GenerateResponse, error) {
	messages := make([]requestMessage, 0, len(req.Messages)+1)
	if req.SystemPrompt != "" {
		messages = append(messages, requestMessage{Role: "system", Content: req.SystemPrompt})
	}
	for _, m := range req.Messages {
		switch {
		// ADR-011 multi-turn: an assistant turn that called a tool is
		// echoed back with its own real tool_calls array — Chat
		// Completions rejects a later "tool" message whose tool_call_id
		// doesn't match one of these.
		case len(m.ToolCalls) > 0:
			calls := make([]toolCall, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				args, err := json.Marshal(tc.Input)
				if err != nil {
					return provider.GenerateResponse{}, fmt.Errorf("openai: encode tool call arguments for %q: %w", tc.Name, err)
				}
				calls[i] = toolCall{ID: tc.ID, Type: "function", Function: functionCall{Name: tc.Name, Arguments: string(args)}}
			}
			var content any
			if m.Content != "" {
				content = m.Content
			}
			messages = append(messages, requestMessage{Role: "assistant", Content: content, ToolCalls: calls})
		// Chat Completions has a real native "tool" role for exactly this.
		case m.ToolCallID != "":
			messages = append(messages, requestMessage{Role: "tool", Content: m.Content, ToolCallID: m.ToolCallID})
		default:
			messages = append(messages, requestMessage{Role: m.Role, Content: buildContent(m.Content, m.Attachment)})
		}
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
		toolCalls = append(toolCalls, provider.ToolCall{ID: tc.ID, Name: tc.Function.Name, Input: input})
	}

	return provider.GenerateResponse{
		Content:      parsed.Choices[0].Message.Content,
		ToolCalls:    toolCalls,
		InputTokens:  parsed.Usage.PromptTokens,
		OutputTokens: parsed.Usage.CompletionTokens,
	}, nil
}

// --- Responses API (ADR-008 batch B) ---
//
// Web search only exists on OpenAI's Responses API — Chat Completions has
// no equivalent tool, at any version. Everything below is a second,
// self-contained request/response shape used only by generateViaResponses,
// entered only when a call sets WebSearchEnabled; it shares no types with
// the Chat Completions path above, deliberately, so a change here can't
// alter that path's wire bytes.

// responsesInputItem is one entry in a Responses API call's input array —
// the rough equivalent of a Chat Completions message, but under a
// different field name and, per responsesContent below, a structurally
// different content-part shape.
type responsesInputItem struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// responsesContentPart covers every shape this client sends or reads on
// the Responses API: input_text/input_image/input_file (request, chosen
// by responsesAttachmentPart) and output_text (response, with Annotations
// carrying any web-search citations). Unlike Chat Completions'
// image_url part, Responses' input_image takes the data URI as a plain
// string field, not a nested object — confirmed against a live request.
type responsesContentPart struct {
	Type        string                `json:"type"`
	Text        string                `json:"text,omitempty"`
	ImageURL    string                `json:"image_url,omitempty"`
	Filename    string                `json:"filename,omitempty"`
	FileData    string                `json:"file_data,omitempty"`
	Annotations []responsesAnnotation `json:"annotations,omitempty"`
}

// buildResponsesContent mirrors buildContent's own shape decision (plain
// string with no attachment, a part array with one) for the Responses
// API's distinct part types — kept as a separate function rather than a
// shared helper because the two APIs' part shapes genuinely differ
// (input_text/input_image/input_file vs. text/image_url/file), not just
// in field names.
func buildResponsesContent(text string, attachment *provider.Attachment) any {
	if attachment == nil {
		return text
	}
	var parts []responsesContentPart
	if text != "" {
		parts = append(parts, responsesContentPart{Type: "input_text", Text: text})
	}
	parts = append(parts, responsesAttachmentPart(*attachment))
	return parts
}

// responsesAttachmentPart is the Responses-API equivalent of
// attachmentPart above, same MIME-based classification (see
// provider.Attachment.Kind), same data-URI construction, translated into
// the Responses API's own input_image/input_file/input_text part shapes.
func responsesAttachmentPart(a provider.Attachment) responsesContentPart {
	dataURI := "data:" + a.MimeType + ";base64," + base64.StdEncoding.EncodeToString(a.Content)
	switch a.Kind() {
	case provider.AttachmentImage:
		return responsesContentPart{Type: "input_image", ImageURL: dataURI}
	case provider.AttachmentPDF:
		return responsesContentPart{Type: "input_file", Filename: a.Filename, FileData: dataURI}
	default:
		return responsesContentPart{Type: "input_text", Text: fmt.Sprintf("Attached file %q:\n%s", a.Filename, string(a.Content))}
	}
}

// responsesAnnotation is one url_citation entry on an output_text part's
// annotations array. StartIndex/EndIndex are Unicode code-point (Go
// rune) offsets into that same part's own Text — confirmed empirically
// against a live response, not documented explicitly by OpenAI as any
// particular unit — so citation extraction below rune-slices Text rather
// than byte-slicing it.
type responsesAnnotation struct {
	Type       string `json:"type"`
	StartIndex int    `json:"start_index"`
	EndIndex   int    `json:"end_index"`
	Title      string `json:"title"`
	URL        string `json:"url"`
}

// responsesTool covers both shapes the Responses API's tools array
// accepts: the native web_search tool (Type only) and a function tool,
// flattened directly onto the tool object rather than nested under a
// "function" key the way Chat Completions' toolDef does — confirmed
// against a live request. Strict is a pointer so generateViaResponses can
// send an explicit false (see its own comment) rather than leaving the
// field absent, which would let the Responses API's default strict mode
// silently apply.
type responsesTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Strict      *bool          `json:"strict,omitempty"`
}

type responsesRequest struct {
	Model      string               `json:"model"`
	Input      []responsesInputItem `json:"input"`
	Tools      []responsesTool      `json:"tools,omitempty"`
	ToolChoice string               `json:"tool_choice,omitempty"`
}

// responsesOutputItem covers every shape the Responses API's output
// array can hold in one response: "message" (Content set, the model's
// own reply), "function_call" (Name/Arguments/CallID set), and
// "web_search_call" plus any other item type this client doesn't
// recognize — left with every field at its zero value and skipped during
// parsing, the same graceful-ignore treatment Anthropic's client gives
// its own non-text server-side blocks.
type responsesOutputItem struct {
	Type      string                 `json:"type"`
	Content   []responsesContentPart `json:"content,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Arguments string                 `json:"arguments,omitempty"`
	CallID    string                 `json:"call_id,omitempty"`
}

// responsesUsage uses the Responses API's own field names — input_tokens/
// output_tokens, not Chat Completions' prompt_tokens/completion_tokens —
// same semantic meaning (tokens sent, tokens generated), different wire
// names, confirmed live: the two paths' usage shapes are not literally
// symmetrical even though what they report is equivalent.
type responsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type responsesResponse struct {
	Output []responsesOutputItem `json:"output"`
	Usage  responsesUsage        `json:"usage"`
}

// strictFalse backs every function tool's explicit Strict pointer below.
// The Responses API defaults an omitted strict to true, which auto-
// injects additionalProperties:false onto the tool's schema — silently
// incompatible with requestHandoffTool's genuinely optional properties
// (completed/remaining/risks aren't in its required list). Setting this
// explicitly on every function tool keeps this path's tool-call behavior
// equivalent to Chat Completions', which has no such implicit strict
// mode at all. Confirmed live: an omitted strict field does change
// behavior here, unlike on Chat Completions.
var strictFalse = false

// generateViaResponses calls the Responses API — the only OpenAI
// endpoint with a web search tool. Entered only when WebSearchEnabled is
// set (see Generate's dispatcher above); every request/response type
// here is this function's own, shared with nothing in
// generateViaChatCompletions.
func (c *Client) generateViaResponses(ctx context.Context, req provider.GenerateRequest) (provider.GenerateResponse, error) {
	input := make([]responsesInputItem, 0, len(req.Messages)+1)
	if req.SystemPrompt != "" {
		input = append(input, responsesInputItem{Role: "system", Content: req.SystemPrompt})
	}
	for _, m := range req.Messages {
		input = append(input, responsesInputItem{Role: m.Role, Content: buildResponsesContent(m.Content, m.Attachment)})
	}

	tools := []responsesTool{{Type: "web_search"}}
	for _, t := range req.Tools {
		tools = append(tools, responsesTool{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.InputSchema,
			Strict:      &strictFalse,
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

	body, err := json.Marshal(responsesRequest{
		Model:      model,
		Input:      input,
		Tools:      tools,
		ToolChoice: toolChoice,
	})
	if err != nil {
		return provider.GenerateResponse{}, fmt.Errorf("openai: encode responses request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/responses", bytes.NewReader(body))
	if err != nil {
		return provider.GenerateResponse{}, fmt.Errorf("openai: build responses request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return provider.GenerateResponse{}, fmt.Errorf("openai: responses request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return provider.GenerateResponse{}, fmt.Errorf("openai: read responses response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp errorEnvelope
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error.Message != "" {
			return provider.GenerateResponse{}, fmt.Errorf("openai: %s: %s", resp.Status, errResp.Error.Message)
		}
		return provider.GenerateResponse{}, fmt.Errorf("openai: %s: %s", resp.Status, string(respBody))
	}

	var parsed responsesResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return provider.GenerateResponse{}, fmt.Errorf("openai: decode responses response: %w", err)
	}

	var content strings.Builder
	var toolCalls []provider.ToolCall
	var citations []provider.Citation
	for _, item := range parsed.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type != "output_text" {
					continue
				}
				content.WriteString(part.Text)
				runes := []rune(part.Text)
				for _, ann := range part.Annotations {
					if ann.Type != "url_citation" || ann.StartIndex < 0 || ann.EndIndex > len(runes) || ann.StartIndex >= ann.EndIndex {
						continue
					}
					citations = append(citations, provider.Citation{
						Title:     ann.Title,
						URL:       ann.URL,
						AfterText: string(runes[ann.StartIndex:ann.EndIndex]),
					})
				}
			}
		case "function_call":
			var input map[string]any
			if err := json.Unmarshal([]byte(item.Arguments), &input); err != nil {
				return provider.GenerateResponse{}, fmt.Errorf("openai: decode tool call arguments for %q: %w", item.Name, err)
			}
			toolCalls = append(toolCalls, provider.ToolCall{Name: item.Name, Input: input})
		// web_search_call and any other item type carry nothing this
		// client uses and are silently skipped.
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
