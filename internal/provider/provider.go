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
	"strconv"
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
	//
	// ADR-008 batch B adds a second legitimate production use: a human's
	// explicit per-message "search the web" attach. That's the same
	// missing-signal problem phase-1 pickup solves for, just from a
	// different direction — there a model's own free-text narration
	// can't be trusted to substitute for a real tool call; here a human
	// has given explicit, deliberate intent that a specific tool run
	// (not a reply, not the model's own judgment about whether search is
	// warranted) is what this turn is for, and prompt-following alone
	// can't guarantee the model honors that over its own instinct to
	// just answer from what it already knows. Unlike phase-1 pickup,
	// this call's output *is* a real reply the human is waiting on — but
	// the human's explicit request, not the model's judgment, is what's
	// being forced here, and that request is specifically "use this
	// tool," so forcing it doesn't distort the answer, it's what the
	// human asked for. Callers using this for forced search restrict
	// Tools to just the search tool for that call — RequireToolCall only
	// says "call something in Tools," and mixing in create_task/
	// mention_agent/request_handoff would let the model dodge into one
	// of those instead and defeat the whole point.
	RequireToolCall bool
	// WebSearchEnabled offers the provider's native web search tool this
	// turn (ADR-008 batch B) — Anthropic's server-side web_search tool,
	// OpenAI's Responses-API web_search tool. Distinct from Tools: this
	// isn't a caller-defined ToolDef the model calls back through
	// ToolCalls, it's a provider-native capability each client declares
	// and resolves entirely server-side, surfacing only through Content
	// (the model's own grounded prose) and Citations. True for either of
	// two independent triggers — a room's own search toggle (advisory:
	// the model decides whether a given question actually warrants a
	// search) or a human's explicit per-message forced attach (paired
	// with RequireToolCall so the model can't skip it) — orchestrate.go
	// is what tells the two apart; this field alone doesn't.
	WebSearchEnabled bool
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
	// Citations backs any web-search grounding the model actually used
	// this turn (ADR-008 batch B) — empty whenever WebSearchEnabled was
	// false, or true but the model didn't judge the question worth a
	// search. Each provider client is responsible for populating this
	// from that provider's own real citation metadata only; never
	// fabricated or inferred client-side. See ApplyCitations.
	Citations []Citation
	// InputTokens/OutputTokens are the real usage counts each provider's
	// own response already includes — captured here so a caller can
	// meter a generation without a second API call. Zero for a provider
	// client that doesn't populate them (there currently isn't one, but
	// nothing here requires every implementation to report usage).
	//
	// Token-based cost tracking built on these two fields alone is
	// accurate for both providers' own tool-use overhead (search-related
	// prompt/completion tokens flow through the ordinary token counts on
	// both sides — nothing special to do there) but incomplete for
	// Anthropic specifically once WebSearchEnabled is in play: Anthropic
	// bills each individual search itself as a separate flat line item
	// ($10 per 1,000 searches, reported as usage.server_tool_use.
	// web_search_requests in the raw response) that this client doesn't
	// currently surface anywhere — not in these fields, not elsewhere on
	// GenerateResponse. A room with search on will cost real money this
	// package's own cost tracking won't show. OpenAI's Responses API has
	// no equivalent separate line item as of this writing — its search
	// cost is folded into ordinary token usage — so this gap is
	// Anthropic-specific, not a symmetry gap between the two providers'
	// InputTokens/OutputTokens themselves (those line up fine; only the
	// wire field names differ — see openai.responsesUsage's own comment).
	InputTokens  int
	OutputTokens int
}

// Citation is one provider-neutral web-search source, translated from
// that provider's own citation shape by its client. AfterText is the
// exact substring of Content (verbatim, safe for strings.Index) the
// citation marker belongs after — Anthropic hands this back directly as
// cited_text; OpenAI's client derives it by slicing Content on the
// annotation's rune offsets. Carrying a substring instead of a numeric
// offset is deliberate: ApplyCitations inserts markers by search, never
// by splicing raw indices into stored message content, so it can't
// corrupt Content even if a provider's offsets were ever off by one or
// measured in a different unit than Go's runes.
type Citation struct {
	Title     string
	URL       string
	AfterText string
}

// ApplyCitations renders numbered inline markers plus a trailing
// "Sources" list from a provider's raw Citations, and is the only place
// that does — callers never hand-roll citation markup. Citations sharing
// a URL are deduped to one source entry but keep separate inline
// markers at each of their AfterText occurrences, matching how a paper
// reuses one footnote number for repeat references to the same source.
// A citation whose AfterText doesn't appear verbatim in content (should
// not happen given each client sources it from that same response's own
// Content, but a provider response is still untrusted input) is
// silently skipped rather than corrupting the text or panicking.
func ApplyCitations(content string, citations []Citation) string {
	if len(citations) == 0 {
		return content
	}

	order := make([]string, 0, len(citations))
	sources := make(map[string]Citation, len(citations))
	numbers := make(map[string]int, len(citations))

	result := content
	for _, c := range citations {
		if c.AfterText == "" {
			continue
		}
		idx := strings.Index(result, c.AfterText)
		if idx < 0 {
			continue
		}
		if _, seen := numbers[c.URL]; !seen {
			order = append(order, c.URL)
			sources[c.URL] = c
			numbers[c.URL] = len(order)
		}
		marker := "[" + strconv.Itoa(numbers[c.URL]) + "]"
		insertAt := idx + len(c.AfterText)
		result = result[:insertAt] + marker + result[insertAt:]
	}

	if len(order) == 0 {
		return result
	}

	var b strings.Builder
	b.WriteString(result)
	b.WriteString("\n\n**Sources**\n")
	for _, url := range order {
		c := sources[url]
		title := c.Title
		if title == "" {
			title = url
		}
		b.WriteString(strconv.Itoa(numbers[url]))
		b.WriteString(". [")
		b.WriteString(title)
		b.WriteString("](")
		b.WriteString(url)
		b.WriteString(")\n")
	}
	return b.String()
}

type Agent interface {
	Generate(ctx context.Context, req GenerateRequest) (GenerateResponse, error)
}
