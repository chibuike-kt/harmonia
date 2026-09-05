package agent

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chibuike-kt/harmonia/internal/room"
)

// TestIntegration_SetStatus exercises Store.SetStatus directly against
// real Postgres, independent of the full task claim/complete lifecycle
// TestIntegration_TaskLifecycle (internal/task) already proves this
// through — so a regression in SetStatus itself doesn't need five other
// moving parts to diagnose. Requires a live Postgres — run via
// `make test-integration` after `make up`.
func TestIntegration_SetStatus(t *testing.T) {
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

	rm, err := rooms.Create(ctx, nil, "agent-set-status-test-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	a, err := agents.Register(ctx, rm.ID, "set-status-test-agent", ProviderAnthropic, nil, "hash-set-status")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	if a.Status != StatusAvailable {
		t.Fatalf("newly registered agent Status = %q, want %q", a.Status, StatusAvailable)
	}

	if err := agents.SetStatus(ctx, a.ID, StatusRunning); err != nil {
		t.Fatalf("SetStatus(running): %v", err)
	}
	got, err := agents.GetByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetByID after SetStatus(running): %v", err)
	}
	if got.Status != StatusRunning {
		t.Fatalf("Status after SetStatus(running) = %q, want %q", got.Status, StatusRunning)
	}

	if err := agents.SetStatus(ctx, a.ID, StatusAvailable); err != nil {
		t.Fatalf("SetStatus(available): %v", err)
	}
	got, err = agents.GetByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetByID after SetStatus(available): %v", err)
	}
	if got.Status != StatusAvailable {
		t.Fatalf("Status after SetStatus(available) = %q, want %q", got.Status, StatusAvailable)
	}

	// SetStatus touches only the row named by its argument, never a
	// sibling agent's.
	bystander, err := agents.Register(ctx, rm.ID, "set-status-bystander", ProviderAnthropic, nil, "hash-bystander")
	if err != nil {
		t.Fatalf("register bystander: %v", err)
	}
	if err := agents.SetStatus(ctx, a.ID, StatusRunning); err != nil {
		t.Fatalf("SetStatus(running) again: %v", err)
	}
	gotBystander, err := agents.GetByID(ctx, bystander.ID)
	if err != nil {
		t.Fatalf("GetByID bystander: %v", err)
	}
	if gotBystander.Status != StatusAvailable {
		t.Fatalf("bystander Status = %q, want unchanged %q", gotBystander.Status, StatusAvailable)
	}
}
