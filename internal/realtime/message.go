// Package realtime implements Phase 3's in-process fan-out hub — one Go
// process, one Hub, subscribers keyed by room ID. See
// docs/adr/ADR-003-realtime-and-frontend-foundations.md for why this is
// an in-process hub and not Redis Pub/Sub, and why SSE rather than
// WebSocket.
package realtime

import (
	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/protocol"
)

// Kind tags which of Message's two payloads is set.
type Kind string

const (
	KindEvent    Kind = "event"
	KindPresence Kind = "presence"
)

// Message is what flows through the Hub — a tagged union of the two
// distinct things ADR-003 says can flow through it: a recorded event (the
// exact protocol.Envelope already built for event.Store.Record) or an
// ephemeral agent presence transition that never touches the events
// table. Exactly one of Event/Presence is populated, matching Kind — a
// single tagged type rather than two unrelated ones, so a subscriber has
// one channel type to read regardless of which kind arrives.
type Message struct {
	Kind     Kind               `json:"kind"`
	Event    *protocol.Envelope `json:"event,omitempty"`
	Presence *Presence          `json:"presence,omitempty"`
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
