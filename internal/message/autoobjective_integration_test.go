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

// TestIntegration_ObjectiveGenerator_HappyPath exercises the plain
// success path against real Postgres: a room with a few early messages
// and no objective yet gets a generated objective applied, and the
// application is published over the hub (KindObjective) so a connected
// client updates live — the same proof TitleGenerator's own happy-path
// test gives for the name.
func TestIntegration_ObjectiveGenerator_HappyPath(t *testing.T) {
	pool, _ := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	messages := NewStore(pool)

	owner := seedMessageTestUser(t, ctx, users, "objective-happy-")
	rm, err := rooms.Create(ctx, &owner.ID, "objective-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if rm.Objective != nil {
		t.Fatalf("seeded room Objective = %v, want nil", rm.Objective)
	}

	// A real early exchange, not just the oldest message — this is
	// exactly the gap the real field closes versus the frontend's old
	// "oldest message" stand-in.
	humanMsg, err := messages.CreateHuman(ctx, rm.ID, owner.ID, "can someone help me debug why webhook retries keep failing intermittently?", nil)
	if err != nil {
		t.Fatalf("seed human message: %v", err)
	}
	a, err := agent.NewStore(pool).Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-objective-happy")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	if _, err := messages.CreateAgent(ctx, rm.ID, a.ID, "Let's start by checking the retry backoff config.", humanMsg.ID, nil, nil, nil); err != nil {
		t.Fatalf("seed agent reply: %v", err)
	}

	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")

	rec := newRecordingHub(rm.ID)
	objectiveGen := NewObjectiveGenerator(rooms, messages, creds, users, rec)
	objectiveGen.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{content: `"Debug intermittent webhook retry failures."`}, nil
	}

	objectiveGen.GenerateObjective(rm.ID, rm.OwnerID)

	published := rec.recv(t)
	if published.Kind != realtime.KindObjective || published.Objective == nil {
		t.Fatalf("published message = %+v, want a KindObjective update", published)
	}
	if published.Objective.RoomID != rm.ID {
		t.Fatalf("published Objective.RoomID = %s, want %s", published.Objective.RoomID, rm.ID)
	}
	// sanitizeObjective must have stripped the surrounding quotes the
	// fake response deliberately included, the same defensive cleanup a
	// real model's output needs.
	if published.Objective.Objective != "Debug intermittent webhook retry failures." {
		t.Fatalf("published Objective.Objective = %q, want sanitized objective without quotes", published.Objective.Objective)
	}

	final, err := rooms.GetByID(ctx, rm.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if final.Objective == nil || *final.Objective != "Debug intermittent webhook retry failures." {
		t.Fatalf("final room Objective = %v, want the generated objective actually persisted", final.Objective)
	}
}

// TestIntegration_ObjectiveGenerator_RaceGuardSkipsManualEdit is the
// objective-field counterpart to
// TestIntegration_TitleGenerator_RaceGuardSkipsManualRename: a human's
// manual edit, landing while the generation call is still in flight,
// must win unconditionally — the job re-reads the room's current
// objective immediately before writing and discards its own generated
// objective if a human has already set one. Injected deterministically
// from inside the fake provider's own Generate call (via beforeReturn),
// not a sleep, so this reproduces the exact race every time.
func TestIntegration_ObjectiveGenerator_RaceGuardSkipsManualEdit(t *testing.T) {
	pool, _ := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	messages := NewStore(pool)

	owner := seedMessageTestUser(t, ctx, users, "objective-race-")
	rm, err := rooms.Create(ctx, &owner.ID, "objective-race-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if _, err := messages.CreateHuman(ctx, rm.ID, owner.ID, "what's our plan for the Q3 migration?", nil); err != nil {
		t.Fatalf("seed human message: %v", err)
	}

	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")

	rec := newRecordingHub(rm.ID)
	objectiveGen := NewObjectiveGenerator(rooms, messages, creds, users, rec)

	const manualObjective = "Manually edited by a human mid-generation"
	objectiveGen.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{
			content: "Generated Objective That Must Lose",
			beforeReturn: func() {
				// Simulates the human's fast manual edit landing while
				// this slow provider call is still in flight — exactly
				// the race the guard is meant to handle.
				manual := manualObjective
				if _, err := rooms.Update(ctx, rm.ID, nil, nil, nil, nil, &manual); err != nil {
					t.Errorf("simulate concurrent manual edit: %v", err)
				}
			},
		}, nil
	}

	// GenerateObjective runs in its own goroutine — call the unexported
	// generate directly, synchronously, so this test doesn't need to
	// poll/sleep waiting for a background goroutine to finish before
	// asserting, same reasoning as the title generator's own race test.
	objectiveGen.generate(ctx, rm.ID, rm.OwnerID)

	final, err := rooms.GetByID(ctx, rm.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if final.Objective == nil || *final.Objective != manualObjective {
		t.Fatalf("final room Objective = %v, want the manual edit %q to have won", final.Objective, manualObjective)
	}

	// The race guard must skip silently — no publish at all, since the
	// generated objective was never applied.
	select {
	case msg := <-rec.ch:
		t.Fatalf("expected no published message after the race guard discards the generated objective, got %+v", msg)
	case <-time.After(200 * time.Millisecond):
	}
}
