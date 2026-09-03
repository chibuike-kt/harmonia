package event

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type errorResponse struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: message})
}

// ListByRoomHandler returns the handler for GET /v1/rooms/{id}/events.
// Room-scoping is entirely the caller's job — mount this behind
// agent.Authenticate and agent.RequireRoom("id"), not re-checked here.
func (s *Store) ListByRoomHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		roomID, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid room id")
			return
		}

		events, err := s.ListByRoom(r.Context(), roomID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list events")
			return
		}
		if events == nil {
			events = []Event{}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(events)
	}
}
