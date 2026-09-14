package message

import (
	"context"
	"strings"
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

	triggering, err := s.CreateHuman(ctx, rm.ID, owner.ID, "@Claude please track this", []uuid.UUID{a.ID}, nil)
	if err != nil {
		t.Fatalf("seed triggering message: %v", err)
	}
	orch.TriggerReply(a.ID, rm.OwnerID, triggering, 0, false)

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

// sequencedFakeAgent returns one provider.GenerateResponse per call, in
// order (pinned to the last one once exhausted) — unlike the shared
// fakeProviderAgent, which always returns the same fixed response, this
// exists specifically to test a mitigation whose whole point is "the
// model's SECOND response differs from its first."
type sequencedFakeAgent struct {
	responses []provider.GenerateResponse
	requests  []provider.GenerateRequest
}

func (f *sequencedFakeAgent) Generate(_ context.Context, req provider.GenerateRequest) (provider.GenerateResponse, error) {
	f.requests = append(f.requests, req)
	idx := len(f.requests) - 1
	if idx >= len(f.responses) {
		idx = len(f.responses) - 1
	}
	return f.responses[idx], nil
}

// TestIntegration_Orchestrator_CreateTaskNarrationRetryRecoversRealTask
// is P1's own real proof for the create_task-narration mitigation: a
// model's first reply narrates creating a task in plain text with no
// actual tool call (the exact live shape the dogfooding pass found —
// "Now, I will create a task... Creating the task now..." and nothing in
// ToolCalls) triggers exactly one retry, and the retry's own real
// create_task call is what actually executes — a real task row, not a
// second narrated non-event. The FINAL stored reply is the retry's
// content, not the original narration, so the human never sees the
// broken first attempt at all.
func TestIntegration_Orchestrator_CreateTaskNarrationRetryRecoversRealTask(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}
	tasks := task.NewStore(pool)

	owner := seedMessageTestUser(t, ctx, users, "msg-narration-retry-")
	rm, err := rooms.Create(ctx, &owner.ID, "narration-retry-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-narration-retry")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")

	const objective = "write the release notes"
	fake := &sequencedFakeAgent{
		responses: []provider.GenerateResponse{
			// First attempt: real narration, no tool call — the exact
			// live-reproduced bug shape.
			{Content: "Now, I will create a task for this. Creating the task now..."},
			// Retry: the model actually calls the tool this time.
			{
				Content:   "Done — I've created the task.",
				ToolCalls: []provider.ToolCall{{Name: createTaskToolName, Input: map[string]any{"objective": objective}}},
			},
		},
	}

	s := NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	orch.newProviderClient = func(agent.Provider, string) (provider.Agent, error) { return fake, nil }

	triggering, err := s.CreateHuman(ctx, rm.ID, owner.ID, "@Claude please track this", []uuid.UUID{a.ID}, nil)
	if err != nil {
		t.Fatalf("seed triggering message: %v", err)
	}
	orch.TriggerReply(a.ID, rm.OwnerID, triggering, 0, false)

	// Same sequence TestIntegration_Orchestrator_CreateTaskExecutesImmediately
	// already relies on: running -> reply -> available -> TASK.CREATE
	// event, in that exact order. Waiting for the real TASK.CREATE event
	// (rather than polling for the reply row and immediately checking the
	// tasks table) is what actually proves executeCreateTask's own
	// transaction committed — those two effects happen sequentially in
	// the same goroutine but aren't atomic with each other, so a
	// poll-then-immediately-check on the reply row alone would race
	// against the create_task commit that follows it.
	if msg := rec.recv(t); msg.Kind != realtime.KindPresence || msg.Presence.Status != string(agent.StatusRunning) {
		t.Fatalf("first published message = %+v, want presence running", msg)
	}
	replyMsg := rec.recv(t)
	if replyMsg.Kind != realtime.KindMessage {
		t.Fatalf("second published message = %+v, want the reply", replyMsg)
	}
	if msg := rec.recv(t); msg.Kind != realtime.KindPresence || msg.Presence.Status != string(agent.StatusAvailable) {
		t.Fatalf("third published message = %+v, want presence available", msg)
	}
	taskEvent := rec.recv(t)
	if taskEvent.Kind != realtime.KindEvent || taskEvent.Event == nil || taskEvent.Event.Type != "TASK.CREATE" {
		t.Fatalf("fourth published message = %+v, want the TASK.CREATE event — the retry's tool call must have actually executed", taskEvent)
	}

	if len(fake.requests) != 2 {
		t.Fatalf("provider Generate call count = %d, want exactly 2 (original + one bounded retry)", len(fake.requests))
	}

	// The retry request must carry the original narrated reply plus a
	// real nudge turn — proof the retry actually gave the model its own
	// prior text back, not a blind re-ask of the identical prompt.
	retryMessages := fake.requests[1].Messages
	if len(retryMessages) == 0 || retryMessages[len(retryMessages)-1].Role != "user" {
		t.Fatalf("retry request's last message = %+v, want a user-role nudge", retryMessages[len(retryMessages)-1])
	}
	if !strings.Contains(retryMessages[len(retryMessages)-1].Content, "create_task") {
		t.Fatalf("retry nudge = %q, want it to name create_task", retryMessages[len(retryMessages)-1].Content)
	}
	foundNarration := false
	for _, m := range retryMessages {
		if m.Role == "assistant" && strings.Contains(m.Content, "Creating the task now") {
			foundNarration = true
		}
	}
	if !foundNarration {
		t.Fatalf("retry request never included the model's own original narration as an assistant turn: %+v", retryMessages)
	}

	active, err := tasks.ListActiveByRoom(ctx, rm.ID)
	if err != nil {
		t.Fatalf("ListActiveByRoom: %v", err)
	}
	if len(active) != 1 || active[0].Objective != objective {
		t.Fatalf("active tasks = %+v, want exactly one with objective %q", active, objective)
	}

	// The human-visible reply is the retry's own content, never the
	// original broken narration.
	if replyMsg.Message == nil || replyMsg.Message.Content != "Done — I've created the task." {
		t.Fatalf("stored reply content = %+v, want the retry's own content, not the original narration", replyMsg.Message)
	}
}

// TestIntegration_Orchestrator_CreateTaskToolListsOpenTasks is the
// dogfooding report's own duplicate-task repro, made deterministic and
// proven at the tool-declaration layer: a room's currently open tasks
// are embedded directly in create_task's own description, the same
// pattern already proven for request_handoff's tasks/agents lists, so a
// model has real visibility into what's already tracked before deciding
// to create something new. Checks both the populated case (an existing
// task's objective must appear verbatim) and the empty case (no active
// tasks reads as "none yet," not a blank or malformed description).
func TestIntegration_Orchestrator_CreateTaskToolListsOpenTasks(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}
	tasks := task.NewStore(pool)

	owner := seedMessageTestUser(t, ctx, users, "msg-create-task-lists-")
	rm, err := rooms.Create(ctx, &owner.ID, "create-task-lists-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-create-task-lists")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")

	const existingObjective = "Write API documentation for the new endpoint"
	if _, err := tasks.Create(ctx, rm.ID, existingObjective, nil); err != nil {
		t.Fatalf("seed existing task: %v", err)
	}

	s := NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	var captured provider.GenerateRequest
	orch.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{content: "ok", capturedRequest: &captured}, nil
	}

	triggering, err := s.CreateHuman(ctx, rm.ID, owner.ID, "@Claude hi", []uuid.UUID{a.ID}, nil)
	if err != nil {
		t.Fatalf("seed triggering message: %v", err)
	}
	orch.TriggerReply(a.ID, rm.OwnerID, triggering, 0, false)
	waitForReplyMessage(t, ctx, s, rm.ID, triggering.ID)

	var createTask *provider.ToolDef
	for i, tool := range captured.Tools {
		if tool.Name == createTaskToolName {
			createTask = &captured.Tools[i]
		}
	}
	if createTask == nil {
		t.Fatal("create_task was not offered at all")
	}
	if !strings.Contains(createTask.Description, existingObjective) {
		t.Fatalf("create_task description didn't list the existing open task %q: %q", existingObjective, createTask.Description)
	}
}

// TestIntegration_Orchestrator_CreateTaskToolListsNoOpenTasks proves the
// empty-room half: with no active tasks, create_task's description
// reads as a real, well-formed "none yet" rather than a blank or
// malformed list — the same tool declaration must read sensibly whether
// or not there's anything to list.
func TestIntegration_Orchestrator_CreateTaskToolListsNoOpenTasks(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}
	tasks := task.NewStore(pool)

	owner := seedMessageTestUser(t, ctx, users, "msg-create-task-empty-")
	rm, err := rooms.Create(ctx, &owner.ID, "create-task-empty-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-create-task-empty")
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

	triggering, err := s.CreateHuman(ctx, rm.ID, owner.ID, "@Claude hi", []uuid.UUID{a.ID}, nil)
	if err != nil {
		t.Fatalf("seed triggering message: %v", err)
	}
	orch.TriggerReply(a.ID, rm.OwnerID, triggering, 0, false)
	waitForReplyMessage(t, ctx, s, rm.ID, triggering.ID)

	var createTask *provider.ToolDef
	for i, tool := range captured.Tools {
		if tool.Name == createTaskToolName {
			createTask = &captured.Tools[i]
		}
	}
	if createTask == nil {
		t.Fatal("create_task was not offered at all")
	}
	if !strings.Contains(createTask.Description, "none yet") {
		t.Fatalf("create_task description with no open tasks = %q, want it to read as \"none yet\"", createTask.Description)
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

	triggering, err := s.CreateHuman(ctx, rm.ID, owner.ID, "@Claude how's it going", []uuid.UUID{from.ID}, nil)
	if err != nil {
		t.Fatalf("seed triggering message: %v", err)
	}
	orch.TriggerReply(from.ID, rm.OwnerID, triggering, 0, false)

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

	triggering, err := s.CreateHuman(ctx, rm.ID, owner.ID, "@Claude hi", []uuid.UUID{a.ID}, nil)
	if err != nil {
		t.Fatalf("seed triggering message: %v", err)
	}
	orch.TriggerReply(a.ID, rm.OwnerID, triggering, 0, false)
	waitForReplyMessage(t, ctx, s, rm.ID, triggering.ID)

	for _, tool := range captured.Tools {
		if tool.Name == requestHandoffToolName {
			t.Fatalf("request_handoff was offered with no open task in the room: %+v", captured.Tools)
		}
	}
}
