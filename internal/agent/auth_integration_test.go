package agent

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chibuike-kt/harmonia/internal/room"
)

// TestIntegration_AuthenticateRoundTrip exercises key issuance and lookup
// against real Postgres: register an agent with a hashed key, then confirm
// Authenticate resolves the plaintext key back to that agent and rejects an
// unknown one. Requires a live Postgres — run via `make test-integration`
// after `make up`.
func TestIntegration_AuthenticateRoundTrip(t *testing.T) {
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

	rm, err := rooms.Create(ctx, nil, "agent-auth-test-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	plaintext, hash, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}

	registered, err := agents.Register(ctx, rm.ID, "auth-test-agent", ProviderAnthropic, nil, hash)
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}

	got, err := agents.Authenticate(ctx, HashAPIKey(plaintext))
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if got.ID != registered.ID {
		t.Fatalf("authenticated agent ID = %s, want %s", got.ID, registered.ID)
	}
	if got.RoomID != rm.ID {
		t.Fatalf("authenticated agent RoomID = %s, want %s", got.RoomID, rm.ID)
	}

	if _, err := agents.Authenticate(ctx, HashAPIKey("not-a-real-key")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Authenticate with unknown key: err = %v, want ErrNotFound", err)
	}
}
