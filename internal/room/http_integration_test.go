package room

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chibuike-kt/harmonia/internal/user"
)

// TestIntegration_CreateHandler exercises POST /v1/rooms end to end against
// real Postgres: an authenticated request creates a room owned by the
// caller, and an unauthenticated one is rejected before ever touching the
// database. Requires a live Postgres — run via `make test-integration`
// after `make up`.
func TestIntegration_CreateHandler(t *testing.T) {
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

	owner := seedRoomTestUser(t, ctx, pool, "room-http-owner-")

	s := NewStore(pool)
	h := s.CreateHandler()

	req := httptest.NewRequestWithContext(
		user.NewContext(ctx, owner),
		http.MethodPost, "/v1/rooms", strings.NewReader(`{"name":"http-handler-test-room"}`),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var got Room
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.ID.String() == "" {
		t.Fatal("expected a non-zero room ID")
	}
	if got.Name != "http-handler-test-room" {
		t.Fatalf("Name = %q, want %q", got.Name, "http-handler-test-room")
	}
	if got.Status != "active" {
		t.Fatalf("Status = %q, want %q", got.Status, "active")
	}
	if got.OwnerID == nil || *got.OwnerID != owner.ID {
		t.Fatalf("OwnerID = %v, want %s", got.OwnerID, owner.ID)
	}

	unauthReq := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/rooms", strings.NewReader(`{"name":"should-not-be-created"}`))
	unauthRec := httptest.NewRecorder()
	h.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", unauthRec.Code, http.StatusUnauthorized)
	}
}

func seedRoomTestUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, githubIDPrefix string) user.User {
	t.Helper()
	var u user.User
	githubID := githubIDPrefix + uuid.New().String()
	err := pool.QueryRow(ctx, `
		INSERT INTO users (github_id, username) VALUES ($1, $2)
		RETURNING id, github_id, google_id, username, display_name, avatar_url, email, created_at
	`, githubID, githubIDPrefix+"user").Scan(
		&u.ID, &u.GitHubID, &u.GoogleID, &u.Username, &u.DisplayName, &u.AvatarURL, &u.Email, &u.CreatedAt,
	)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u
}
