package message

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/credentials"
	"github.com/chibuike-kt/harmonia/internal/provider"
	"github.com/chibuike-kt/harmonia/internal/realtime"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/store"
	"github.com/chibuike-kt/harmonia/internal/task"
	"github.com/chibuike-kt/harmonia/internal/user"
)

// hangingProviderAgent never returns from Generate on its own — no
// select on ctx.Done(), no timer, nothing — the real shape of the gap
// found during this project's own P1 live verification: a provider call
// that simply never comes back leaves an agent stuck showing "running"
// forever, with no error and nothing posted to explain it, because the
// old fail() path reused the exact context that had just expired to do
// its own recovery writes, so those writes silently failed too. This
// type exists to prove that's fixed — not that a timeout value is set
// somewhere, but that the room actually recovers when a provider truly
// never returns.
type hangingProviderAgent struct{}

func (hangingProviderAgent) Generate(context.Context, provider.GenerateRequest) (provider.GenerateResponse, error) {
	select {}
}

// TestIntegration_Orchestrator_HungProviderTimesOutAndRoomRecovers is the
// real proof for the standalone gap found during P1 live verification:
// internal/provider had no request timeout anywhere, so a hung provider
// call left an agent stuck at status "running" indefinitely, with no
// error and no recovery. This drives a real TriggerReply against a
// provider that never returns at all, with both of Orchestrator's own
// timeout budgets shrunk to milliseconds (the same test seam
// newProviderClient already establishes — see Orchestrator's own doc
// comment on generationTimeout/requestTimeout), and asserts on the two
// things that actually matter to a human watching the room: a real,
// visible failure message lands, and the agent's status is really back
// to available in the database — not just that CallWithTimeout's own
// unit test proves the timeout value is honored in isolation.
func TestIntegration_Orchestrator_HungProviderTimesOutAndRoomRecovers(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}
	tasks := task.NewStore(pool)

	owner := seedMessageTestUser(t, ctx, users, "msg-hung-provider-")
	rm, err := rooms.Create(ctx, &owner.ID, "hung-provider-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-hung-provider")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")

	s := NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	orch.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return hangingProviderAgent{}, nil
	}
	// Shrunk from the production 45s/2m budgets to milliseconds — this
	// test needs to actually wait out a real timeout to prove it fires,
	// and it should do that in well under a second, not minutes.
	orch.requestTimeout = 50 * time.Millisecond
	orch.generationTimeout = 500 * time.Millisecond

	triggering, err := s.CreateHuman(ctx, rm.ID, owner.ID, "@Claude are you there?", []uuid.UUID{a.ID}, nil)
	if err != nil {
		t.Fatalf("seed triggering message: %v", err)
	}
	orch.TriggerReply(a.ID, rm.OwnerID, triggering, 0, false)

	if msg := rec.recv(t); msg.Kind != realtime.KindPresence || msg.Presence.Status != string(agent.StatusRunning) {
		t.Fatalf("first published message = %+v, want presence running", msg)
	}

	failureMsg := rec.recv(t)
	if failureMsg.Kind != realtime.KindMessage {
		t.Fatalf("second published message = %+v, want a real visible failure message", failureMsg)
	}
	if !strings.Contains(failureMsg.Message.Content, "couldn't generate a reply") {
		t.Fatalf("failure message content = %q, want it to explain the failure plainly", failureMsg.Message.Content)
	}
	if !strings.Contains(failureMsg.Message.Content, "timed out") {
		t.Fatalf("failure message content = %q, want it to name the timeout as the reason, not a generic failure", failureMsg.Message.Content)
	}

	if msg := rec.recv(t); msg.Kind != realtime.KindPresence || msg.Presence.Status != string(agent.StatusAvailable) {
		t.Fatalf("third published message = %+v, want presence available — the agent must not stay stuck showing running", msg)
	}

	// The published presence event is a claim about what happened;
	// this confirms it against the database directly, the same
	// standard every other status-transition test in this package
	// holds itself to.
	reloaded, err := agents.GetByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("reload agent: %v", err)
	}
	if reloaded.Status != agent.StatusAvailable {
		t.Fatalf("agent.Status = %s, want %s — a hung provider call must not leave an agent stuck running", reloaded.Status, agent.StatusAvailable)
	}
}
