package user

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrSessionNotFound is returned when no valid (unrevoked, unexpired)
// session matches the given token hash.
var ErrSessionNotFound = errors.New("user: session not found")

// SessionCookieName is the cookie carrying the opaque session token.
const SessionCookieName = "harmonia_session"

// sessionTokenBytes matches agent.apiKeyBytes — same crypto/rand pattern,
// same reasoning, genuinely separate system (see ADR-002).
const sessionTokenBytes = 32

// sessionTTL is how long a session is valid from issuance. Not specified
// by ADR-002 or the build brief — 30 days is a reasonable default for a
// cookie-based web session and an easy value to revisit if it's wrong.
const sessionTTL = 30 * 24 * time.Hour

type Session struct {
	ID         uuid.UUID  `json:"id"`
	UserID     uuid.UUID  `json:"user_id"`
	UserAgent  *string    `json:"user_agent,omitempty"`
	IPAddress  *string    `json:"ip_address,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt time.Time  `json:"last_seen_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// GenerateSessionToken creates a new opaque session credential: a
// plaintext token to set as the cookie value exactly once, and the hash
// of it that gets stored. The plaintext is not recoverable from the hash.
func GenerateSessionToken() (plaintext, hash string, err error) {
	buf := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	plaintext = hex.EncodeToString(buf)
	return plaintext, HashSessionToken(plaintext), nil
}

// HashSessionToken deterministically hashes a plaintext token for storage
// and lookup — same reasoning as agent.HashAPIKey: auth needs to find the
// owning session by equality on the hash, not verify against one known
// session.
func HashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CreateSession issues a new session for userID and returns it.
func (s *Store) CreateSession(ctx context.Context, userID uuid.UUID, tokenHash string, userAgent, ipAddress *string) (Session, error) {
	var sess Session
	expiresAt := time.Now().Add(sessionTTL)
	err := s.pool.QueryRow(ctx, `
		INSERT INTO sessions (user_id, token_hash, user_agent, ip_address, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, user_id, user_agent, ip_address, created_at, last_seen_at, expires_at, revoked_at
	`, userID, tokenHash, userAgent, ipAddress, expiresAt).Scan(
		&sess.ID, &sess.UserID, &sess.UserAgent, &sess.IPAddress, &sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt, &sess.RevokedAt,
	)
	return sess, err
}

// Authenticate resolves a session token hash to its owning user. Returns
// ErrSessionNotFound if there's no matching session, or if it's expired
// or revoked — those cases aren't distinguished in the response, so a
// caller can't use it to probe session state.
func (s *Store) Authenticate(ctx context.Context, tokenHash string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT u.id, u.github_id, u.google_id, u.username, u.display_name, u.avatar_url, u.email, u.created_at
		FROM sessions se
		JOIN users u ON u.id = se.user_id
		WHERE se.token_hash = $1 AND se.revoked_at IS NULL AND se.expires_at > now()
	`, tokenHash).Scan(&u.ID, &u.GitHubID, &u.GoogleID, &u.Username, &u.DisplayName, &u.AvatarURL, &u.Email, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrSessionNotFound
	}
	return u, err
}

type contextKey struct{}

var userCtxKey = contextKey{}

// FromContext returns the user that Authenticate resolved for this
// request, if any.
func FromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userCtxKey).(User)
	return u, ok
}

// NewContext returns a copy of ctx carrying u as the authenticated user.
func NewContext(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, userCtxKey, u)
}

// Authenticate is chi middleware that resolves the session cookie to its
// owning user and stores it in the request context. It rejects with 401
// on a missing cookie or an unknown/expired/revoked session, without
// distinguishing the cases, so the response can't be used to probe
// session state.
//
// Genuinely separate from agent.Authenticate per ADR-002: a different
// credential (cookie, not a bearer header), a different table, a
// different context key. A route needs exactly one of the two — never
// both, and never sharing code or a context key between them.
func Authenticate(store *Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(SessionCookieName)
			if err != nil || cookie.Value == "" {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			u, err := store.Authenticate(r.Context(), HashSessionToken(cookie.Value))
			if err != nil {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			next.ServeHTTP(w, r.WithContext(NewContext(r.Context(), u)))
		})
	}
}

// issueSessionAndSetCookie generates a new session for userID, stores it,
// and sets it as the response cookie. Shared by every OAuth callback —
// GitHub now, Google in step 3 — so the cookie attributes (HttpOnly,
// Secure, SameSite=Lax, no exceptions) are set in exactly one place.
func (s *Store) issueSessionAndSetCookie(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	plaintext, hash, err := GenerateSessionToken()
	if err != nil {
		return err
	}

	var userAgent *string
	if ua := r.UserAgent(); ua != "" {
		userAgent = &ua
	}
	var ipAddress *string
	if ip := clientIP(r); ip != "" {
		ipAddress = &ip
	}

	sess, err := s.CreateSession(r.Context(), userID, hash, userAgent, ipAddress)
	if err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    plaintext,
		Path:     "/",
		Expires:  sess.ExpiresAt,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// clientIP extracts the request's peer address, stripping the port. Falls
// back to the raw RemoteAddr if it isn't in host:port form.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type errorResponse struct {
	Error string `json:"error"`
}

// writeError writes a JSON error body, {"error": "..."} — matching the
// shape every other handler in this API uses. agent.Authenticate predates
// this convention and still writes plain text; not fixing that here since
// it's unrelated to this package's own work.
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: message})
}
