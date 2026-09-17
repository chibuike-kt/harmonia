package actionproposal

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/companionrelay"
	"github.com/chibuike-kt/harmonia/internal/event"
	"github.com/chibuike-kt/harmonia/internal/protocol"
	"github.com/chibuike-kt/harmonia/internal/realtime"
	"github.com/chibuike-kt/harmonia/internal/store"
)

// fileEditDispatchTimeout bounds how long an approved file-edit
// proposal's real write waits for the reviewing human's own browser tab
// to relay it to the companion and confirm — the human is, by
// construction, looking at this exact diff right now (that's what
// approving it means), so this can be generous without risking a stuck
// request the way an unattended agent action might.
const fileEditDispatchTimeout = 20 * time.Second

// CreateFileEditProposal records a new propose_file_edit proposal and its
// real ACTION_PROPOSED event, then publishes it — the exact shape
// internal/message's own executeRequestHandoff uses for
// ActionRequestHandoff (create, record, commit, publish), so a client
// reconstructing pending approvals from the room's event stream needs no
// per-action-type special case.
//
// This is the real hook a future agent file-edit tool calls when
// ADR-010's presence gate finds nobody currently watching — direct
// writes stay presence-gated; this path exists specifically for when
// that gate says no. No such tool is wired in yet (that's Batch 2's own
// scope, same as this project's other agent-tool integrations), so
// today's only real caller is this function itself, called directly —
// real, tested, and ready for that tool to call the moment it exists.
func CreateFileEditProposal(ctx context.Context, pool store.Beginner, hub realtime.Publisher, roomID, agentID uuid.UUID, path, oldContentBase64, newContentBase64 string) (Proposal, error) {
	tx, rollback, err := store.BeginTx(ctx, pool)
	if err != nil {
		return Proposal{}, fmt.Errorf("actionproposal: begin tx for file-edit proposal: %w", err)
	}
	defer rollback()

	payload := map[string]any{
		"path":               path,
		"old_content_base64": oldContentBase64,
		"new_content_base64": newContentBase64,
	}

	txProposals := NewStore(tx)
	p, err := txProposals.Create(ctx, roomID, agentID, ActionProposeFileEdit, payload)
	if err != nil {
		return Proposal{}, fmt.Errorf("actionproposal: create file-edit proposal: %w", err)
	}

	env := protocol.NewEnvelope(roomID, protocol.OpActionPropose, protocol.Participant{AgentID: agentID}, map[string]any{
		"proposal_id": p.ID.String(),
		"action_type": string(p.ActionType),
		"path":        path,
	})

	txEvents := event.NewStore(tx)
	if err := txEvents.Record(ctx, roomID, nil, &agentID, EventActionProposed, env.Payload); err != nil {
		return Proposal{}, fmt.Errorf("actionproposal: record ACTION_PROPOSED for file-edit proposal: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Proposal{}, fmt.Errorf("actionproposal: commit file-edit proposal: %w", err)
	}
	hub.Publish(roomID, realtime.NewEventMessage(env))

	return p, nil
}

// executeApprovedFileEdit runs an approved propose_file_edit proposal's
// real write — called only from ApproveHandler, only on a proposal that
// just atomically transitioned pending -> approved. Deliberately outside
// the DB transaction that resolved the proposal: the write itself isn't
// a database operation at all, it's a real round trip through
// ADR-010's relay protocol (backend -> live channel -> the reviewing
// human's own already-open browser tab -> their companion -> the real
// filesystem), and holding a DB transaction open across a network round
// trip to a browser would be exactly the kind of long-held-lock mistake
// this codebase's own conditional-write discipline exists to avoid
// elsewhere.
//
// contentOverrideBase64, when non-nil, is what actually gets written
// instead of the proposal's own stored new_content — this is what makes
// per-hunk resolution real: a human accepting some hunks and rejecting
// others produces a merged result that is neither the stored "old" nor
// "new" content, computed client-side and sent as the real content to
// write. A nil override (Accept All) writes the proposal's own stored
// new_content_base64 unchanged.
func executeApprovedFileEdit(ctx context.Context, roomID uuid.UUID, payload map[string]any, contentOverrideBase64 *string, hub realtime.Publisher, relay *companionrelay.Coordinator) (companionrelay.Result, error) {
	path, _ := payload["path"].(string)
	if path == "" {
		return companionrelay.Result{}, fmt.Errorf("actionproposal: stored file-edit payload has no path")
	}
	content := contentOverrideBase64
	if content == nil {
		newContent, _ := payload["new_content_base64"].(string)
		content = &newContent
	}
	return companionrelay.Dispatch(ctx, relay, hub, roomID, "write_file", path, *content, fileEditDispatchTimeout)
}
