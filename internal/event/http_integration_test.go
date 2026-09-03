package event

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chibuike-kt/harmonia/internal/room"
)

// TestIntegration_ListByRoomHandler exercises GET /v1/rooms/{id}/events
// against real Postgres: an empty room returns [] (never null), and a
// recorded event shows up in the response. Room-scoping itself is
// agent.RequireRoom's job, exercised in the agent package and via full
// wiring in main.go — this test only proves the handler's own behavior.
// Requires a live Postgres — run via `make test-integration` after
// `make up`.
func TestIntegration_ListByRoomHandler(t *testing.T) {
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
	events := NewStore(pool)

	rm, err := rooms.Create(ctx, "event-history-handler-test-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	h := events.ListByRoomHandler()

	rec := doRequest(t, h, rm.ID.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Body.String() != "[]\n" {
		t.Fatalf("empty room body = %q, want %q", rec.Body.String(), "[]\n")
	}

	if err := events.Record(ctx, rm.ID, nil, nil, "ROOM_TEST_EVENT", map[string]any{"note": "hello"}); err != nil {
		t.Fatalf("record event: %v", err)
	}

	rec = doRequest(t, h, rm.ID.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got []Event
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].Type != "ROOM_TEST_EVENT" {
		t.Fatalf("Type = %q, want %q", got[0].Type, "ROOM_TEST_EVENT")
	}
}

func doRequest(t *testing.T, h http.HandlerFunc, roomIDParam string) *httptest.ResponseRecorder {
	t.Helper()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", roomIDParam)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/rooms/"+roomIDParam+"/events", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
