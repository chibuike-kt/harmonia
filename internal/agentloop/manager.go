package agentloop

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
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
)

// ErrSessionAlreadyRunning is returned by Start when roomID already has a
// live Session — at most one sustained loop per room at a time (see
// Session's own doc comment on why RoomID is this package's uniqueness
// key), the same "one real IDE session, one real loop" scoping ADR-011
// itself describes.
var ErrSessionAlreadyRunning = errors.New("agentloop: a session is already running in this room")

// ErrSessionNotFound is returned by Stop when sessionID names no
// currently-tracked Session — already finished, or never started.
var ErrSessionNotFound = errors.New("agentloop: no such session")

// Manager tracks every real, currently-running Session in this process,
// entirely in memory — the same "ephemeral, not persisted" precedent
// internal/message.Orchestrator's own lastPickupEvalAt already set for
// per-agent runtime state that a server restart is allowed to simply
// lose, while every real lifecycle transition still lands in the
// durable, queryable events table via events (see finish/run in loop.go).
type Manager struct {
	rooms    *room.Store
	agents   *agent.Store
	tasks    *task.Store
	creds    *credentials.Store
	beginner store.Beginner
	events   *event.Store
	hub      realtime.Publisher
	relay    *companionrelay.Coordinator
	rdb      *redis.Client

	// resolveClient defaults to creds.Resolve (the real BYOK credential
	// path — an agent loop has no env-var fallback the way an ordinary
	// reply does, since a sustained loop is deliberately never the "quick
	// dev key" path) but is a field, not a direct call, so a test can
	// substitute a fake provider.Agent — the same seam
	// internal/message.Orchestrator.newProviderClient already established
	// for exactly this reason.
	resolveClient func(ctx context.Context, roomOwnerID *uuid.UUID, providerName agent.Provider) (provider.Agent, error)

	mu     sync.Mutex
	byRoom map[uuid.UUID]*Session
	byID   map[uuid.UUID]*Session
}

func NewManager(
	rooms *room.Store,
	agents *agent.Store,
	tasks *task.Store,
	creds *credentials.Store,
	beginner store.Beginner,
	events *event.Store,
	hub realtime.Publisher,
	relay *companionrelay.Coordinator,
	rdb *redis.Client,
) *Manager {
	return &Manager{
		rooms: rooms, agents: agents, tasks: tasks, creds: creds,
		beginner: beginner, events: events, hub: hub, relay: relay, rdb: rdb,
		resolveClient: creds.Resolve,
		byRoom:        make(map[uuid.UUID]*Session),
		byID:          make(map[uuid.UUID]*Session),
	}
}

// StartParams is everything a human must supply to start a session —
// every field required, per ADR-011's "no hard default bound."
type StartParams struct {
	RoomID           uuid.UUID
	AgentID          uuid.UUID
	Task             string
	MaxCycles        int
	DollarCapUSD     float64
	WallClockSeconds int
}

// Start validates params, resolves the acting agent's real provider
// client, registers a new Session, and launches its real cycle loop in a
// new goroutine — returning as soon as the session is registered, not
// once it finishes (a bounded session can legitimately run for the
// entire configured wall-clock limit). The session's own dedicated
// terminal is created inside that goroutine (see run in loop.go), not
// here, since that's itself a real relay round trip through the
// browser's companion connection and shouldn't block this call.
func (m *Manager) Start(ctx context.Context, p StartParams) (*Session, error) {
	if p.Task == "" {
		return nil, fmt.Errorf("agentloop: task is required")
	}
	bounds := Bounds{MaxCycles: p.MaxCycles, DollarCapUSD: p.DollarCapUSD, WallClock: time.Duration(p.WallClockSeconds) * time.Second}
	if err := bounds.Validate(); err != nil {
		return nil, err
	}

	rm, err := m.rooms.GetByID(ctx, p.RoomID)
	if err != nil {
		return nil, fmt.Errorf("agentloop: look up room: %w", err)
	}
	a, err := m.agents.GetByID(ctx, p.AgentID)
	if err != nil || a.RoomID != p.RoomID {
		return nil, fmt.Errorf("agentloop: agent %s is not in room %s", p.AgentID, p.RoomID)
	}
	client, err := m.resolveClient(ctx, rm.OwnerID, a.Provider)
	if err != nil {
		return nil, fmt.Errorf("agentloop: resolve %s's real provider credential: %w", a.Name, err)
	}

	m.mu.Lock()
	if _, exists := m.byRoom[p.RoomID]; exists {
		m.mu.Unlock()
		return nil, ErrSessionAlreadyRunning
	}

	loopCtx, cancel := context.WithCancel(context.Background())
	s := &Session{
		ID: uuid.New(), RoomID: p.RoomID, AgentID: p.AgentID, Provider: a.Provider,
		Task: p.Task, Bounds: bounds, client: client,
		StartedAt: time.Now(), cancel: cancel, State: StateRunning,
	}
	s.deadline = s.StartedAt.Add(bounds.WallClock)
	m.byRoom[p.RoomID] = s
	m.byID[s.ID] = s
	m.mu.Unlock()

	go m.run(loopCtx, s)

	return s, nil
}

// Stop ends sessionID's session immediately by canceling its real
// context — the always-visible Stop control ADR-011 requires, distinct
// from presence loss (see State's own doc comment): a human explicitly
// ending a session that's still being watched, not the session losing its
// safety gate. Cancellation aborts whatever real blocked call (a
// provider.Generate, a companionrelay.Dispatch) the loop is currently in,
// not just a check the loop happens to make on its next iteration.
func (m *Manager) Stop(sessionID uuid.UUID) error {
	m.mu.Lock()
	s, ok := m.byID[sessionID]
	m.mu.Unlock()
	if !ok {
		return ErrSessionNotFound
	}
	if !s.isRunning() {
		return nil
	}
	s.cancel()
	return nil
}

// Get returns roomID's currently-tracked session, if any — used by the
// status HTTP endpoint to answer "is a session running here" without a
// caller needing to already know a session id.
func (m *Manager) Get(roomID uuid.UUID) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.byRoom[roomID]
	return s, ok
}

// forget removes a finished session from both indexes — called once by
// run's own defer, after its terminal state is already recorded, so a new
// session can start in the same room and Get/Stop never resolve a
// finished session as if it were still live.
func (m *Manager) forget(s *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.byRoom[s.RoomID] == s {
		delete(m.byRoom, s.RoomID)
	}
	delete(m.byID, s.ID)
}
