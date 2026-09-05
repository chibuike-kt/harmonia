package room

import (
	"encoding/json"
	"net/http"

	"github.com/chibuike-kt/harmonia/internal/user"
)

type createRequest struct {
	Name string `json:"name"`
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

// CreateHandler returns the handler for POST /v1/rooms. Mount it behind
// user.Authenticate — every room created through this endpoint has an
// owner, set from the session.
func (s *Store) CreateHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := user.FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		var req createRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}

		rm, err := s.Create(r.Context(), &u.ID, req.Name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create room")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(rm)
	}
}

// ListHandler returns the handler for GET /v1/rooms — the authenticated
// user's own rooms, most recently active first. Mount it behind
// user.Authenticate.
func (s *Store) ListHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := user.FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		rooms, err := s.ListByOwner(r.Context(), u.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list rooms")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(rooms)
	}
}
