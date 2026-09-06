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
	KindEvent    Kind = "event"
	KindPresence Kind = "presence"
	KindMessage  Kind = "message"
)

// Message is what flows through the Hub — a tagged union of the distinct
// things that can flow through it: a recorded event (the exact
// protocol.Envelope already built for event.Store.Record), an ephemeral
// agent presence transition that never touches the events table, or a
// chat message (ADR-004). Exactly one of Event/Presence/Message is
// populated, matching Kind — a single tagged type rather than separate
// ones, so a subscriber has one channel type to read regardless of which
// kind arrives.
type Message struct {
	Kind     Kind               `json:"kind"`
	Event    *protocol.Envelope `json:"event,omitempty"`
	Presence *Presence          `json:"presence,omitempty"`
	Message  *ChatMessage       `json:"message,omitempty"`
}

// ChatMessage mirrors internal/message.Message's wire shape. Defined
// here rather than imported: internal/message needs realtime.Publisher
// to publish a message it just wrote, so realtime importing back from
// internal/message would be a cycle — the same reasoning KindPresence's
// own Presence struct (not agent.Agent) already follows in this file.
type ChatMessage struct {
	ID               uuid.UUID  `json:"id"`
	RoomID           uuid.UUID  `json:"room_id"`
	SenderKind       string     `json:"sender_kind"`
	UserID           *uuid.UUID `json:"user_id,omitempty"`
	AgentID          *uuid.UUID `json:"agent_id,omitempty"`
	MentionedAgentID *uuid.UUID `json:"mentioned_agent_id,omitempty"`
	ReplyToMessageID *uuid.UUID `json:"reply_to_message_id,omitempty"`
	Content          string     `json:"content"`
	CreatedAt        time.Time  `json:"created_at"`
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
