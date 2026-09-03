package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrNotFound is returned when no agent matches the given API key hash.
var ErrNotFound = errors.New("agent: not found")

// apiKeyBytes is the amount of randomness backing each issued key.
const apiKeyBytes = 32

// GenerateAPIKey creates a new scoped credential: a plaintext key to return
// to the caller exactly once, and the hash of it that gets stored. The
// plaintext is not recoverable from the hash.
func GenerateAPIKey() (plaintext, hash string, err error) {
	buf := make([]byte, apiKeyBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	plaintext = hex.EncodeToString(buf)
	return plaintext, HashAPIKey(plaintext), nil
}

// HashAPIKey deterministically hashes a plaintext key for storage and
// lookup. Deterministic, not salted, by design — auth needs to find the
// owning agent by equality on the hash, not verify against one known agent.
func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// Authenticate looks up the agent owning apiKeyHash. Returns ErrNotFound if
// no agent matches.
func (s *Store) Authenticate(ctx context.Context, apiKeyHash string) (Agent, error) {
	var a Agent
	var capabilities []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, room_id, name, provider, capabilities, status, created_at
		FROM agents WHERE api_key_hash = $1
	`, apiKeyHash).Scan(&a.ID, &a.RoomID, &a.Name, &a.Provider, &capabilities, &a.Status, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	if err != nil {
		return Agent{}, err
	}
	if err := json.Unmarshal(capabilities, &a.Capabilities); err != nil {
		return Agent{}, err
	}
	return a, nil
}

type contextKey struct{}

var agentCtxKey = contextKey{}

// FromContext returns the agent that Authenticate resolved for this
// request, if any.
func FromContext(ctx context.Context) (Agent, bool) {
	a, ok := ctx.Value(agentCtxKey).(Agent)
	return a, ok
}

// RoomIDParam is the conventional chi URL param name RequireRoom checks
// the authenticated agent's room against.
const RoomIDParam = "room_id"

// Authenticate is chi middleware that resolves the bearer token in the
// Authorization header to its owning agent and stores it in the request
// context for downstream handlers and RequireRoom. It rejects with 401 on
// a missing/malformed header or an unrecognized key, without distinguishing
// the two, so the response can't be used to enumerate valid keys.
func Authenticate(store *Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, ok := bearerToken(r)
			if !ok {
				unauthorized(w)
				return
			}
			a, err := store.Authenticate(r.Context(), HashAPIKey(key))
			if err != nil {
				unauthorized(w)
				return
			}
			ctx := context.WithValue(r.Context(), agentCtxKey, a)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireRoom is chi middleware, chained after Authenticate, that rejects
// with 401 unless the authenticated agent belongs to the room named by the
// paramName URL parameter — a valid key for one room must not reach
// another room's resources.
func RequireRoom(paramName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a, ok := FromContext(r.Context())
			if !ok {
				unauthorized(w)
				return
			}
			roomID, err := uuid.Parse(chi.URLParam(r, paramName))
			if err != nil || roomID != a.RoomID {
				unauthorized(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(h, prefix))
	if token == "" {
		return "", false
	}
	return token, true
}

func unauthorized(w http.ResponseWriter) {
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}
