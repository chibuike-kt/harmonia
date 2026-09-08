package actionproposal

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

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

// ApproveHandler returns the handler for POST
// /v1/action_proposals/{id}/approve. Mount it behind user.Authenticate —
// a human approves, never an agent. Resolving pending -> approved is
// atomic (Store.Resolve's own conditional WHERE), so two concurrent
// approve/reject calls can't both succeed. Approving a request_handoff
// proposal executes it for real, in the same transaction, against the
// exact same path a human's own direct API call would use
// (executeApprovedHandoff) — ADR-006's "approving it produces a real
// handoff exactly as if a human had requested it," not a second,
// parallel way of creating one.
func (s *Store) ApproveHandler(rooms *room.Store, pool store.Beginner, hub realtime.Publisher) http.HandlerFunc {
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
		resolved, err := txProposals.Resolve(ctx, p.ID, StatusApproved)
		if errors.Is(err, ErrNotPending) {
			writeError(w, http.StatusConflict, "proposal already resolved")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to approve proposal")
			return
		}

		var handoffEnv *protocol.Envelope
		if resolved.ActionType == ActionRequestHandoff {
			env, err := executeApprovedHandoff(ctx, tx, resolved.RoomID, resolved.ProposingAgentID, resolved.Payload)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to execute approved handoff")
				return
			}
			handoffEnv = &env
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
		if handoffEnv != nil {
			hub.Publish(resolved.RoomID, realtime.NewEventMessage(*handoffEnv))
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
