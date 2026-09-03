// Package concurrency tests the "100 agents, 1 task, only one wins" property
// from design doc section 7 / original overview section 74. Requires a
// live Postgres — run via `make test-integration` after `make up`.
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
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/task"
)

func TestIntegration_OnlyOneAgentClaimsTask(t *testing.T) {
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

	rm, err := rooms.Create(ctx, "concurrency-test-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	const numAgents = 20
	agentIDs := make([]uuid.UUID, 0, numAgents)
	for i := 0; i < numAgents; i++ {
		a, err := agents.Register(ctx, rm.ID, "agent", agent.ProviderAnthropic, nil, "hash")
		if err != nil {
			t.Fatalf("register agent: %v", err)
		}
		agentIDs = append(agentIDs, a.ID)
	}

	tk, err := tasks.Create(ctx, rm.ID, "contested task", nil)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	var wg sync.WaitGroup
	var successes int64

	for _, id := range agentIDs {
		wg.Add(1)
		go func(id uuid.UUID) {
			defer wg.Done()
			if err := tasks.Claim(ctx, tk.ID, id); err == nil {
				atomic.AddInt64(&successes, 1)
			}
		}(id)
	}
	wg.Wait()

	if successes != 1 {
		t.Fatalf("expected exactly 1 successful claim, got %d", successes)
	}
}
