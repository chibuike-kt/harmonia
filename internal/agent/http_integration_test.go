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
)

// TestIntegration_RegisterHandler exercises POST /v1/rooms/{room_id}/agents
// end to end against real Postgres: a successful registration returns a
// usable plaintext key, and an unknown room_id 404s instead of 500ing.
// Requires a live Postgres — run via `make test-integration` after `make up`.
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

	rm, err := rooms.Create(ctx, "agent-register-handler-test-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	rec := registerViaHandler(t, agents, rm.ID.String(), `{"name":"handler-test-agent","provider":"anthropic","capabilities":["research"]}`)
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

	rec = registerViaHandler(t, agents, uuid.New().String(), `{"name":"orphan-agent","provider":"anthropic"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status for unknown room_id = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func registerViaHandler(t *testing.T, s *Store, roomIDParam, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := s.RegisterHandler()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(RoomIDParam, roomIDParam)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/rooms/"+roomIDParam+"/agents", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
