package credentials

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/user"
)

type connectRequest struct {
	Provider string `json:"provider"`
	Key      string `json:"key"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// writeError writes a JSON error body, {"error": "..."} — the shape
// every handler in this API uses.
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: message})
}

// validProvider mirrors agent/http.go's own switch — the providers this
// deployment actually has clients for, not a permanent restriction.
func validProvider(name string) (agent.Provider, bool) {
	switch agent.Provider(name) {
	case agent.ProviderAnthropic, agent.ProviderOpenAI:
		return agent.Provider(name), true
	default:
		return "", false
	}
}

// ConnectHandler returns the handler for POST /v1/credentials. Mount it
// behind user.Authenticate. The plaintext key is verified with a live
// provider call and, on success, encrypted and stored; it is never
// returned in the response, logged, or persisted in any recoverable
// form — only the encrypted blob and a trailing hint are.
func (s *Store) ConnectHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := user.FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		var req connectRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		providerName, ok := validProvider(req.Provider)
		if !ok {
			writeError(w, http.StatusBadRequest, `provider must be "anthropic" or "openai"`)
			return
		}
		if req.Key == "" {
			writeError(w, http.StatusBadRequest, "key is required")
			return
		}

		cred, err := s.Connect(r.Context(), u.ID, providerName, req.Key)
		if err != nil {
			switch {
			case errors.Is(err, ErrEncryptionNotConfigured):
				writeError(w, http.StatusInternalServerError, "credential storage is not configured")
			case errors.Is(err, ErrVerificationFailed):
				writeError(w, http.StatusBadRequest, "provider rejected the key")
			default:
				writeError(w, http.StatusInternalServerError, "failed to save credential")
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(cred)
	}
}

// ListHandler returns the handler for GET /v1/credentials. Mount it
// behind user.Authenticate — lists only the authenticated user's own
// connected providers, hints only.
func (s *Store) ListHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := user.FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		creds, err := s.List(r.Context(), u.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list credentials")
			return
		}
		if creds == nil {
			creds = []Credential{}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(creds)
	}
}

// DeleteHandler returns the handler for DELETE /v1/credentials/{provider}.
// Mount it behind user.Authenticate. Scoped to the authenticated user by
// construction (the delete is WHERE user_id = ... AND provider = ...),
// so there's no id to enumerate and nothing to leak between users.
func (s *Store) DeleteHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := user.FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		providerName, ok := validProvider(chi.URLParam(r, "provider"))
		if !ok {
			writeError(w, http.StatusBadRequest, `provider must be "anthropic" or "openai"`)
			return
		}

		deleted, err := s.Delete(r.Context(), u.ID, providerName)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete credential")
			return
		}
		if !deleted {
			writeError(w, http.StatusNotFound, "credential not found")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}
