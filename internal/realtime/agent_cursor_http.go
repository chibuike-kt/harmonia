package realtime

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/user"
)

type agentCursorRequest struct {
	AgentID  uuid.UUID `json:"agent_id"`
	Path     string    `json:"path"`
	Line     int       `json:"line"`
	Column   int       `json:"column"`
	Active   bool      `json:"active"`
	Name     string    `json:"name"`
	Provider string    `json:"provider"`
}

// AgentCursorHandler returns the handler for
// POST /v1/rooms/{room_id}/agent_cursor — publishes a live agent cursor
// position (or, with active:false, an explicit "stopped editing"
// transition) onto the room's existing live channel. Mount it behind
// user.Authenticate; owner-checked like every other room-scoped endpoint
// in this package.
//
// Nothing in the IDE design overhaul's own tool-execution path calls
// this yet — an agent's real file-edit tool isn't wired in (that's
// docs/companion-ide-safety-brief.md's Batch 2). This exists now, real
// and tested, so the frontend's live-cursor rendering has a real signal
// to prove itself against today, the same "mechanism ready, real caller
// still pending" shape as IsHumanPresent and companionrelay.Dispatch
// before their own real callers existed.
func AgentCursorHandler(rooms *room.Store, hub Publisher) http.HandlerFunc {
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

		ctx := r.Context()
		rm, err := rooms.GetByID(ctx, roomID)
		if err != nil {
			if errors.Is(err, room.ErrNotFound) {
				writeError(w, http.StatusNotFound, "room not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to look up room")
			return
		}
		if rm.OwnerID == nil || *rm.OwnerID != u.ID {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}

		var body agentCursorRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if body.AgentID == uuid.Nil {
			writeError(w, http.StatusBadRequest, "agent_id is required")
			return
		}

		hub.Publish(roomID, NewAgentCursorMessage(AgentCursor{
			AgentID:  body.AgentID,
			RoomID:   roomID,
			Path:     body.Path,
			Line:     body.Line,
			Column:   body.Column,
			Active:   body.Active,
			Name:     body.Name,
			Provider: body.Provider,
		}))
		w.WriteHeader(http.StatusNoContent)
	}
}
