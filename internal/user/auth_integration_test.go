package user

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestIntegration_SessionAuthenticate exercises session issuance and
// lookup against real Postgres: a valid session authenticates, an
// unknown token doesn't, a revoked session stops authenticating, and an
// expired session stops authenticating. Requires a live Postgres — run
// via `make test-integration` after `make up`.
func TestIntegration_SessionAuthenticate(t *testing.T) {
	dbURL := os.Getenv("HARMONIA_DATABASE_URL")
	if dbURL == "" {
		t.Skip("HARMONIA_DATABASE_URL not set; skipping integration test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	githubID := "gh-session-test-" + uuid.New().String()
	var userID uuid.UUID
	err = pool.QueryRow(ctx, `
		INSERT INTO users (github_id, username) VALUES ($1, $2) RETURNING id
	`, githubID, "session-test-user").Scan(&userID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	s := NewStore(pool)

	plaintext, hash, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken: %v", err)
	}

	sess, err := s.CreateSession(ctx, userID, hash, nil, nil)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sess.UserID != userID {
		t.Fatalf("UserID = %s, want %s", sess.UserID, userID)
	}

	authenticated, err := s.Authenticate(ctx, HashSessionToken(plaintext))
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if authenticated.ID != userID {
		t.Fatalf("authenticated ID = %s, want %s", authenticated.ID, userID)
	}

	if _, err := s.Authenticate(ctx, HashSessionToken("not-a-real-token")); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("Authenticate unknown token: err = %v, want ErrSessionNotFound", err)
	}

	// Revoked sessions don't authenticate. No Store.Revoke yet — that's
	// step 5's job — so this updates the row directly.
	if _, err := pool.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE id = $1`, sess.ID); err != nil {
		t.Fatalf("revoke session: %v", err)
	}
	if _, err := s.Authenticate(ctx, HashSessionToken(plaintext)); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("Authenticate revoked session: err = %v, want ErrSessionNotFound", err)
	}

	// Expired (but not revoked) sessions don't authenticate either.
	plaintext2, hash2, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken: %v", err)
	}
	expiredSess, err := s.CreateSession(ctx, userID, hash2, nil, nil)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE sessions SET expires_at = now() - interval '1 hour' WHERE id = $1`, expiredSess.ID); err != nil {
		t.Fatalf("expire session: %v", err)
	}
	if _, err := s.Authenticate(ctx, HashSessionToken(plaintext2)); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("Authenticate expired session: err = %v, want ErrSessionNotFound", err)
	}
}
