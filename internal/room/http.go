package room

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/user"
)

type createRequest struct {
	Name string `json:"name"`
}

// updateRequest fields are pointers so an omitted field in the request
// body is distinguishable from an explicit false/empty-string — see
// Store.Update.
type updateRequest struct {
	Name   *string `json:"name"`
	Pinned *bool   `json:"pinned"`
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

// UpdateHandler returns the handler for PATCH /v1/rooms/{id} — partial
// update of name and/or pinned. Mount it behind user.Authenticate.
// Ownership is checked the same way as every other room-scoped route
// (see realtime.StreamHandler): 404 if the room doesn't exist at all,
// 403 if it exists but isn't the caller's, so a non-owner learns nothing
// about a room they don't own.
func (s *Store) UpdateHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := user.FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		roomID, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid room id")
			return
		}

		var req updateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Name != nil && *req.Name == "" {
			writeError(w, http.StatusBadRequest, "name cannot be empty")
			return
		}

		ctx := r.Context()
		rm, err := s.GetByID(ctx, roomID)
		if errors.Is(err, ErrNotFound) {
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

		updated, err := s.Update(ctx, roomID, req.Name, req.Pinned)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update room")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(updated)
	}
}
