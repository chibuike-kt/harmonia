package message

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/actionproposal"
	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/credentials"
	"github.com/chibuike-kt/harmonia/internal/provider"
	"github.com/chibuike-kt/harmonia/internal/realtime"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/store"
	"github.com/chibuike-kt/harmonia/internal/task"
	"github.com/chibuike-kt/harmonia/internal/user"
)

// TestIntegration_Orchestrator_CreateTaskExecutesImmediately is ADR-006
// batch C's central create_task proof: an agent's own tool call produces
// a real task row and its TASK_CREATED event immediately — the same
// effect as a human hitting POST /v1/tasks directly, not a second,
// lesser path. Uses a fake provider client (the same test seam every
// other orchestration test in this file already uses) so this runs in
// CI without a real network call.
func TestIntegration_Orchestrator_CreateTaskExecutesImmediately(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}
	tasks := task.NewStore(pool)

	owner := seedMessageTestUser(t, ctx, users, "msg-create-task-")
	rm, err := rooms.Create(ctx, &owner.ID, "create-task-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-create-task")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")

	const objective = "write the release notes"
	s := NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	orch.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{
			content:   "Sure, I've created that task.",
			toolCalls: []provider.ToolCall{{Name: createTaskToolName, Input: map[string]any{"objective": objective}}},
		}, nil
	}

	triggering, err := s.CreateHuman(ctx, rm.ID, owner.ID, "@Claude please track this", []uuid.UUID{a.ID})
	if err != nil {
		t.Fatalf("seed triggering message: %v", err)
	}
	orch.TriggerReply(a.ID, rm.OwnerID, triggering, 0)

	// running -> reply -> available, same sequence every single-agent
	// invocation already produces, before create_task's own effects.
	if msg := rec.recv(t); msg.Kind != realtime.KindPresence || msg.Presence.Status != string(agent.StatusRunning) {
		t.Fatalf("first published message = %+v, want presence running", msg)
	}
	if msg := rec.recv(t); msg.Kind != realtime.KindMessage {
		t.Fatalf("second published message = %+v, want the reply", msg)
	}
	if msg := rec.recv(t); msg.Kind != realtime.KindPresence || msg.Presence.Status != string(agent.StatusAvailable) {
		t.Fatalf("third published message = %+v, want presence available", msg)
	}

	// create_task's own effect: a TASK.CREATE event, published after the
	// reply's own three messages.
	taskEvent := rec.recv(t)
	if taskEvent.Kind != realtime.KindEvent || taskEvent.Event == nil {
		t.Fatalf("fourth published message = %+v, want the TASK.CREATE event", taskEvent)
	}
	if taskEvent.Event.Type != "TASK.CREATE" {
		t.Fatalf("event.Type = %q, want %q", taskEvent.Event.Type, "TASK.CREATE")
	}
	if taskEvent.Event.Sender.AgentID != a.ID {
		t.Fatalf("event.Sender.AgentID = %s, want %s", taskEvent.Event.Sender.AgentID, a.ID)
	}

	active, err := tasks.ListActiveByRoom(ctx, rm.ID)
	if err != nil {
		t.Fatalf("ListActiveByRoom: %v", err)
	}
	if len(active) != 1 || active[0].Objective != objective {
		t.Fatalf("active tasks = %+v, want exactly one with objective %q", active, objective)
	}
	if active[0].Status != task.StatusQueued {
		t.Fatalf("task status = %s, want %s", active[0].Status, task.StatusQueued)
	}
}

// TestIntegration_Orchestrator_RequestHandoffCreatesPendingProposalOnly
// is ADR-006 batch C's central request_handoff proof: an agent's tool
// call creates a pending, human-approvable proposal — and, just as
// importantly, does NOT create a real handoff. The two are deliberately
// checked together: a test that only asserted the proposal exists
// wouldn't catch a bug that also (wrongly) executed the handoff
// immediately.
func TestIntegration_Orchestrator_RequestHandoffCreatesPendingProposalOnly(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}
	tasks := task.NewStore(pool)
	proposals := actionproposal.NewStore(pool)

	owner := seedMessageTestUser(t, ctx, users, "msg-request-handoff-")
	rm, err := rooms.Create(ctx, &owner.ID, "request-handoff-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	from, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-handoff-from")
	if err != nil {
		t.Fatalf("register proposing agent: %v", err)
	}
	to, err := agents.Register(ctx, rm.ID, "GPT", agent.ProviderOpenAI, nil, "hash-handoff-to")
	if err != nil {
		t.Fatalf("register target agent: %v", err)
	}
	openTask, err := tasks.Create(ctx, rm.ID, "existing open task", nil)
	if err != nil {
		t.Fatalf("seed open task: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")

	const summary = "half done, needs GPT's help finishing it"
	s := NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	var captured provider.GenerateRequest
	orch.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{
			content:         "I'll hand this off to GPT.",
			capturedRequest: &captured,
			toolCalls: []provider.ToolCall{{
				Name: requestHandoffToolName,
				Input: map[string]any{
					"task_id":       openTask.ID.String(),
					"to_agent_name": "GPT",
					"summary":       summary,
				},
			}},
		}, nil
	}

	triggering, err := s.CreateHuman(ctx, rm.ID, owner.ID, "@Claude how's it going", []uuid.UUID{from.ID})
	if err != nil {
		t.Fatalf("seed triggering message: %v", err)
	}
	orch.TriggerReply(from.ID, rm.OwnerID, triggering, 0)

	if msg := rec.recv(t); msg.Kind != realtime.KindPresence || msg.Presence.Status != string(agent.StatusRunning) {
		t.Fatalf("first published message = %+v, want presence running", msg)
	}
	if msg := rec.recv(t); msg.Kind != realtime.KindMessage {
		t.Fatalf("second published message = %+v, want the reply", msg)
	}
	// Generate has now returned (its result is what the reply above was
	// built from), so captured is safely readable here — the channel
	// receive above happens-after the goroutine's write to it.
	sawTool := false
	for _, tool := range captured.Tools {
		if tool.Name == requestHandoffToolName {
			sawTool = true
		}
	}
	if !sawTool {
		t.Fatalf("captured request.Tools = %+v, want request_handoff offered", captured.Tools)
	}
	if msg := rec.recv(t); msg.Kind != realtime.KindPresence || msg.Presence.Status != string(agent.StatusAvailable) {
		t.Fatalf("third published message = %+v, want presence available", msg)
	}

	proposeEvent := rec.recv(t)
	if proposeEvent.Kind != realtime.KindEvent || proposeEvent.Event == nil {
		t.Fatalf("fourth published message = %+v, want the ACTION.PROPOSE event", proposeEvent)
	}
	if proposeEvent.Event.Type != "ACTION.PROPOSE" {
		t.Fatalf("event.Type = %q, want %q", proposeEvent.Event.Type, "ACTION.PROPOSE")
	}

	pending, err := proposals.ListPendingByRoom(ctx, rm.ID)
	if err != nil {
		t.Fatalf("ListPendingByRoom: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending proposals = %+v, want exactly one", pending)
	}
	p := pending[0]
	if p.ActionType != actionproposal.ActionRequestHandoff {
		t.Fatalf("proposal.ActionType = %q, want %q", p.ActionType, actionproposal.ActionRequestHandoff)
	}
	if p.ProposingAgentID != from.ID {
		t.Fatalf("proposal.ProposingAgentID = %s, want %s", p.ProposingAgentID, from.ID)
	}
	if p.Payload["to_agent_id"] != to.ID.String() {
		t.Fatalf("proposal payload to_agent_id = %v, want %s", p.Payload["to_agent_id"], to.ID)
	}
	if p.Payload["summary"] != summary {
		t.Fatalf("proposal payload summary = %v, want %q", p.Payload["summary"], summary)
	}

	// The one thing this test most needs to prove: no real handoff exists
	// yet. ADR-006 is explicit that a proposal, unlike create_task, does
	// not execute anything on its own.
	var handoffCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM handoffs WHERE room_id = $1`, rm.ID).Scan(&handoffCount); err != nil {
		t.Fatalf("count handoffs: %v", err)
	}
	if handoffCount != 0 {
		t.Fatalf("handoffs for room = %d, want 0 — request_handoff must not execute immediately", handoffCount)
	}
}

// TestIntegration_Orchestrator_RequestHandoffToolNotOfferedWithoutOpenTask
// confirms requestHandoffTool's own gating: with no active task in the
// room, the tool is never declared to the model at all — there being
// nothing real for its task_id to reference, offering it would just
// invite a call executeRequestHandoff can only ever drop.
func TestIntegration_Orchestrator_RequestHandoffToolNotOfferedWithoutOpenTask(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}
	tasks := task.NewStore(pool)

	owner := seedMessageTestUser(t, ctx, users, "msg-no-open-task-")
	rm, err := rooms.Create(ctx, &owner.ID, "no-open-task-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-no-open-task")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")

	s := NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	var captured provider.GenerateRequest
	orch.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{content: "ok", capturedRequest: &captured}, nil
	}

	triggering, err := s.CreateHuman(ctx, rm.ID, owner.ID, "@Claude hi", []uuid.UUID{a.ID})
	if err != nil {
		t.Fatalf("seed triggering message: %v", err)
	}
	orch.TriggerReply(a.ID, rm.OwnerID, triggering, 0)
	waitForReplyMessage(t, ctx, s, rm.ID, triggering.ID)

	for _, tool := range captured.Tools {
		if tool.Name == requestHandoffToolName {
			t.Fatalf("request_handoff was offered with no open task in the room: %+v", captured.Tools)
		}
	}
}
