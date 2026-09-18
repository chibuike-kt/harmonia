package agentloop

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/companionrelay"
	"github.com/chibuike-kt/harmonia/internal/credentials"
	"github.com/chibuike-kt/harmonia/internal/event"
	"github.com/chibuike-kt/harmonia/internal/provider"
	"github.com/chibuike-kt/harmonia/internal/realtime"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/store"
	"github.com/chibuike-kt/harmonia/internal/task"
	"github.com/chibuike-kt/harmonia/internal/user"
)

func connectAgentLoopTestPool(t *testing.T) (*pgxpool.Pool, *redis.Client) {
	t.Helper()
	dbURL := os.Getenv("HARMONIA_DATABASE_URL")
	if dbURL == "" {
		t.Skip("HARMONIA_DATABASE_URL not set; skipping integration test")
	}
	redisAddr := os.Getenv("HARMONIA_REDIS_ADDR")
	if redisAddr == "" {
		t.Skip("HARMONIA_REDIS_ADDR not set; skipping integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	t.Cleanup(func() { _ = rdb.Close() })
	return pool, rdb
}

func seedAgentLoopTestUser(t *testing.T, ctx context.Context, users *user.Store, githubIDPrefix string) user.User {
	t.Helper()
	u, err := users.UpsertByGitHubID(ctx, githubIDPrefix+uuid.New().String(), githubIDPrefix+"user", nil, nil, nil)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u
}

// sequencedAgent is a fake provider.Agent that answers each successive
// Generate call with the next canned response in order (repeating the
// last one if called more times than provided) — real multi-turn history
// (req.Messages) is available to a test via capturedRequests, but this
// fake's own answers don't branch on it; the test's own tool-dispatch
// simulation is what makes each turn's "observation" real.
//
// blockOn, when non-negative, makes the call at that zero-based index
// block until either unblock is closed or ctx is canceled — the real
// mechanism TestIntegration_Stop_CancelsAModelCallInFlight uses to prove
// Stop aborts a real in-flight call, not just a check on the next loop.
type sequencedAgent struct {
	mu        sync.Mutex
	responses []provider.GenerateResponse
	calls     int

	blockOn int
	unblock chan struct{}
}

func (a *sequencedAgent) Generate(ctx context.Context, req provider.GenerateRequest) (provider.GenerateResponse, error) {
	a.mu.Lock()
	i := a.calls
	a.calls++
	a.mu.Unlock()

	if a.unblock != nil && i == a.blockOn {
		select {
		case <-a.unblock:
		case <-ctx.Done():
			return provider.GenerateResponse{}, ctx.Err()
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if i < len(a.responses) {
		return a.responses[i], nil
	}
	return a.responses[len(a.responses)-1], nil
}

func (a *sequencedAgent) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func toolCallResponse(name string, input map[string]any) provider.GenerateResponse {
	return provider.GenerateResponse{ToolCalls: []provider.ToolCall{{ID: "fake-" + name, Name: name, Input: input}}}
}

// companionSim stands in for the reviewing human's own browser tab —
// exactly like companionrelay's and actionproposal's own tests already do
// for their real callers — answering every real companion_action this
// package dispatches with a real Resolve.
type companionSim struct {
	relay      *companionrelay.Coordinator
	terminalID string

	mu          sync.Mutex
	runCommands []string

	// onRunCommand, if set, runs synchronously before resolving a
	// run_command action — used to inject "presence is lost right now"
	// mid-loop at a deterministic point rather than a real sleep-based race.
	onRunCommand func()
}

func (c *companionSim) run(ctx context.Context, sub <-chan realtime.Message) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-sub:
			action := msg.CompanionAction
			if action == nil {
				continue
			}
			switch action.Type {
			case "create_terminal":
				_ = c.relay.Resolve(action.ID, companionrelay.Result{Output: c.terminalID})
			case "terminal_narrate":
				_ = c.relay.Resolve(action.ID, companionrelay.Result{Output: "ok"})
			case "write_file":
				_ = c.relay.Resolve(action.ID, companionrelay.Result{Output: "written"})
			case "read_file":
				_ = c.relay.Resolve(action.ID, companionrelay.Result{Output: "package main"})
			case "run_command":
				c.mu.Lock()
				c.runCommands = append(c.runCommands, action.Data)
				c.mu.Unlock()
				if c.onRunCommand != nil {
					c.onRunCommand()
				}
				_ = c.relay.Resolve(action.ID, companionrelay.Result{Output: "PASS"})
			}
		}
	}
}

func (c *companionSim) commandCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.runCommands)
}

// testManager wires a real Manager against real postgres/redis but a
// fake, injected provider client — the same "real everything except the
// network call to the model itself" seam internal/message's own
// integration tests already use via newProviderClient/resolveClient.
func testManager(pool *pgxpool.Pool, rdb *redis.Client, hub realtime.Publisher, relay *companionrelay.Coordinator, client provider.Agent) *Manager {
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	tasks := task.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}
	events := event.NewStore(pool)
	m := NewManager(rooms, agents, tasks, creds, beginner, events, hub, relay, rdb)
	m.resolveClient = func(context.Context, *uuid.UUID, agent.Provider) (provider.Agent, error) {
		return client, nil
	}
	return m
}

func waitForState(t *testing.T, s *Session, want State, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if snap := s.snapshot(); snap.State == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("session never reached state %q, still %q", want, s.snapshot().State)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// TestIntegration_Start_CompletesRealBoundedSessionWithVerification is
// this feature's central "real proof": a real bounded session runs a
// real multi-step task (write a file, run a real command to check it,
// then declare done) through real tool dispatch, real event recording,
// and stops itself via mark_done once the model calls it — exactly the
// build brief's "a real bounded session, a real multi-step task actually
// completing with real verification."
func TestIntegration_Start_CompletesRealBoundedSessionWithVerification(t *testing.T) {
	pool, rdb := connectAgentLoopTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)

	owner := seedAgentLoopTestUser(t, ctx, users, "agentloop-complete-")
	rm, err := rooms.Create(ctx, &owner.ID, "agentloop-complete-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-agentloop-complete")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}

	if err := realtime.SetHumanPresent(ctx, rdb, rm.ID, time.Minute); err != nil {
		t.Fatalf("set presence: %v", err)
	}
	t.Cleanup(func() { _ = realtime.ClearHumanPresent(context.Background(), rdb, rm.ID) })

	client := &sequencedAgent{responses: []provider.GenerateResponse{
		toolCallResponse(toolWriteFile, map[string]any{"path": "main.go", "content": "package main"}),
		toolCallResponse(toolRunCommand, map[string]any{"command": "go build ./..."}),
		toolCallResponse(toolMarkDone, map[string]any{"summary": "wrote main.go and ran go build ./... which passed"}),
	}, blockOn: -1}

	hub := realtime.NewHub()
	relay := companionrelay.NewCoordinator()
	sub, unsubscribe := hub.Subscribe(rm.ID)
	defer unsubscribe()

	sim := &companionSim{relay: relay, terminalID: "term-complete"}
	simCtx, cancelSim := context.WithCancel(ctx)
	defer cancelSim()
	go sim.run(simCtx, sub)

	m := testManager(pool, rdb, hub, relay, client)

	s, err := m.Start(ctx, StartParams{
		RoomID: rm.ID, AgentID: a.ID, Task: "add a real build check",
		MaxCycles: 10, DollarCapUSD: 5, WallClockSeconds: 30,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitForState(t, s, StateCompleted, 10*time.Second)

	snap := s.snapshot()
	if snap.Message == "" {
		t.Errorf("completed session Message is empty, want the model's mark_done summary")
	}
	if snap.Cycle != 3 {
		t.Errorf("Cycle = %d, want 3 (write_file, run_command, mark_done)", snap.Cycle)
	}
	if sim.commandCount() != 1 {
		t.Errorf("real run_command calls = %d, want exactly 1", sim.commandCount())
	}

	var startedCount, completedCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE room_id = $1 AND type = $2`, rm.ID, eventLoopStarted).Scan(&startedCount); err != nil {
		t.Fatalf("count AGENT_LOOP_STARTED events: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE room_id = $1 AND type = $2`, rm.ID, eventLoopCompleted).Scan(&completedCount); err != nil {
		t.Fatalf("count AGENT_LOOP_COMPLETED events: %v", err)
	}
	if startedCount != 1 || completedCount != 1 {
		t.Errorf("real recorded lifecycle events: started=%d completed=%d, want 1/1", startedCount, completedCount)
	}
}

// TestIntegration_PresenceLostMidLoop_StopsImmediately is this feature's
// other required proof: a session that's actively cycling (a real
// completed cycle already behind it) stops the instant presence is lost
// — before its next model call, with no further real tool dispatch — the
// same zero-ambiguity, no-grace-period guarantee ADR-010's live cursor
// already established, now extended to a whole session (ADR-011).
func TestIntegration_PresenceLostMidLoop_StopsImmediately(t *testing.T) {
	pool, rdb := connectAgentLoopTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)

	owner := seedAgentLoopTestUser(t, ctx, users, "agentloop-presence-")
	rm, err := rooms.Create(ctx, &owner.ID, "agentloop-presence-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-agentloop-presence")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}

	if err := realtime.SetHumanPresent(ctx, rdb, rm.ID, time.Minute); err != nil {
		t.Fatalf("set presence: %v", err)
	}
	t.Cleanup(func() { _ = realtime.ClearHumanPresent(context.Background(), rdb, rm.ID) })

	// Every cycle asks to run a real command and never calls mark_done —
	// without the presence gate this session would run until its cycle
	// cap, so reaching stopped_presence with only one real cycle behind
	// it proves the gate, not exhaustion, is what stopped it.
	client := &sequencedAgent{responses: []provider.GenerateResponse{
		toolCallResponse(toolRunCommand, map[string]any{"command": "go test ./..."}),
	}, blockOn: -1}

	hub := realtime.NewHub()
	relay := companionrelay.NewCoordinator()
	sub, unsubscribe := hub.Subscribe(rm.ID)
	defer unsubscribe()

	sim := &companionSim{relay: relay, terminalID: "term-presence"}
	// The exact deterministic moment presence is lost: right after the
	// first real run_command dispatch resolves, before the loop's next
	// iteration re-checks presence.
	sim.onRunCommand = func() {
		if err := realtime.ClearHumanPresent(context.Background(), rdb, rm.ID); err != nil {
			t.Errorf("clear presence: %v", err)
		}
	}
	simCtx, cancelSim := context.WithCancel(ctx)
	defer cancelSim()
	go sim.run(simCtx, sub)

	m := testManager(pool, rdb, hub, relay, client)

	s, err := m.Start(ctx, StartParams{
		RoomID: rm.ID, AgentID: a.ID, Task: "keep testing forever",
		MaxCycles: 50, DollarCapUSD: 50, WallClockSeconds: 60,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitForState(t, s, StateStoppedPresence, 10*time.Second)

	if snap := s.snapshot(); snap.Message != "" {
		t.Errorf("presence-loss Message = %q, want empty (silent per ADR-011)", snap.Message)
	}
	if got := client.callCount(); got != 1 {
		t.Errorf("real model calls after presence loss = %d, want exactly 1 (no second cycle)", got)
	}
	if got := sim.commandCount(); got != 1 {
		t.Errorf("real run_command dispatches = %d, want exactly 1 (no second cycle)", got)
	}

	var stoppedCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE room_id = $1 AND type = $2`, rm.ID, eventLoopStopped).Scan(&stoppedCount); err != nil {
		t.Fatalf("count AGENT_LOOP_STOPPED events: %v", err)
	}
	if stoppedCount != 1 {
		t.Errorf("real recorded AGENT_LOOP_STOPPED events = %d, want 1", stoppedCount)
	}
}

// TestIntegration_Stop_CancelsARealInFlightModelCall proves ADR-011's
// always-visible Stop control is real, not cooperative: canceling a
// session while its model call is genuinely blocked aborts that call
// immediately via context cancellation, the same real abort a human
// losing presence gets, but explicitly triggered instead.
func TestIntegration_Stop_CancelsARealInFlightModelCall(t *testing.T) {
	pool, rdb := connectAgentLoopTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)

	owner := seedAgentLoopTestUser(t, ctx, users, "agentloop-stop-")
	rm, err := rooms.Create(ctx, &owner.ID, "agentloop-stop-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-agentloop-stop")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}

	if err := realtime.SetHumanPresent(ctx, rdb, rm.ID, time.Minute); err != nil {
		t.Fatalf("set presence: %v", err)
	}
	t.Cleanup(func() { _ = realtime.ClearHumanPresent(context.Background(), rdb, rm.ID) })

	client := &sequencedAgent{
		responses: []provider.GenerateResponse{{}},
		blockOn:   0,
		unblock:   make(chan struct{}), // never closed — Stop must be what ends this call
	}

	hub := realtime.NewHub()
	relay := companionrelay.NewCoordinator()
	sub, unsubscribe := hub.Subscribe(rm.ID)
	defer unsubscribe()
	sim := &companionSim{relay: relay, terminalID: "term-stop"}
	simCtx, cancelSim := context.WithCancel(ctx)
	defer cancelSim()
	go sim.run(simCtx, sub)

	m := testManager(pool, rdb, hub, relay, client)

	s, err := m.Start(ctx, StartParams{
		RoomID: rm.ID, AgentID: a.ID, Task: "this call will hang until Stop",
		MaxCycles: 10, DollarCapUSD: 5, WallClockSeconds: 30,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Give the loop a real moment to actually enter the blocked Generate
	// call before Stop is issued — otherwise Stop could race ahead of it.
	deadline := time.After(2 * time.Second)
	for client.callCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("model call never started")
		case <-time.After(10 * time.Millisecond):
		}
	}

	if err := m.Stop(s.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	waitForState(t, s, StateStoppedManual, 5*time.Second)
}

// TestIntegration_MaxCyclesBound_StopsWithClearMessage proves the max-
// cycle bound stops a session that never calls mark_done, with a real,
// visible reason — never a silent stop, unlike presence loss.
func TestIntegration_MaxCyclesBound_StopsWithClearMessage(t *testing.T) {
	pool, rdb := connectAgentLoopTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)

	owner := seedAgentLoopTestUser(t, ctx, users, "agentloop-maxcycle-")
	rm, err := rooms.Create(ctx, &owner.ID, "agentloop-maxcycle-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-agentloop-maxcycle")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}

	if err := realtime.SetHumanPresent(ctx, rdb, rm.ID, time.Minute); err != nil {
		t.Fatalf("set presence: %v", err)
	}
	t.Cleanup(func() { _ = realtime.ClearHumanPresent(context.Background(), rdb, rm.ID) })

	client := &sequencedAgent{responses: []provider.GenerateResponse{
		toolCallResponse(toolRunCommand, map[string]any{"command": "go test ./..."}),
	}, blockOn: -1}

	hub := realtime.NewHub()
	relay := companionrelay.NewCoordinator()
	sub, unsubscribe := hub.Subscribe(rm.ID)
	defer unsubscribe()
	sim := &companionSim{relay: relay, terminalID: "term-maxcycle"}
	simCtx, cancelSim := context.WithCancel(ctx)
	defer cancelSim()
	go sim.run(simCtx, sub)

	m := testManager(pool, rdb, hub, relay, client)

	s, err := m.Start(ctx, StartParams{
		RoomID: rm.ID, AgentID: a.ID, Task: "never finishes",
		MaxCycles: 1, DollarCapUSD: 50, WallClockSeconds: 60,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitForState(t, s, StateStoppedBound, 10*time.Second)

	if snap := s.snapshot(); snap.Message == "" {
		t.Error("bound-hit Message is empty, want a real, visible reason")
	}
	if got := sim.commandCount(); got != 1 {
		t.Errorf("real run_command dispatches = %d, want exactly 1 (the cap is 1 cycle)", got)
	}
}

// TestIntegration_Start_RejectsASecondConcurrentSessionInTheSameRoom
// proves the one-session-per-room scoping Manager's own doc comment
// describes.
func TestIntegration_Start_RejectsASecondConcurrentSessionInTheSameRoom(t *testing.T) {
	pool, rdb := connectAgentLoopTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)

	owner := seedAgentLoopTestUser(t, ctx, users, "agentloop-conflict-")
	rm, err := rooms.Create(ctx, &owner.ID, "agentloop-conflict-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-agentloop-conflict")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}

	if err := realtime.SetHumanPresent(ctx, rdb, rm.ID, time.Minute); err != nil {
		t.Fatalf("set presence: %v", err)
	}
	t.Cleanup(func() { _ = realtime.ClearHumanPresent(context.Background(), rdb, rm.ID) })

	client := &sequencedAgent{
		responses: []provider.GenerateResponse{{}},
		blockOn:   0,
		unblock:   make(chan struct{}),
	}
	defer close(client.unblock)

	hub := realtime.NewHub()
	relay := companionrelay.NewCoordinator()
	sub, unsubscribe := hub.Subscribe(rm.ID)
	defer unsubscribe()
	sim := &companionSim{relay: relay, terminalID: "term-conflict"}
	simCtx, cancelSim := context.WithCancel(ctx)
	defer cancelSim()
	go sim.run(simCtx, sub)

	m := testManager(pool, rdb, hub, relay, client)

	s, err := m.Start(ctx, StartParams{
		RoomID: rm.ID, AgentID: a.ID, Task: "first session",
		MaxCycles: 10, DollarCapUSD: 5, WallClockSeconds: 30,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = m.Stop(s.ID) }()

	if _, err := m.Start(ctx, StartParams{
		RoomID: rm.ID, AgentID: a.ID, Task: "second session",
		MaxCycles: 10, DollarCapUSD: 5, WallClockSeconds: 30,
	}); err != ErrSessionAlreadyRunning {
		t.Fatalf("second Start error = %v, want ErrSessionAlreadyRunning", err)
	}
}
