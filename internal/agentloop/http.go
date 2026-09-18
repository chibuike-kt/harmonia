package agentloop

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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// statusResponse is the wire shape every agent_loop HTTP response uses —
// the same fields realtime.AgentLoopStatus publishes live, so the
// frontend's initial fetch (on page load, or after a refresh mid-session)
// and its live SSE updates render from one shape, not two.
type statusResponse struct {
	SessionID    uuid.UUID `json:"session_id"`
	Task         string    `json:"task"`
	Cycle        int       `json:"cycle"`
	MaxCycles    int       `json:"max_cycles"`
	SpendUSD     float64   `json:"spend_usd"`
	DollarCapUSD float64   `json:"dollar_cap_usd"`
	State        string    `json:"state"`
	Message      string    `json:"message,omitempty"`
}

func toStatusResponse(s *Session) statusResponse {
	snap := s.snapshot()
	return statusResponse{
		SessionID: s.ID, Task: s.Task, Cycle: snap.Cycle, MaxCycles: s.Bounds.MaxCycles,
		SpendUSD: snap.SpendUSD, DollarCapUSD: s.Bounds.DollarCapUSD,
		State: string(snap.State), Message: snap.Message,
	}
}

// loadOwnedRoom fetches room_id from the URL and checks the authenticated
// human owns it — the same 404-then-403 pattern every other room-scoped
// route in this API uses (see actionproposal.loadOwnedProposal).
func loadOwnedRoom(w http.ResponseWriter, r *http.Request, rooms *room.Store) (room.Room, uuid.UUID, bool) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return room.Room{}, uuid.Nil, false
	}
	roomID, err := uuid.Parse(chi.URLParam(r, "room_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid room_id")
		return room.Room{}, uuid.Nil, false
	}
	rm, err := rooms.GetByID(r.Context(), roomID)
	if errors.Is(err, room.ErrNotFound) {
		writeError(w, http.StatusNotFound, "room not found")
		return room.Room{}, uuid.Nil, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to look up room")
		return room.Room{}, uuid.Nil, false
	}
	if rm.OwnerID == nil || *rm.OwnerID != u.ID {
		writeError(w, http.StatusForbidden, "forbidden")
		return room.Room{}, uuid.Nil, false
	}
	return rm, roomID, true
}

type startRequest struct {
	AgentID          uuid.UUID `json:"agent_id"`
	Task             string    `json:"task"`
	MaxCycles        int       `json:"max_cycles"`
	DollarCapUSD     float64   `json:"dollar_cap_usd"`
	WallClockSeconds int       `json:"wall_clock_seconds"`
}

// StartHandler returns the handler for POST
// /v1/rooms/{room_id}/agent_loop/start. Mount it behind user.Authenticate
// — a human starts a sustained session, never an agent, and ADR-011
// requires all three real bounds to already be set by the time this call
// is made (the IDE's own start form is what enforces that client-side;
// Bounds.Validate enforces it again here regardless of what the client
// sent).
func (m *Manager) StartHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, roomID, ok := loadOwnedRoom(w, r, m.rooms)
		if !ok {
			return
		}

		var req startRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		s, err := m.Start(r.Context(), StartParams{
			RoomID: roomID, AgentID: req.AgentID, Task: req.Task,
			MaxCycles: req.MaxCycles, DollarCapUSD: req.DollarCapUSD, WallClockSeconds: req.WallClockSeconds,
		})
		if errors.Is(err, ErrSessionAlreadyRunning) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		writeJSON(w, http.StatusCreated, toStatusResponse(s))
	}
}

// StopHandler returns the handler for POST
// /v1/rooms/{room_id}/agent_loop/{session_id}/stop — ADR-011's real,
// always-visible Stop control. Mount it behind user.Authenticate.
func (m *Manager) StopHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _, ok := loadOwnedRoom(w, r, m.rooms)
		if !ok {
			return
		}
		sessionID, err := uuid.Parse(chi.URLParam(r, "session_id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid session_id")
			return
		}
		if err := m.Stop(sessionID); errors.Is(err, ErrSessionNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// StatusHandler returns the handler for GET
// /v1/rooms/{room_id}/agent_loop — lets a freshly (re)loaded IDE page
// discover a session that's already running in this room, since the live
// agent_loop_status SSE event alone only reaches a tab that was already
// connected when it was published. Returns 204 when nothing is running.
func (m *Manager) StatusHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, roomID, ok := loadOwnedRoom(w, r, m.rooms)
		if !ok {
			return
		}
		s, ok := m.Get(roomID)
		if !ok {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusOK, toStatusResponse(s))
	}
}
