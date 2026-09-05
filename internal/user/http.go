package user

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// SessionsHandler returns the handler for GET /v1/sessions. Mount it
// behind Authenticate — it lists the authenticated user's own active
// sessions, never another user's.
func (s *Store) SessionsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		var currentHash string
		if cookie, err := r.Cookie(SessionCookieName); err == nil {
			currentHash = HashSessionToken(cookie.Value)
		}

		sessions, err := s.ListSessions(r.Context(), u.ID, currentHash)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list sessions")
			return
		}
		if sessions == nil {
			sessions = []SessionWithCurrent{}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(sessions)
	}
}

// RevokeSessionHandler returns the handler for DELETE /v1/sessions/{id}.
// Mount it behind Authenticate — it can only revoke a session belonging
// to the authenticated user; anything else (wrong owner, unknown id,
// already revoked) reports 404, not 403, so the response can't be used
// to probe another user's session ids. If the revoked session is the
// one authenticating this very request, its cookie is cleared too, same
// as LogoutHandler — revoking someone else's session, or a non-current
// one of your own, must never touch the caller's cookie.
func (s *Store) RevokeSessionHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		sessionID, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid session id")
			return
		}

		var currentHash string
		if cookie, err := r.Cookie(SessionCookieName); err == nil {
			currentHash = HashSessionToken(cookie.Value)
		}

		revoked, wasCurrent, err := s.RevokeSession(r.Context(), sessionID, u.ID, currentHash)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to revoke session")
			return
		}
		if !revoked {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		if wasCurrent {
			clearSessionCookie(w)
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// MeHandler returns the handler for GET /v1/users/me. Mount it behind
// Authenticate — returns the authenticated user's own profile exactly as
// Authenticate resolved it this request, no extra query needed.
func (s *Store) MeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(u)
	}
}

type updateMeRequest struct {
	Username    *string `json:"username"`
	DisplayName *string `json:"display_name"`
}

// UpdateMeHandler returns the handler for PATCH /v1/users/me. Mount it
// behind Authenticate. Username and display name only — this phase's
// entire "settings" surface is this plus the sessions and credentials
// endpoints, not a new subsystem. A field omitted or null in the request
// body is left unchanged; a present-but-empty username is rejected
// rather than silently ignored or blanking out a NOT NULL column.
func (s *Store) UpdateMeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		var req updateMeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Username != nil && *req.Username == "" {
			writeError(w, http.StatusBadRequest, "username cannot be empty")
			return
		}

		updated, err := s.UpdateMe(r.Context(), u.ID, req.Username, req.DisplayName)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update user")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(updated)
	}
}

// LogoutHandler returns the handler for POST /v1/auth/logout. Not mounted
// behind Authenticate: logging out a request whose session already
// expired should still clear the stale cookie rather than 401, so this
// reads the cookie itself and no-ops if it's missing or doesn't match a
// live session.
func (s *Store) LogoutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(SessionCookieName); err == nil && cookie.Value != "" {
			if _, err := s.RevokeSessionByTokenHash(r.Context(), HashSessionToken(cookie.Value)); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to log out")
				return
			}
		}

		clearSessionCookie(w)
		w.WriteHeader(http.StatusNoContent)
	}
}
