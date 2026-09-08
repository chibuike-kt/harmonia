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

// TestIntegration_Register_DefaultsToDistinctNameOnCollision is the real
// fix for the duplicate-agent-display investigation: two same-provider
// registrations in one room (AddAgentMenu's own default-name flow, one
// click per credential, no naming step) used to both land as "ChatGPT"
// with nothing to tell them apart — confirmed directly against
// production data as two genuinely distinct agent rows, not the
// mention-dedup bug manifesting differently. This isn't rejected as an
// error (a person may genuinely want two differently configured agents
// on the same provider) — it's defaulted to a distinct name instead.
func TestIntegration_Register_DefaultsToDistinctNameOnCollision(t *testing.T) {
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

	rm, err := rooms.Create(ctx, nil, "agent-distinct-name-test-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	first, err := agents.Register(ctx, rm.ID, "ChatGPT", ProviderOpenAI, nil, "hash-distinct-1")
	if err != nil {
		t.Fatalf("register first ChatGPT: %v", err)
	}
	if first.Name != "ChatGPT" {
		t.Fatalf("first agent Name = %q, want unchanged %q (no collision yet)", first.Name, "ChatGPT")
	}

	second, err := agents.Register(ctx, rm.ID, "ChatGPT", ProviderOpenAI, nil, "hash-distinct-2")
	if err != nil {
		t.Fatalf("register second ChatGPT: %v", err)
	}
	if second.Name != "ChatGPT 2" {
		t.Fatalf("second agent Name = %q, want %q", second.Name, "ChatGPT 2")
	}

	third, err := agents.Register(ctx, rm.ID, "ChatGPT", ProviderOpenAI, nil, "hash-distinct-3")
	if err != nil {
		t.Fatalf("register third ChatGPT: %v", err)
	}
	if third.Name != "ChatGPT 3" {
		t.Fatalf("third agent Name = %q, want %q — the first free suffix, not reused", third.Name, "ChatGPT 3")
	}

	// A different room's own "ChatGPT" is entirely unaffected — the
	// collision check is scoped to one room, not global.
	otherRoom, err := rooms.Create(ctx, nil, "agent-distinct-name-other-room")
	if err != nil {
		t.Fatalf("create other room: %v", err)
	}
	elsewhere, err := agents.Register(ctx, otherRoom.ID, "ChatGPT", ProviderOpenAI, nil, "hash-distinct-elsewhere")
	if err != nil {
		t.Fatalf("register ChatGPT in other room: %v", err)
	}
	if elsewhere.Name != "ChatGPT" {
		t.Fatalf("agent in a different room Name = %q, want unchanged %q", elsewhere.Name, "ChatGPT")
	}
}
