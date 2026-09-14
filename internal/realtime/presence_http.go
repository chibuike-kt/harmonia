package realtime

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/user"
)

// humanPresenceHeartbeatTTL bounds how long a single heartbeat keeps a
// room "present" without another one arriving. Short enough that a
// closed tab or a browser that crashed mid-session stops satisfying
// ADR-010's presence gate within a couple of missed intervals, not many
// seconds later — the frontend side of this contract is expected to send
// a heartbeat well inside this window (e.g. every 5s) while the IDE page
// is open, the same "heartbeat faster than the TTL" shape the SSE
// stream's own heartbeatInterval already establishes for connection
// liveness in http.go.
const humanPresenceHeartbeatTTL = 15 * time.Second

// HumanPresenceHeartbeatHandler returns the handler for
// POST /v1/rooms/{room_id}/presence/heartbeat. Mount it behind
// user.Authenticate — like StreamHandler, it also checks the
// authenticated user owns the room (403 on mismatch, 404 if the room
// doesn't exist), the same ownership check every other room-scoped
// endpoint in this package applies.
//
// A human's browser tab calls this repeatedly, on an interval well
// inside humanPresenceHeartbeatTTL, for as long as that room's IDE
// session is genuinely open and being watched — this is the write side
// of the presence gate IsHumanPresent reads.
func HumanPresenceHeartbeatHandler(rooms *room.Store, rdb *redis.Client) http.HandlerFunc {
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

		if err := SetHumanPresent(ctx, rdb, roomID, humanPresenceHeartbeatTTL); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to record presence")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// HumanPresenceLeaveHandler returns the handler for
// DELETE /v1/rooms/{room_id}/presence/heartbeat — the best-effort
// explicit "I'm leaving" signal (see ClearHumanPresent's own doc comment
// on why this is a courtesy, not the real guarantee).
func HumanPresenceLeaveHandler(rooms *room.Store, rdb *redis.Client) http.HandlerFunc {
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

		if err := ClearHumanPresent(ctx, rdb, roomID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to clear presence")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
