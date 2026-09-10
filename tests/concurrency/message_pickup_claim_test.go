// TestIntegration_OnlyOneMessagePickupClaimSucceeds extends this
// package's "only one wins" property (see task_claim_test.go,
// action_proposal_resolve_test.go) to ADR-007 batch B's own atomic
// claim: many agents, one unaddressed message, exactly one claim may
// succeed — proving phase 2's single-real-reply guarantee doesn't
// depend on goroutine scheduling or timing, only on the database's own
// row-level locking, the same reasoning task.Store.Claim's WHERE
// status = 'QUEUED' already relies on. Requires a live Postgres — run
// via `make test-integration` after `make up`.
package concurrency

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/message"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/user"
)

func TestIntegration_OnlyOneMessagePickupClaimSucceeds(t *testing.T) {
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

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	messages := message.NewStore(pool)

	owner, err := users.UpsertByGitHubID(ctx, "pickup-claim-concurrency-owner-"+uuid.New().String(), "pickup-claim-owner", nil, nil, nil)
	if err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	rm, err := rooms.Create(ctx, &owner.ID, "pickup-claim-concurrency-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	triggering, err := messages.CreateHuman(ctx, rm.ID, owner.ID, "does anyone want to help with this?", nil)
	if err != nil {
		t.Fatalf("seed triggering message: %v", err)
	}

	const numAgents = 20
	agentIDs := make([]uuid.UUID, 0, numAgents)
	for i := 0; i < numAgents; i++ {
		a, err := agents.Register(ctx, rm.ID, "pickup-claimant", agent.ProviderAnthropic, nil, "hash")
		if err != nil {
			t.Fatalf("register agent: %v", err)
		}
		agentIDs = append(agentIDs, a.ID)
	}

	var wg sync.WaitGroup
	var successes int64
	for _, agentID := range agentIDs {
		wg.Add(1)
		go func(agentID uuid.UUID) {
			defer wg.Done()
			claimed, err := messages.ClaimForPickup(ctx, triggering.ID, agentID)
			if err != nil {
				t.Errorf("ClaimForPickup: %v", err)
				return
			}
			if claimed {
				atomic.AddInt64(&successes, 1)
			}
		}(agentID)
	}
	wg.Wait()

	if successes != 1 {
		t.Fatalf("expected exactly 1 successful claim, got %d", successes)
	}
}
