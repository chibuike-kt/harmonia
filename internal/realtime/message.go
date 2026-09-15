// Package realtime implements Phase 3's in-process fan-out hub — one Go
// process, one Hub, subscribers keyed by room ID. See
// docs/adr/ADR-003-realtime-and-frontend-foundations.md for why this is
// an in-process hub and not Redis Pub/Sub, and why SSE rather than
// WebSocket.
package realtime

import (
	"time"

	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/protocol"
)

// Kind tags which of Message's payloads is set.
type Kind string

const (
	KindEvent           Kind = "event"
	KindPresence        Kind = "presence"
	KindMessage         Kind = "message"
	KindRoom            Kind = "room"
	KindPickupUsage     Kind = "pickup_usage"
	KindObjective       Kind = "objective"
	KindCompanionAction Kind = "companion_action"
	KindAgentCursor     Kind = "agent_cursor"
)

// Message is what flows through the Hub — a tagged union of the distinct
// things that can flow through it: a recorded event (the exact
// protocol.Envelope already built for event.Store.Record), an ephemeral
// agent presence transition that never touches the events table, a chat
// message (ADR-004), or a room update (currently just the auto-generated
// title landing — ADR-004's nameless-room-creation addendum). Exactly
// one of Event/Presence/Message/Room is populated, matching Kind — a
// single tagged type rather than separate ones, so a subscriber has one
// channel type to read regardless of which kind arrives.
type Message struct {
	Kind            Kind                 `json:"kind"`
	Event           *protocol.Envelope   `json:"event,omitempty"`
	Presence        *Presence            `json:"presence,omitempty"`
	Message         *ChatMessage         `json:"message,omitempty"`
	Room            *RoomUpdate          `json:"room,omitempty"`
	PickupUsage     *PickupUsage         `json:"pickup_usage,omitempty"`
	Objective       *RoomObjectiveUpdate `json:"objective,omitempty"`
	CompanionAction *CompanionAction     `json:"companion_action,omitempty"`
	AgentCursor     *AgentCursor         `json:"agent_cursor,omitempty"`
}

// ChatMessage mirrors internal/message.Message's wire shape. Defined
// here rather than imported: internal/message needs realtime.Publisher
// to publish a message it just wrote, so realtime importing back from
// internal/message would be a cycle — the same reasoning KindPresence's
// own Presence struct (not agent.Agent) already follows in this file.
type ChatMessage struct {
	ID                uuid.UUID   `json:"id"`
	RoomID            uuid.UUID   `json:"room_id"`
	SenderKind        string      `json:"sender_kind"`
	UserID            *uuid.UUID  `json:"user_id,omitempty"`
	AgentID           *uuid.UUID  `json:"agent_id,omitempty"`
	MentionedAgentIDs []uuid.UUID `json:"mentioned_agent_ids,omitempty"`
	ReplyToMessageID  *uuid.UUID  `json:"reply_to_message_id,omitempty"`
	Content           string      `json:"content"`
	CreatedAt         time.Time   `json:"created_at"`
	// InputTokens/OutputTokens mirror internal/message.Message's own
	// fields — real usage from the provider response, nil for a human
	// message. Carried over SSE so the frontend's cost/token pill can
	// accumulate a running total without a separate fetch per message.
	InputTokens  *int `json:"input_tokens,omitempty"`
	OutputTokens *int `json:"output_tokens,omitempty"`
	// AttachmentFilename/AttachmentMimeType mirror internal/message.
	// Message's own fields of the same name — deliberately not the raw
	// attachment content (ADR-008 batch A): this is what lets the
	// frontend render a chip for an attached file without every fetch of
	// a room's history (this snapshot included) pulling potentially-
	// megabyte attachment bytes for messages nobody's asked to see
	// again. See message.Message.AttachmentContent's own doc comment.
	AttachmentFilename *string `json:"attachment_filename,omitempty"`
	AttachmentMimeType *string `json:"attachment_mime_type,omitempty"`
}

// RoomUpdate carries a room's new name — currently only ever published
// by the auto-title job once it successfully applies a generated title
// (internal/message.TitleGenerator), never by a manual rename (a PATCH
// already returns the new name synchronously to whoever made it; a
// live push isn't needed there the way it is for something that
// completes asynchronously in the background, possibly after the human
// has navigated away from the create flow and is just sitting in the
// room watching).
type RoomUpdate struct {
	RoomID uuid.UUID `json:"room_id"`
	Name   string    `json:"name"`
}

// RoomObjectiveUpdate carries a room's newly generated objective —
// published only by ObjectiveGenerator (internal/message/autoobjective.go)
// once it successfully applies one, the same "only the successful async
// outcome, never a manual edit" reasoning as RoomUpdate's own doc
// comment: a manual PATCH already returns the new objective synchronously
// to whoever made it, so only the async job's own eventual completion
// needs a live push to a room a human might already be sitting in. A
// separate Kind/type from RoomUpdate rather than reusing it — Name is a
// plain, always-populated string on that type, and a bare `Message{Kind:
// KindRoom, Room: &RoomUpdate{RoomID: id}}` (Name left as "") would read
// on the frontend as "the room was renamed to empty," not "no rename
// happened, this is an unrelated update."
type RoomObjectiveUpdate struct {
	RoomID    uuid.UUID `json:"room_id"`
	Objective string    `json:"objective"`
}

// PickupUsage is one phase-1 classification call's real token usage
// (ADR-007 batch B) — published live so a room that's already open
// reflects the cost pill's running total without waiting for a reload,
// the same reasoning ChatMessage's own InputTokens/OutputTokens serve
// for a real reply. Never persisted as a Message itself (see
// internal/message.Store.RecordPickupEvaluationUsage's own doc comment
// for why it's a dedicated ledger, not a messages row) — this is purely
// the live half of that same data, an incremental delta for whoever's
// already watching, not the source of truth (a fresh snapshot's own
// aggregate is).
type PickupUsage struct {
	RoomID       uuid.UUID `json:"room_id"`
	AgentID      uuid.UUID `json:"agent_id"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
}

// Presence is an agent's status transition. Ephemeral by design — never
// persisted to the append-only events table (see ADR-003) — mirrored
// instead into a short-lived Redis key for a client connecting mid-session.
type Presence struct {
	AgentID uuid.UUID `json:"agent_id"`
	Status  string    `json:"status"`
}

// NewEventMessage wraps a recorded event's envelope for publishing. Call
// this only after the transaction that recorded env has committed — see
// ADR-003 on why publishing before commit is unsafe.
func NewEventMessage(env protocol.Envelope) Message {
	return Message{Kind: KindEvent, Event: &env}
}

// NewPresenceMessage wraps an agent's status transition for publishing.
func NewPresenceMessage(agentID uuid.UUID, status string) Message {
	return Message{Kind: KindPresence, Presence: &Presence{AgentID: agentID, Status: status}}
}

// NewChatMessage wraps a chat message for publishing. Call this only
// after the transaction (or, for an agent's reply generated
// asynchronously, the plain insert) that stored msg has actually
// committed — same ordering rule as NewEventMessage.
func NewChatMessage(msg ChatMessage) Message {
	return Message{Kind: KindMessage, Message: &msg}
}

// NewRoomRenamedMessage wraps a room's new name for publishing. Call
// this only after the write that applied it has actually committed —
// same ordering rule as NewEventMessage.
func NewRoomRenamedMessage(roomID uuid.UUID, name string) Message {
	return Message{Kind: KindRoom, Room: &RoomUpdate{RoomID: roomID, Name: name}}
}

// NewRoomObjectiveMessage wraps a room's newly generated objective for
// publishing. Call this only after the write that applied it has
// actually committed — same ordering rule as NewEventMessage.
func NewRoomObjectiveMessage(roomID uuid.UUID, objective string) Message {
	return Message{Kind: KindObjective, Objective: &RoomObjectiveUpdate{RoomID: roomID, Objective: objective}}
}

// CompanionAction is one agent-initiated companion action (ADR-010's
// relay protocol) relayed from the backend to the one browser tab
// holding that room's live companion WebSocket. Actor is always "agent"
// on the wire — this Kind only ever carries agent-driven actions; a
// human typing directly into the terminal talks to the companion over
// its own already-open WebSocket and never touches the backend at all.
// Carrying Actor explicitly anyway (rather than leaving it implicit)
// means the frontend's terminal rendering — which must mark every
// agent-driven action visibly and unambiguously the instant it happens
// (ADR-010) — never has to infer whose action this is from which code
// path delivered it; it just reads the field.
type CompanionAction struct {
	ID     uuid.UUID `json:"id"`
	RoomID uuid.UUID `json:"room_id"`
	Type   string    `json:"type"`
	Data   string    `json:"data"`
	Actor  string    `json:"actor"`
}

// NewCompanionActionMessage wraps an agent-initiated companion action
// for publishing over the room's existing live channel — the hub/SSE
// stream every other Message kind already rides, per ADR-010's decision
// that a cloud backend can only reach a loopback-bound companion process
// through the browser tab that already has it open.
func NewCompanionActionMessage(action CompanionAction) Message {
	return Message{Kind: KindCompanionAction, CompanionAction: &action}
}

// AgentCursor is one agent's live edit position in one open file — the
// IDE design overhaul's live multiplayer-cursor signal (VS Code
// LiveShare/Google-Docs-style). Active false is the explicit "stopped
// editing" transition, published the instant an agent's file/shell tools
// stop being available (a human's presence lapsing, per
// realtime.IsHumanPresent, or the agent simply finishing) — the
// frontend must remove the cursor, its tag, and any live dots the
// moment this arrives, with no fade and no lingering frozen state; a
// stale "still editing" cursor after presence is gone is exactly the
// ambiguity the whole presence-gate model exists to prevent, and that
// has to be as true on screen as it is in the backend's own tool
// availability.
type AgentCursor struct {
	AgentID  uuid.UUID `json:"agent_id"`
	RoomID   uuid.UUID `json:"room_id"`
	Path     string    `json:"path"`
	Line     int       `json:"line"`
	Column   int       `json:"column"`
	Active   bool      `json:"active"`
	Name     string    `json:"name"`
	Provider string    `json:"provider,omitempty"`
}

// NewAgentCursorMessage wraps a live agent cursor position/transition for
// publishing over the room's existing live channel.
func NewAgentCursorMessage(cursor AgentCursor) Message {
	return Message{Kind: KindAgentCursor, AgentCursor: &cursor}
}

// NewPickupUsageMessage wraps one phase-1 classification call's real
// token usage for publishing. Call this only after
// RecordPickupEvaluationUsage's own insert has committed — same
// ordering rule as NewEventMessage.
func NewPickupUsageMessage(roomID, agentID uuid.UUID, inputTokens, outputTokens int) Message {
	return Message{Kind: KindPickupUsage, PickupUsage: &PickupUsage{
		RoomID: roomID, AgentID: agentID, InputTokens: inputTokens, OutputTokens: outputTokens,
	}}
}
