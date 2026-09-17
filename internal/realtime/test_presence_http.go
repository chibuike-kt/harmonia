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

// TEMPORARY, for live-testing follow mode's click-to-toggle gate only —
// mirrors AgentCursorHandler's own real, already-shipped "mechanism
// ready, real caller still pending" pattern (see that handler's own doc
// comment) for the one signal that has no such lever yet: presence
// status. Not meant to ship — remove once follow mode's live proof is
// captured.
type testPresenceRequest struct {
	AgentID uuid.UUID `json:"agent_id"`
	Status  string    `json:"status"`
}

func TestSetPresenceHandler(rooms *room.Store, hub Publisher) http.HandlerFunc {
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
		var body testPresenceRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		hub.Publish(roomID, NewPresenceMessage(body.AgentID, body.Status))
		w.WriteHeader(http.StatusNoContent)
	}
}
