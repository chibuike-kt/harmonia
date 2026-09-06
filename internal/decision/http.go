package decision

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/message"
	"github.com/chibuike-kt/harmonia/internal/room"
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

// PinHandler returns the handler for POST
// /v1/rooms/{room_id}/messages/{message_id}/decisions — a human manually
// marking one message as a decision worth surfacing in the room info
// panel (see package decision's own doc comment: no automatic
// detection). Mount behind user.Authenticate — a human pins, never an
// agent. Ownership is checked the same 404-then-403 way as every other
// room-scoped route; the message must actually belong to this room,
// the same non-leaking 404 pattern message.CreateHandler already uses
// for mentioned_agent_id.
func (s *Store) PinHandler(rooms *room.Store, messages *message.Store) http.HandlerFunc {
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
		messageID, err := uuid.Parse(chi.URLParam(r, "message_id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid message_id")
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

		m, err := messages.GetByID(ctx, messageID)
		if errors.Is(err, message.ErrNotFound) {
			writeError(w, http.StatusNotFound, "message not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to look up message")
			return
		}
		if m.RoomID != roomID {
			// A message that exists, just not in this room, doesn't
			// exist as far as this request is concerned — same
			// "don't leak cross-room existence" reasoning as
			// message.CreateHandler's own mentioned-agent check.
			writeError(w, http.StatusNotFound, "message not found")
			return
		}

		d, err := s.Pin(ctx, roomID, messageID, m.Content)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to pin decision")
			return
		}

		writeJSON(w, http.StatusCreated, d)
	}
}

// ListByRoomHandler returns the handler for GET
// /v1/rooms/{room_id}/decisions — the room info panel's decisions
// section. Mount behind user.Authenticate; ownership checked the same
// way as PinHandler.
func (s *Store) ListByRoomHandler(rooms *room.Store) http.HandlerFunc {
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

		decisions, err := s.ListByRoom(ctx, roomID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list decisions")
			return
		}
		if decisions == nil {
			decisions = []Decision{}
		}

		writeJSON(w, http.StatusOK, decisions)
	}
}
