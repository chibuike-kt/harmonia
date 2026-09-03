package contextengine

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/event"
	"github.com/chibuike-kt/harmonia/internal/protocol"
)

// EventContextRequested is the events.type value for a CONTEXT.REQUEST —
// distinct from protocol.OpContextRequest, the wire message type.
const EventContextRequested = "CONTEXT_REQUESTED"

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

// TaskHandler returns the handler for GET /v1/context/tasks/{task_id}. It
// records CONTEXT_REQUESTED — the request only. The response is not
// itself a stored event: the events table has no column for it today, and
// adding one is a schema change, not something to do silently under a
// step scoped to wiring an HTTP handler.
func (s *Store) TaskHandler(events *event.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, ok := agent.FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		taskID, err := uuid.Parse(chi.URLParam(r, "task_id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid task id")
			return
		}

		ctx := r.Context()
		result, err := s.TaskByID(ctx, taskID)
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "task not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load task")
			return
		}
		// A task in another room doesn't exist as far as this agent is
		// concerned — same reasoning as every other cross-room lookup here.
		if result.RoomID != a.RoomID {
			writeError(w, http.StatusNotFound, "task not found")
			return
		}

		env := protocol.NewEnvelope(result.RoomID, protocol.OpContextRequest, protocol.Participant{AgentID: a.ID}, map[string]any{
			"task_id": result.TaskID.String(),
		})
		if err := events.Record(ctx, result.RoomID, &taskID, &a.ID, EventContextRequested, env.Payload); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to record event")
			return
		}

		writeJSON(w, http.StatusOK, result)
	}
}
