package message

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/realtime"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/store"
	"github.com/chibuike-kt/harmonia/internal/user"
)

type createRequest struct {
	Content          string     `json:"content"`
	MentionedAgentID *uuid.UUID `json:"mentioned_agent_id,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// writeError writes a JSON error body, {"error": "..."} — the shape
// every handler in this API uses.
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// CreateHandler returns the handler for POST /v1/rooms/{room_id}/messages.
// Mount it behind user.Authenticate — a human posts here, never an
// agent. Ownership is checked the same 404-then-403 way as every other
// room-scoped route (see room.UpdateHandler); a mentioned_agent_id gets
// the same non-leaking treatment: an agent that doesn't exist, or exists
// in a different room, both 404 identically, so a caller learns nothing
// about an agent it can't reach.
//
// The message write is transactional and published only after commit —
// the same pattern every other write in this API follows — even though
// today it's a single insert with no accompanying event record: chat
// messages are their own first-class grammar (ADR-004), not folded into
// the events audit trail. If a mention is present, the reply is
// triggered asynchronously after the human message has committed and
// published: this handler returns as soon as the human message is
// durable, never blocking on a live provider call (ADR-004).
func (s *Store) CreateHandler(rooms *room.Store, agents *agent.Store, pool store.Beginner, hub realtime.Publisher, orch *Orchestrator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := user.FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		roomID, err := uuid.Parse(chi.URLParam(r, "room_id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid room_id")
			return
		}

		var req createRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if strings.TrimSpace(req.Content) == "" {
			writeError(w, http.StatusBadRequest, "content is required")
			return
		}

		ctx := r.Context()
		rm, err := rooms.GetByID(ctx, roomID)
		if errors.Is(err, room.ErrNotFound) {
			writeError(w, http.StatusNotFound, "room not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to look up room")
			return
		}
		if rm.OwnerID == nil || *rm.OwnerID != u.ID {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}

		var mentioned *agent.Agent
		if req.MentionedAgentID != nil {
			a, err := agents.GetByID(ctx, *req.MentionedAgentID)
			if errors.Is(err, agent.ErrNotFound) {
				writeError(w, http.StatusNotFound, "mentioned agent not found")
				return
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to look up mentioned agent")
				return
			}
			if a.RoomID != roomID {
				// An agent that exists, just not in this room, doesn't
				// exist as far as this request is concerned — same
				// "don't leak cross-room existence" reasoning as
				// task.ClaimHandler's own room check.
				writeError(w, http.StatusNotFound, "mentioned agent not found")
				return
			}
			mentioned = &a
		}

		tx, rollback, err := store.BeginTx(ctx, pool)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to start transaction")
			return
		}
		defer rollback()

		txMessages := NewStore(tx)
		m, err := txMessages.CreateHuman(ctx, roomID, u.ID, req.Content, req.MentionedAgentID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create message")
			return
		}

		if err := tx.Commit(ctx); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to commit transaction")
			return
		}
		hub.Publish(roomID, realtime.NewChatMessage(toChatMessage(m)))

		if mentioned != nil {
			orch.TriggerReply(mentioned.ID, rm.OwnerID, m)
		}

		writeJSON(w, http.StatusCreated, m)
	}
}
