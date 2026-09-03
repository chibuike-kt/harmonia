package agent

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/user"
)

type registerRequest struct {
	Name         string   `json:"name"`
	Provider     string   `json:"provider"`
	Capabilities []string `json:"capabilities"`
}

// registerResponse carries the plaintext API key alongside the created
// agent — the one and only time it appears in a response.
type registerResponse struct {
	Agent
	APIKey string `json:"api_key"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// writeError writes a JSON error body, {"error": "..."} — the shape every
// handler in this API uses, not just this one.
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: message})
}

// RegisterHandler returns the handler for POST /v1/rooms/{room_id}/agents.
// Mount it behind user.Authenticate — it also checks that the
// authenticated user owns the target room (403 on mismatch, 404 if the
// room doesn't exist at all, the same non-leaking pattern used elsewhere
// in this API) before registering anything. It issues the agent's scoped
// API key (see GenerateAPIKey) and returns the plaintext key in the
// response body exactly once; only its hash is persisted.
func (s *Store) RegisterHandler(rooms *room.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := user.FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		roomID, err := uuid.Parse(chi.URLParam(r, RoomIDParam))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid room_id")
			return
		}

		rm, err := rooms.GetByID(r.Context(), roomID)
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

		var req registerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		// Reflects the providers implemented today, not a permanent
		// restriction — extend alongside a new provider client, not before.
		switch Provider(req.Provider) {
		case ProviderAnthropic, ProviderOpenAI:
		default:
			writeError(w, http.StatusBadRequest, `provider must be "anthropic" or "openai"`)
			return
		}

		plaintext, hash, err := GenerateAPIKey()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to generate api key")
			return
		}

		a, err := s.Register(r.Context(), roomID, req.Name, Provider(req.Provider), req.Capabilities, hash)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to register agent")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(registerResponse{Agent: a, APIKey: plaintext})
	}
}
