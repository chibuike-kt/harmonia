package room

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestIntegration_CreateHandler exercises POST /v1/rooms end to end against
// real Postgres. Requires a live Postgres — run via `make test-integration`
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

	s := NewStore(pool)
	h := s.CreateHandler()

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/rooms", strings.NewReader(`{"name":"http-handler-test-room"}`))
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
}
