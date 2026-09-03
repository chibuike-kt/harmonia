// Package protocol defines AACP (Agent-to-Agent Communication Protocol) v0.
//
// v0 intentionally implements only the seven operations Milestone 1 needs.
// The envelope shape matches the full protocol design exactly, so the
// remaining operations (HANDOFF.REJECT, DECISION.*, ARTIFACT.*, etc.) drop
// in later without a schema break. See docs — Milestone 1 System Design,
// section 5.
package protocol

import (
	"time"

	"github.com/google/uuid"
)

// Operation is an AACP message type. Only the Milestone 1 subset is defined
// here; do not add operations speculatively — extend this list when the
// phase that needs them is actually being built.
type Operation string

const (
	OpTaskCreate       Operation = "TASK.CREATE"
	OpTaskClaim        Operation = "TASK.CLAIM"
	OpTaskComplete     Operation = "TASK.COMPLETE"
	OpContextRequest   Operation = "CONTEXT.REQUEST"
	OpContextResponse  Operation = "CONTEXT.RESPONSE"
	OpHandoffRequest   Operation = "HANDOFF.REQUEST"
	OpHandoffAccept    Operation = "HANDOFF.ACCEPT"
)

// ProtocolVersion is pre-stable. Bump deliberately; this is not tied to
// the Go module version.
const ProtocolVersion = "0.1"

// Participant identifies a message sender or recipient. Only AgentID is
// populated in Milestone 1 — room-wide broadcast (no recipient) is valid
// for presence-style messages later, not yet used.
type Participant struct {
	AgentID uuid.UUID `json:"agent_id"`
}

// Envelope is the common wrapper for every AACP message. Shape matches the
// full protocol design (see overview section 18) exactly.
type Envelope struct {
	ID              uuid.UUID       `json:"id"`
	ProtocolVersion string          `json:"version"`
	Type            Operation       `json:"type"`
	Timestamp       time.Time       `json:"timestamp"`

	RoomID uuid.UUID  `json:"room_id"`
	TaskID *uuid.UUID `json:"task_id,omitempty"`

	Sender    Participant  `json:"sender"`
	Recipient *Participant `json:"recipient,omitempty"`

	Payload map[string]any `json:"payload"`
}

// NewEnvelope builds an Envelope with ID, version, and timestamp populated,
// so call sites can't forget them or drift on format.
func NewEnvelope(roomID uuid.UUID, op Operation, sender Participant, payload map[string]any) Envelope {
	return Envelope{
		ID:              uuid.New(),
		ProtocolVersion: ProtocolVersion,
		Type:            op,
		Timestamp:       time.Now().UTC(),
		RoomID:          roomID,
		Sender:          sender,
		Payload:         payload,
	}
}
