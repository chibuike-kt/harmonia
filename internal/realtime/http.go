package realtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/event"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/user"
)

type errorResponse struct {
	Error string `json:"error"`
}

// writeError writes a JSON error body, {"error": "..."} — the shape
// every handler in this API uses. Only reachable before the stream
// itself starts (headers not yet written); once streaming begins, a
// failure just ends the connection, the same as any other SSE server.
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: message})
}

// heartbeatInterval bounds how long an idle stream (no hub publishes) can
// go without the server attempting a write. A write is how a dead TCP
// connection actually gets discovered on this side — see StreamHandler's
// doc comment on the two disconnect paths this exists to cover.
const heartbeatInterval = 15 * time.Second

// agentPresence pairs an agent id with its current status, for the
// stream's initial snapshot.
type agentPresence struct {
	AgentID uuid.UUID `json:"agent_id"`
	Status  string    `json:"status"`
}

type snapshot struct {
	Events   []event.Event   `json:"events"`
	Presence []agentPresence `json:"presence"`
}

// StreamHandler returns the handler for GET /v1/rooms/{room_id}/stream.
// Mount it behind user.Authenticate — it also checks the authenticated
// user owns the room (403 on mismatch, 404 if the room doesn't exist at
// all), the same pattern agent.RegisterHandler uses for room ownership.
//
// On connect it subscribes to hub first, then writes one "snapshot"
// event (recent room events from Postgres, current agent presence read
// from each agent's Redis key), then streams every further Message the
// hub publishes for this room as its own SSE event, until the request
// context is done.
//
// Disconnect handling: a client-initiated close (tab closed, EventSource
// explicitly closed, navigating away) sends a real TCP close, which
// Go's net/http server detects via a background read on the connection
// and cancels ctx promptly — that path is reliable and is what the
// leak test exercises. A genuinely silent drop (network blip with no
// FIN/RST ever sent, e.g. a laptop going straight to sleep) is not
// something ctx.Done() is guaranteed to catch quickly on its own: TCP
// itself won't notice until something tries to use the dead connection.
// The heartbeat ticker below exists for exactly that gap — it forces a
// write attempt at least every heartbeatInterval even on an idle room,
// so a dead connection gets discovered by a failed write within one
// interval instead of waiting on an OS-level TCP timeout that can be
// far longer. It narrows the gap; it doesn't eliminate it — a fully
// robust bound on that needs an application-level ping/pong (WebSocket),
// which ADR-003 explicitly defers.
func StreamHandler(rooms *room.Store, agents *agent.Store, events *event.Store, hub *Hub, rdb *redis.Client) http.HandlerFunc {
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

		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "streaming not supported")
			return
		}

		// Subscribe before reading anything for the snapshot: a message
		// published between the snapshot read and Subscribe would
		// otherwise be silently missed. Subscribing first means the worst
		// case is the opposite, safer overlap — an event appearing in
		// both the snapshot and as a live message — not a dropped one.
		sub, unsubscribe := hub.Subscribe(roomID)
		defer unsubscribe()

		roomAgents, err := agents.ListByRoom(ctx, roomID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list agents")
			return
		}
		presence := make([]agentPresence, 0, len(roomAgents))
		for _, a := range roomAgents {
			status, err := GetPresence(ctx, rdb, a.ID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to read agent presence")
				return
			}
			if status == "" {
				// Nothing mirrored yet (never transitioned, or the key
				// expired) — the durable status column is still a fair
				// answer for a snapshot.
				status = string(a.Status)
			}
			presence = append(presence, agentPresence{AgentID: a.ID, Status: status})
		}

		recentEvents, err := events.ListByRoom(ctx, roomID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list events")
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		if !writeSSEEvent(w, flusher, "snapshot", snapshot{Events: recentEvents, Presence: presence}) {
			return
		}

		heartbeat := time.NewTicker(heartbeatInterval)
		defer heartbeat.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case msg, open := <-sub:
				if !open {
					return
				}
				if !writeSSEEvent(w, flusher, string(msg.Kind), msg) {
					return
				}
			case <-heartbeat.C:
				if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
}

// writeSSEEvent writes one SSE "event: ...\ndata: ...\n\n" frame and
// flushes it. Reports whether the write succeeded — false means the
// connection is gone and the caller should stop.
func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, eventName string, data any) bool {
	payload, err := json.Marshal(data)
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventName, payload); err != nil {
		return false
	}
	flusher.Flush()
	return true
}
