// TestIntegration_OnlyOneResolutionSucceeds extends this package's
// "only one wins" property (see task_claim_test.go, task_complete_test.go)
// to ADR-006 batch C's action proposals: many concurrent approve/reject
// calls racing the same pending proposal, exactly one of which may
// succeed. Requires a live Postgres — run via `make test-integration`
// after `make up`.
package concurrency

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chibuike-kt/harmonia/internal/actionproposal"
	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/room"
)

func TestIntegration_OnlyOneResolutionSucceeds(t *testing.T) {
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
	agents := agent.NewStore(pool)
	proposals := actionproposal.NewStore(pool)

	rm, err := rooms.Create(ctx, nil, "actionproposal-concurrency-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	from, err := agents.Register(ctx, rm.ID, "proposer", agent.ProviderAnthropic, nil, "hash")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}

	p, err := proposals.Create(ctx, rm.ID, from.ID, actionproposal.ActionRequestHandoff, map[string]any{
		"task_id": "irrelevant-to-this-test", "to_agent_id": "irrelevant-to-this-test", "summary": "s",
	})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}

	const attempts = 20
	var wg sync.WaitGroup
	var successes int64

	for i := 0; i < attempts; i++ {
		wg.Add(1)
		// Alternate the attempted outcome — the point is that only one
		// resolution of EITHER kind can ever land, not that approvals
		// and rejections queue politely behind each other.
		status := actionproposal.StatusApproved
		if i%2 == 1 {
			status = actionproposal.StatusRejected
		}
		go func(status actionproposal.Status) {
			defer wg.Done()
			if _, err := proposals.Resolve(ctx, p.ID, status); err == nil {
				atomic.AddInt64(&successes, 1)
			}
		}(status)
	}
	wg.Wait()

	if successes != 1 {
		t.Fatalf("expected exactly 1 successful resolution, got %d", successes)
	}

	final, err := proposals.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if final.Status != actionproposal.StatusApproved && final.Status != actionproposal.StatusRejected {
		t.Fatalf("final status = %q, want approved or rejected", final.Status)
	}
}
