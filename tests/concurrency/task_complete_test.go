package concurrency

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/task"
)

// TestIntegration_OnlyOneCompletionSucceeds tests the same property as
// TestIntegration_OnlyOneAgentClaimsTask, for Complete instead of Claim:
// the WHERE status = 'CLAIMED' guard on Store.Complete (design doc section
// 7 reasoning, applied to the second transition) is what makes concurrent
// completions safe, not application-level locking. Requires a live
// Postgres — run via `make test-integration` after `make up`.
func TestIntegration_OnlyOneCompletionSucceeds(t *testing.T) {
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
	tasks := task.NewStore(pool)

	rm, err := rooms.Create(ctx, nil, "concurrency-test-room-complete")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	a, err := agents.Register(ctx, rm.ID, "agent", agent.ProviderAnthropic, nil, "hash")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}

	tk, err := tasks.Create(ctx, rm.ID, "contested task", nil)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := tasks.Claim(ctx, tk.ID, a.ID); err != nil {
		t.Fatalf("claim task: %v", err)
	}

	const numAttempts = 20
	var wg sync.WaitGroup
	var successes int64

	for i := 0; i < numAttempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := tasks.Complete(ctx, tk.ID); err == nil {
				atomic.AddInt64(&successes, 1)
			}
		}()
	}
	wg.Wait()

	if successes != 1 {
		t.Fatalf("expected exactly 1 successful completion, got %d", successes)
	}
}
