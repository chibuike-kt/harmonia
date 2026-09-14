package companionrelay

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/user"
)

type errorResponse struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: message})
}

type resultRequest struct {
	Output string `json:"output"`
	Err    string `json:"err"`
}

// ResultHandler returns the handler for
// POST /v1/rooms/{room_id}/companion_actions/{action_id}/result — the
// frontend's own end of the relay protocol, called once the companion
// has actually finished (or failed) the action it forwarded. Mount it
// behind user.Authenticate; like every other room-scoped endpoint in
// this codebase, it also checks the authenticated user owns the room.
//
// A 404/410-shaped response (via ErrUnknownAction) for an action ID this
// Coordinator isn't waiting on anymore is the expected, correct outcome
// for a result that arrives after Dispatch already timed out — not a
// bug to retry.
func ResultHandler(rooms *room.Store, c *Coordinator) http.HandlerFunc {
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
		actionID, err := uuid.Parse(chi.URLParam(r, "action_id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid action_id")
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

		var body resultRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		if err := c.Resolve(actionID, Result{Output: body.Output, Err: body.Err}); err != nil {
			if errors.Is(err, ErrUnknownAction) {
				writeError(w, http.StatusGone, "action is no longer awaiting a result")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to resolve action")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
