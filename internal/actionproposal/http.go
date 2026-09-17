package actionproposal

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/companionrelay"
	"github.com/chibuike-kt/harmonia/internal/event"
	"github.com/chibuike-kt/harmonia/internal/protocol"
	"github.com/chibuike-kt/harmonia/internal/realtime"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/store"
	"github.com/chibuike-kt/harmonia/internal/user"
)

type errorResponse struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// loadOwnedProposal fetches id and checks the authenticated human owns
// its room — the same 404-then-403 pattern every other room-scoped route
// in this API uses (see decision.PinHandler), just reached via the
// proposal's own room_id rather than a room_id path segment: unlike a
// message or a decision, a proposal is meaningful entirely on its own
// (there's exactly one human who could ever approve it), so there's no
// reason to also require room_id in the URL.
func loadOwnedProposal(w http.ResponseWriter, r *http.Request, proposals *Store, rooms *room.Store) (Proposal, bool) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return Proposal{}, false
	}

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return Proposal{}, false
	}

	ctx := r.Context()
	p, err := proposals.GetByID(ctx, id)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "proposal not found")
		return Proposal{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to look up proposal")
		return Proposal{}, false
	}

	rm, err := rooms.GetByID(ctx, p.RoomID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to look up room")
		return Proposal{}, false
	}
	if rm.OwnerID == nil || *rm.OwnerID != u.ID {
		writeError(w, http.StatusForbidden, "forbidden")
		return Proposal{}, false
	}
	return p, true
}

// resolvedEnvelope builds the ACTION.RESOLVE envelope shared by
// ApproveHandler and RejectHandler — identical shape, only the resulting
// status differs.
func resolvedEnvelope(p Proposal) protocol.Envelope {
	return protocol.NewEnvelope(p.RoomID, protocol.OpActionResolve, protocol.Participant{AgentID: p.ProposingAgentID}, map[string]any{
		"proposal_id": p.ID.String(),
		"action_type": string(p.ActionType),
		"status":      string(p.Status),
	})
}

// approveRequest is the optional body POST .../approve accepts —
// meaningful only for a propose_file_edit proposal. content_base64,
// when present, is what actually gets written instead of the proposal's
// own stored new_content — see executeApprovedFileEdit's own doc
// comment on why this (not a second endpoint) is what makes per-hunk
// resolution real: a human accepting some hunks and rejecting others
// sends the real merged result it computed, not an all-or-nothing pick
// of the two stored versions. An empty or absent body is Accept All.
type approveRequest struct {
	ContentBase64 *string `json:"content_base64,omitempty"`
}

// GetHandler returns the handler for GET /v1/action_proposals/{id}.
// Mount it behind user.Authenticate. The ACTION.PROPOSE event a client
// sees over the live channel deliberately carries only a summary (id,
// action_type, and for a file edit, the path) — the full payload (a
// file edit's real old/new content) is what this fetches on demand, the
// same "event announces, a real fetch gets the full content" split the
// rest of this API already uses for anything bigger than a summary
// belongs in a live push.
func (s *Store) GetHandler(rooms *room.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := loadOwnedProposal(w, r, s, rooms)
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

// ApproveHandler returns the handler for POST
// /v1/action_proposals/{id}/approve. Mount it behind user.Authenticate —
// a human approves, never an agent. Resolving pending -> approved is
// atomic (Store.Resolve's own conditional WHERE), so two concurrent
// approve/reject calls can't both succeed. Approving a request_handoff
// proposal executes it for real, in the same transaction, against the
// exact same path a human's own direct API call would use
// (executeApprovedHandoff) — ADR-006's "approving it produces a real
// handoff exactly as if a human had requested it," not a second,
// parallel way of creating one. Per the ADR's 2026-09-14 addendum, that
// execution also immediately accepts the handoff in the same
// transaction — a human's approval here is the whole transaction's one
// required consent, not the first of two separate gates.
//
// Approving a propose_file_edit proposal is different in kind, not just
// in payload: the real write isn't a database operation the transaction
// can perform at all, it's a live round trip through the reviewing
// human's own browser tab and companion (ADR-010's relay protocol) — see
// executeApprovedFileEdit's own doc comment on why that happens outside
// the transaction that resolved the proposal, after it has already
// committed. A relay failure (the tab closed mid-review, the write timed
// out) is reported for real rather than silently swallowed, but doesn't
// roll the approval itself back — a human's own decision stays durable
// regardless of whether the mechanical write happened to land.
func (s *Store) ApproveHandler(rooms *room.Store, pool store.Beginner, hub realtime.Publisher, relay *companionrelay.Coordinator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := loadOwnedProposal(w, r, s, rooms)
		if !ok {
			return
		}

		var body approveRequest
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}
		}

		ctx := r.Context()
		tx, rollback, err := store.BeginTx(ctx, pool)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to start transaction")
			return
		}
		defer rollback()

		txProposals := NewStore(tx)
		resolved, err := txProposals.Resolve(ctx, p.ID, StatusApproved)
		if errors.Is(err, ErrNotPending) {
			writeError(w, http.StatusConflict, "proposal already resolved")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to approve proposal")
			return
		}

		var handoffRequestedEnv, handoffAcceptedEnv *protocol.Envelope
		if resolved.ActionType == ActionRequestHandoff {
			requestedEnv, acceptedEnv, err := executeApprovedHandoff(ctx, tx, resolved.RoomID, resolved.ProposingAgentID, resolved.Payload)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to execute approved handoff")
				return
			}
			handoffRequestedEnv = &requestedEnv
			handoffAcceptedEnv = &acceptedEnv
		}

		resolveEnv := resolvedEnvelope(resolved)
		txEvents := event.NewStore(tx)
		if err := txEvents.Record(ctx, resolved.RoomID, nil, &resolved.ProposingAgentID, EventActionResolved, resolveEnv.Payload); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to record event")
			return
		}

		if err := tx.Commit(ctx); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to commit transaction")
			return
		}
		hub.Publish(resolved.RoomID, realtime.NewEventMessage(resolveEnv))
		if handoffRequestedEnv != nil {
			hub.Publish(resolved.RoomID, realtime.NewEventMessage(*handoffRequestedEnv))
		}
		if handoffAcceptedEnv != nil {
			hub.Publish(resolved.RoomID, realtime.NewEventMessage(*handoffAcceptedEnv))
		}

		if resolved.ActionType == ActionProposeFileEdit {
			if _, err := executeApprovedFileEdit(ctx, resolved.RoomID, resolved.Payload, body.ContentBase64, hub, relay); err != nil {
				// The approval itself already committed and published —
				// only the real write failed. Reported honestly as its
				// own distinct outcome, not masked as an approve failure.
				writeJSON(w, http.StatusBadGateway, map[string]any{
					"proposal": resolved,
					"error":    "approved, but the real write failed: " + err.Error(),
				})
				return
			}
		}

		writeJSON(w, http.StatusOK, resolved)
	}
}

// RejectHandler returns the handler for POST
// /v1/action_proposals/{id}/reject. Mount it behind user.Authenticate.
// Rejecting never executes anything — it only marks the proposal
// resolved, the same atomic pending -> rejected transition ApproveHandler
// uses for the opposite outcome.
func (s *Store) RejectHandler(rooms *room.Store, pool store.Beginner, hub realtime.Publisher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := loadOwnedProposal(w, r, s, rooms)
		if !ok {
			return
		}

		ctx := r.Context()
		tx, rollback, err := store.BeginTx(ctx, pool)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to start transaction")
			return
		}
		defer rollback()

		txProposals := NewStore(tx)
		resolved, err := txProposals.Resolve(ctx, p.ID, StatusRejected)
		if errors.Is(err, ErrNotPending) {
			writeError(w, http.StatusConflict, "proposal already resolved")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to reject proposal")
			return
		}

		env := resolvedEnvelope(resolved)
		txEvents := event.NewStore(tx)
		if err := txEvents.Record(ctx, resolved.RoomID, nil, &resolved.ProposingAgentID, EventActionResolved, env.Payload); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to record event")
			return
		}

		if err := tx.Commit(ctx); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to commit transaction")
			return
		}
		hub.Publish(resolved.RoomID, realtime.NewEventMessage(env))

		writeJSON(w, http.StatusOK, resolved)
	}
}
