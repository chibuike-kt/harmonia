package actionproposal

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/event"
	"github.com/chibuike-kt/harmonia/internal/handoff"
	"github.com/chibuike-kt/harmonia/internal/protocol"
	"github.com/chibuike-kt/harmonia/internal/store"
)

// executeApprovedHandoff runs a request_handoff proposal's stored payload
// against the real handoff.Store.Request path, in tx — called only from
// ApproveHandler, only on a proposal that just atomically transitioned
// pending -> approved. This is what makes ADR-006's "approving it
// produces a real handoff exactly as if a human had requested it" true:
// the exact same Request call, the exact same HANDOFF_REQUESTED event, as
// internal/handoff.RequestHandler's own direct-API path — not a second,
// parallel way of creating a handoff. Lives here rather than in
// internal/message (which is what actually builds the proposal's
// payload) specifically to avoid that package importing this one for
// creation and this one importing that one back for execution.
//
// Per ADR-006's 2026-09-14 addendum, the handoff is also immediately
// accepted in this same transaction — a human's approval of the proposal
// is the one judgment call this system asks for; requiring a second,
// separate accept from the receiving agent (with nothing that ever
// actually triggers it) was a dead end, not a real second gate. Both
// legs are recorded as real, distinct events (HANDOFF_REQUESTED then
// HANDOFF_ACCEPTED) — the true, honest history of what happened, not a
// single collapsed event — so the room's timeline and audit trail read
// exactly as they would if a receiving agent had really accepted a split
// second later, and the frontend's existing handling of both event types
// needs no changes.
func executeApprovedHandoff(ctx context.Context, tx store.Tx, roomID, proposingAgentID uuid.UUID, payload map[string]any) (requested, accepted protocol.Envelope, err error) {
	taskID, err := uuid.Parse(fmt.Sprint(payload["task_id"]))
	if err != nil {
		return protocol.Envelope{}, protocol.Envelope{}, fmt.Errorf("actionproposal: stored task_id %v is not a valid uuid: %w", payload["task_id"], err)
	}
	toAgentID, err := uuid.Parse(fmt.Sprint(payload["to_agent_id"]))
	if err != nil {
		return protocol.Envelope{}, protocol.Envelope{}, fmt.Errorf("actionproposal: stored to_agent_id %v is not a valid uuid: %w", payload["to_agent_id"], err)
	}
	summary, _ := payload["summary"].(string)

	txHandoffs := handoff.NewStore(tx)
	h, err := txHandoffs.Request(ctx, handoff.Handoff{
		RoomID:      roomID,
		TaskID:      taskID,
		FromAgentID: proposingAgentID,
		ToAgentID:   toAgentID,
		Summary:     summary,
		Completed:   toStringSlice(payload["completed"]),
		Remaining:   toStringSlice(payload["remaining"]),
		Risks:       toStringSlice(payload["risks"]),
	})
	if err != nil {
		return protocol.Envelope{}, protocol.Envelope{}, err
	}

	requestedEnv := protocol.NewEnvelope(roomID, protocol.OpHandoffRequest, protocol.Participant{AgentID: proposingAgentID}, map[string]any{
		"summary":     h.Summary,
		"completed":   h.Completed,
		"remaining":   h.Remaining,
		"artifacts":   h.Artifacts,
		"decisions":   h.Decisions,
		"risks":       h.Risks,
		"to_agent_id": h.ToAgentID.String(),
	})
	requestedEnv.TaskID = &taskID

	txEvents := event.NewStore(tx)
	if err := txEvents.Record(ctx, roomID, &h.TaskID, &proposingAgentID, handoff.EventHandoffRequested, requestedEnv.Payload); err != nil {
		return protocol.Envelope{}, protocol.Envelope{}, err
	}

	if err := txHandoffs.Accept(ctx, h.ID); err != nil {
		return protocol.Envelope{}, protocol.Envelope{}, fmt.Errorf("actionproposal: auto-accept handoff %s: %w", h.ID, err)
	}

	// Participant is the receiving agent, not the human who approved —
	// same as a real accept, this event represents that agent's own
	// acceptance of the work, just auto-triggered by the human's consent
	// rather than a separate call the agent itself made.
	acceptedEnv := protocol.NewEnvelope(roomID, protocol.OpHandoffAccept, protocol.Participant{AgentID: toAgentID}, map[string]any{})

	if err := txEvents.Record(ctx, roomID, &h.TaskID, &toAgentID, handoff.EventHandoffAccepted, acceptedEnv.Payload); err != nil {
		return protocol.Envelope{}, protocol.Envelope{}, err
	}

	return requestedEnv, acceptedEnv, nil
}

// toStringSlice converts a jsonb-decoded []any (pgx's own decoding of a
// stored JSON array) into []string, skipping anything that isn't
// actually a string rather than failing the whole execution over one bad
// element.
func toStringSlice(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
