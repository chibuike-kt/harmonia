package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/user"
)

// TestIntegration_RegisterHandler exercises POST /v1/rooms/{room_id}/agents
// end to end against real Postgres: the room's owner can register an
// agent into it, a different user cannot (403), an unauthenticated
// request never reaches the room lookup (401), and an unknown room_id
// 404s instead of 500ing. Requires a live Postgres — run via
// `make test-integration` after `make up`.
func TestIntegration_RegisterHandler(t *testing.T) {
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

	rooms := room.NewStore(pool)
	agents := NewStore(pool)

	owner := seedAgentTestUser(t, ctx, pool, "agent-register-owner-")
	other := seedAgentTestUser(t, ctx, pool, "agent-register-other-")

	rm, err := rooms.Create(ctx, &owner.ID, "agent-register-handler-test-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	h := agents.RegisterHandler(rooms)

	// The room's owner can register into it.
	rec := registerViaHandler(t, h, owner, rm.ID.String(), `{"name":"handler-test-agent","provider":"anthropic","capabilities":["research"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var got registerResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.APIKey == "" {
		t.Fatal("expected a non-empty plaintext api_key")
	}
	if got.RoomID != rm.ID {
		t.Fatalf("RoomID = %s, want %s", got.RoomID, rm.ID)
	}

	authenticated, err := agents.Authenticate(ctx, HashAPIKey(got.APIKey))
	if err != nil {
		t.Fatalf("Authenticate with issued key: %v", err)
	}
	if authenticated.ID != got.ID {
		t.Fatalf("authenticated agent ID = %s, want %s", authenticated.ID, got.ID)
	}

	// A different user cannot register into someone else's room.
	forbiddenRec := registerViaHandler(t, h, other, rm.ID.String(), `{"name":"trespasser-agent","provider":"anthropic"}`)
	if forbiddenRec.Code != http.StatusForbidden {
		t.Fatalf("non-owner status = %d, want %d, body = %s", forbiddenRec.Code, http.StatusForbidden, forbiddenRec.Body.String())
	}

	// An unauthenticated request never reaches the room lookup.
	unauthReq := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/rooms/"+rm.ID.String()+"/agents", strings.NewReader(`{"name":"anon-agent","provider":"anthropic"}`))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(RoomIDParam, rm.ID.String())
	unauthReq = unauthReq.WithContext(context.WithValue(unauthReq.Context(), chi.RouteCtxKey, rctx))
	unauthRec := httptest.NewRecorder()
	h.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", unauthRec.Code, http.StatusUnauthorized)
	}

	// An unknown room_id 404s, even for an authenticated user.
	orphanRec := registerViaHandler(t, h, owner, uuid.New().String(), `{"name":"orphan-agent","provider":"anthropic"}`)
	if orphanRec.Code != http.StatusNotFound {
		t.Fatalf("status for unknown room_id = %d, want %d, body = %s", orphanRec.Code, http.StatusNotFound, orphanRec.Body.String())
	}
}

// TestIntegration_RegisterHandler_ValidationOrder pins down the ordering
// a reviewer specifically asked about: room ownership resolves before
// request-body validation, not after. A non-owner gets 403 for a
// malformed body or a missing name — the same as for a well-formed one —
// because they never reach body validation at all; only the room's
// actual owner gets as far as a 400 for those same bad bodies. If
// ownership were checked after body validation, a non-owner sending a
// bad body would learn "the room exists and my request shape was wrong"
// before ever being told they don't own it.
func TestIntegration_RegisterHandler_ValidationOrder(t *testing.T) {
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

	rooms := room.NewStore(pool)
	agents := NewStore(pool)

	owner := seedAgentTestUser(t, ctx, pool, "agent-validation-order-owner-")
	other := seedAgentTestUser(t, ctx, pool, "agent-validation-order-other-")

	rm, err := rooms.Create(ctx, &owner.ID, "agent-validation-order-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	h := agents.RegisterHandler(rooms)

	badBodies := []string{"not json", `{"name":""}`, `{"name":"a","provider":"not-a-provider"}`}

	for _, body := range badBodies {
		rec := registerViaHandler(t, h, other, rm.ID.String(), body)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("non-owner with bad body %q: status = %d, want %d, body = %s", body, rec.Code, http.StatusForbidden, rec.Body.String())
		}
	}

	for _, body := range badBodies {
		rec := registerViaHandler(t, h, owner, rm.ID.String(), body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("owner with bad body %q: status = %d, want %d, body = %s", body, rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	}
}

func seedAgentTestUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, githubIDPrefix string) user.User {
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

func registerViaHandler(t *testing.T, h http.HandlerFunc, caller user.User, roomIDParam, body string) *httptest.ResponseRecorder {
	t.Helper()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(RoomIDParam, roomIDParam)
	ctx := context.WithValue(user.NewContext(context.Background(), caller), chi.RouteCtxKey, rctx)

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/rooms/"+roomIDParam+"/agents", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
