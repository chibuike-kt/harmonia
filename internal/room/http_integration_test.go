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

// TestIntegration_ListHandler exercises GET /v1/rooms against real
// Postgres: only the caller's own rooms come back, most recently active
// first, has_running_agent reflects a real agents.status = 'running' row
// (proving the EXISTS subquery, not a stale/persisted flag), and a user
// with zero rooms gets [] rather than null. Requires a live Postgres —
// run via `make test-integration` after `make up`.
func TestIntegration_ListHandler(t *testing.T) {
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

	owner := seedRoomTestUser(t, ctx, pool, "room-list-owner-")
	other := seedRoomTestUser(t, ctx, pool, "room-list-other-")

	s := NewStore(pool)
	h := s.ListHandler()

	emptyRec := doListRequest(h, owner)
	if emptyRec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", emptyRec.Code, http.StatusOK, emptyRec.Body.String())
	}
	if emptyRec.Body.String() != "[]\n" {
		t.Fatalf("empty list body = %q, want %q", emptyRec.Body.String(), "[]\n")
	}

	older, err := s.Create(ctx, &owner.ID, "older room")
	if err != nil {
		t.Fatalf("create older room: %v", err)
	}
	newer, err := s.Create(ctx, &owner.ID, "newer room")
	if err != nil {
		t.Fatalf("create newer room: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE rooms SET last_activity_at = now() - interval '1 hour' WHERE id = $1`, older.ID); err != nil {
		t.Fatalf("backdate older room: %v", err)
	}

	othersRoom, err := s.Create(ctx, &other.ID, "not mine")
	if err != nil {
		t.Fatalf("create other user's room: %v", err)
	}

	// Inserted directly, not via internal/agent's own Store: that package
	// imports this one (agent.RegisterHandler takes a *room.Store), so
	// importing it back from here would be an import cycle.
	if _, err := pool.Exec(ctx, `
		INSERT INTO agents (room_id, name, provider, capabilities, status, api_key_hash)
		VALUES ($1, 'runner', 'anthropic', '[]', 'running', 'hash')
	`, newer.ID); err != nil {
		t.Fatalf("seed running agent: %v", err)
	}

	rec := doListRequest(h, owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got []Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rooms (owner's only, not other's), got %d: %+v", len(got), got)
	}
	if got[0].ID != newer.ID {
		t.Fatalf("got[0].ID = %s, want the newer room %s (most recently active first)", got[0].ID, newer.ID)
	}
	if !got[0].HasRunningAgent {
		t.Fatal("expected the newer room to report a running agent")
	}
	if got[1].ID != older.ID {
		t.Fatalf("got[1].ID = %s, want the older room %s", got[1].ID, older.ID)
	}
	if got[1].HasRunningAgent {
		t.Fatal("expected the older room to report no running agent")
	}
	for _, sm := range got {
		if sm.ID == othersRoom.ID {
			t.Fatalf("leaked another user's room into the response: %+v", sm)
		}
	}
}

func doListRequest(h http.HandlerFunc, u user.User) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(user.NewContext(context.Background(), u), http.MethodGet, "/v1/rooms", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
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
