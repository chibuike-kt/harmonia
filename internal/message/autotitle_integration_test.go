package message

import (
	"context"
	"testing"
	"time"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/credentials"
	"github.com/chibuike-kt/harmonia/internal/provider"
	"github.com/chibuike-kt/harmonia/internal/realtime"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/user"
)

// TestIntegration_TitleGenerator_HappyPath exercises the plain success
// path against real Postgres: a fresh, placeholder-named room gets
// renamed to the generated title, and the rename is published over the
// hub (KindRoom) so a connected client updates live.
func TestIntegration_TitleGenerator_HappyPath(t *testing.T) {
	pool, _ := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	creds := credentials.NewStore(pool, nil)

	owner := seedMessageTestUser(t, ctx, users, "title-happy-")
	rm, err := rooms.Create(ctx, &owner.ID, room.PlaceholderName)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if rm.Name != room.PlaceholderName {
		t.Fatalf("seeded room Name = %q, want placeholder %q", rm.Name, room.PlaceholderName)
	}

	// No BYOK credential is connected for owner, so resolveClient falls
	// back to the env-var dev path — that fallback only calls
	// newProviderClient at all once it finds a non-empty env var, so a
	// dummy value is required here even though the fake client below
	// never reads it.
	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")

	rec := newRecordingHub(rm.ID)
	titleGen := NewTitleGenerator(rooms, creds, rec)
	titleGen.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{content: `"Debugging the Webhook Retry Logic."`}, nil
	}

	titleGen.GenerateTitle(rm.ID, rm.OwnerID, "why do webhook retries keep failing intermittently?")

	published := rec.recv(t)
	if published.Kind != realtime.KindRoom || published.Room == nil {
		t.Fatalf("published message = %+v, want a KindRoom update", published)
	}
	if published.Room.RoomID != rm.ID {
		t.Fatalf("published Room.RoomID = %s, want %s", published.Room.RoomID, rm.ID)
	}
	// sanitizeTitle must have stripped the surrounding quotes and
	// trailing period the fake response deliberately included, the same
	// defensive cleanup a real model's output needs.
	if published.Room.Name != "Debugging the Webhook Retry Logic" {
		t.Fatalf("published Room.Name = %q, want sanitized title without quotes/period", published.Room.Name)
	}

	final, err := rooms.GetByID(ctx, rm.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if final.Name != "Debugging the Webhook Retry Logic" {
		t.Fatalf("final room Name = %q, want the generated title actually persisted", final.Name)
	}
}

// TestIntegration_TitleGenerator_RaceGuardSkipsManualRename is the test
// the build brief calls out by name: a human's manual rename, landing
// while the generation call is still in flight, must win unconditionally
// — the job re-reads the room's current name immediately before writing
// and discards its own generated title if the room is no longer the
// placeholder. The rename is injected deterministically from inside the
// fake provider's own Generate call (via beforeReturn) — not a sleep —
// so this test reproduces the exact race every time, not "usually."
func TestIntegration_TitleGenerator_RaceGuardSkipsManualRename(t *testing.T) {
	pool, _ := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	creds := credentials.NewStore(pool, nil)

	owner := seedMessageTestUser(t, ctx, users, "title-race-")
	rm, err := rooms.Create(ctx, &owner.ID, room.PlaceholderName)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")

	rec := newRecordingHub(rm.ID)
	titleGen := NewTitleGenerator(rooms, creds, rec)

	const manualName = "Renamed by a human mid-generation"
	titleGen.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{
			content: "Generated Title That Must Lose",
			beforeReturn: func() {
				// Simulates the human's fast manual rename landing while
				// this slow provider call is still in flight — exactly
				// the race ADR-004's addendum describes.
				manual := manualName
				if _, err := rooms.Update(ctx, rm.ID, &manual, nil); err != nil {
					t.Errorf("simulate concurrent manual rename: %v", err)
				}
			},
		}, nil
	}

	// GenerateTitle runs in its own goroutine — call the unexported
	// generate directly, synchronously, so this test doesn't need to
	// poll/sleep waiting for a background goroutine to finish before
	// asserting: the whole point here is deterministic ordering, and
	// going through the async entry point would reintroduce exactly the
	// timing uncertainty this test is designed to avoid.
	titleGen.generate(ctx, rm.ID, rm.OwnerID, "does this get overwritten?")

	final, err := rooms.GetByID(ctx, rm.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if final.Name != manualName {
		t.Fatalf("final room Name = %q, want the manual rename %q to have won", final.Name, manualName)
	}

	// The race guard must skip silently — no publish at all, since the
	// generated title was never applied.
	select {
	case msg := <-rec.ch:
		t.Fatalf("expected no published message after the race guard discards the generated title, got %+v", msg)
	case <-time.After(200 * time.Millisecond):
	}
}
