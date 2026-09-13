// Package provider defines the model-agnostic Agent interface. Only
// Generate is implemented for Milestone 1 — Stream and Cancel are real
// future methods on this interface but are left off rather than stubbed,
// since nothing exercises them yet. Generate now also carries optional
// native tool-use (ADR-006 batches B and C: agent-to-agent mentions and
// the create_task/request_handoff tools all go through this one
// mechanism, Anthropic's tool_use and OpenAI's function calling
// underneath) — a single-turn capability, not a multi-turn agentic loop:
// a caller passes Tools the model may call, and reads any calls back off
// the response alongside whatever text content came with them. Nothing
// here feeds a tool's result back to the model for a second turn, since
// none of this milestone's tools need the model to see one.
package provider

import (
	"context"
	"strings"
)

type GenerateRequest struct {
	SystemPrompt string
	Messages     []Message
	// Model overrides the client's own default model for this call only
	// — "" leaves the client's configured default in place, its usual
	// state for every real reply. Exists for ADR-007 batch B's phase-1
	// pickup classification, a distinct, minimal, cheap call each
	// provider client's caller can point at that provider's own
	// cheaper/faster tier without needing a second Client instance
	// (and a second resolved credential) just to change one string.
	Model string
	// Tools the model may call this turn. Nil/empty means no tool-use
	// capability is offered at all — not merely unused: a model can only
	// call a tool that was actually declared for this specific request.
	Tools []ToolDef
	// RequireToolCall forces the model to call one of Tools rather than
	// leaving that to its own judgment (each provider's default, "auto",
	// left in place when this is false).
	//
	// Banned for any call whose output is itself the real, visible
	// product of the turn — a reply, or an agent's own judgment call
	// about whether to use mention_agent/create_task/request_handoff.
	// An agent deciding *not* to call one of those tools is a legitimate,
	// expected outcome, never something to force past; forcing a tool
	// call there would distort the actual conversational answer a human
	// is waiting on. Test-only for exactly this reason: a live-provider
	// test exercising the tool-call parsing path can't rely on prompt-
	// following alone to guarantee one arrives.
	//
	// Correct default, including in production, for a call that was
	// never going to produce a free-text reply in the first place — a
	// dedicated classification/decision step whose entire output is
	// meant to be a structured signal a caller acts on programmatically
	// (ADR-007 batch B's phase-1 pickup classification is exactly this).
	// ADR-007's own build brief names the direct lesson: batch A found
	// that free-text advisory framing asking a model to narrate an
	// action *and* separately call the tool that would make it real
	// isn't reliably honored — the model can narrate a hand-off in text
	// without ever calling mention_agent. That gap is about protecting a
	// real reply's own content; it doesn't apply here, since phase 1 has
	// no reply content to protect — only a decision the calling code
	// needs to be able to act on every time.
	RequireToolCall bool
}

type Message struct {
	Role    string // "user" | "assistant"
	Content string
	// Attachment is the file a human attached to this message, if any
	// (ADR-008 batch A) — nil for the overwhelming majority of messages.
	// Only ever set on a "user" turn: a human is the only sender that can
	// attach a file today, and threading it through the ordinary recency-
	// window history (internal/message.buildGenerateRequest) rather than
	// a side channel is what lets a later turn still reference an
	// earlier attachment, the same way any other real multi-turn
	// conversation with these APIs already works — every attachment ever
	// sent in the conversation stays in context on each subsequent call,
	// not just the turn it arrived on.
	Attachment *Attachment
}

// Attachment is a small file attached directly to one message — a
// snippet or a screenshot, per ADR-008's own scope, not a general
// object-storage system. Content is the raw decoded bytes; each
// provider client is responsible for translating this into that
// provider's own real inline content-block shape (see Kind below for
// the one piece of that decision that isn't provider-specific).
type Attachment struct {
	Content  []byte
	Filename string
	MimeType string
}

// AttachmentKind is the shape decision every provider client needs to
// make when translating an Attachment into its own wire format — real
// image content block, a real PDF document block, or (the common case
// for "a snippet") plain inline text. Classifying this is identical
// logic regardless of provider (a MIME-type check), so it lives here
// once rather than duplicated in each client; only the resulting JSON
// shape for each kind is genuinely provider-specific.
type AttachmentKind int

const (
	AttachmentText AttachmentKind = iota
	AttachmentImage
	AttachmentPDF
)

// Kind classifies a by MIME type alone, not content-sniffing — ADR-008
// scopes this feature to "a snippet, a screenshot," and those are
// exactly what MIME type already tells you honestly. Anything that
// isn't image/* or application/pdf is treated as text: its bytes are
// included verbatim as a text content block, which degrades gracefully
// (not a crash) even for the genuinely-binary edge case this feature
// doesn't target — encoding/json's own UTF-8 handling substitutes the
// replacement character for invalid byte sequences rather than failing
// the request.
func (a Attachment) Kind() AttachmentKind {
	switch {
	case strings.HasPrefix(a.MimeType, "image/"):
		return AttachmentImage
	case a.MimeType == "application/pdf":
		return AttachmentPDF
	default:
		return AttachmentText
	}
}

// ToolDef describes one tool the model may call, translated verbatim
// into each provider's own tool-definition shape — both Anthropic's
// input_schema and OpenAI's function.parameters already speak plain
// JSON Schema, so there's nothing provider-specific to translate here.
type ToolDef struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// ToolCall is one call the model made against a declared ToolDef, read
// back off GenerateResponse. Input is the tool's arguments, already
// decoded from whatever wire shape the provider used (Anthropic returns
// a JSON object directly; OpenAI returns a JSON-encoded string that the
// client decodes here) — a caller never touches either provider's raw
// tool-call encoding.
type ToolCall struct {
	Name  string
	Input map[string]any
}

type GenerateResponse struct {
	Content string
	// ToolCalls holds every tool call the model made this turn, in the
	// order the provider returned them — empty when no Tools were
	// offered, or the model chose not to call any.
	ToolCalls []ToolCall
	// InputTokens/OutputTokens are the real usage counts each provider's
	// own response already includes — captured here so a caller can
	// meter a generation without a second API call. Zero for a provider
	// client that doesn't populate them (there currently isn't one, but
	// nothing here requires every implementation to report usage).
	InputTokens  int
	OutputTokens int
}

type Agent interface {
	Generate(ctx context.Context, req GenerateRequest) (GenerateResponse, error)
}
