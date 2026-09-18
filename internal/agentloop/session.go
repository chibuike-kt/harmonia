package agentloop

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/provider"
)

// State is a Session's own real lifecycle state — every value here is one
// of ADR-011's real, non-silent stopping conditions except Running itself.
type State string

const (
	StateRunning         State = "running"
	StateCompleted       State = "completed"
	StateStoppedBound    State = "stopped_bound"
	StateStoppedPresence State = "stopped_presence"
	StateStoppedManual   State = "stopped_manual"
	StateFailed          State = "failed"
)

// Session is one real sustained agentic loop running against one real
// room's IDE — RoomID doubles as this package's own uniqueness key
// (Manager allows at most one live Session per room at a time), matching
// ADR-011's "scoped to the IDE" framing: an IDE session is one paired
// room, so one loop per room is one loop per IDE session.
type Session struct {
	ID       uuid.UUID
	RoomID   uuid.UUID
	AgentID  uuid.UUID
	Provider agent.Provider
	Task     string
	Bounds   Bounds

	client provider.Agent

	StartedAt time.Time
	deadline  time.Time

	cancel context.CancelFunc

	mu         sync.Mutex
	Cycle      int
	SpendUSD   float64
	State      State
	Message    string
	TerminalID string
}

// snapshot reads every field a status publish or an HTTP response needs
// in one lock — the loop goroutine is the only writer, but both the HTTP
// handlers and the loop's own narration/publish helpers read concurrently
// with it.
type snapshot struct {
	Cycle      int
	SpendUSD   float64
	State      State
	Message    string
	TerminalID string
}

func (s *Session) snapshot() snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return snapshot{Cycle: s.Cycle, SpendUSD: s.SpendUSD, State: s.State, Message: s.Message, TerminalID: s.TerminalID}
}

func (s *Session) setTerminalID(id string) {
	s.mu.Lock()
	s.TerminalID = id
	s.mu.Unlock()
}

func (s *Session) terminalID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.TerminalID
}

func (s *Session) addSpend(usd float64) {
	s.mu.Lock()
	s.SpendUSD += usd
	s.mu.Unlock()
}

func (s *Session) incrementCycle() int {
	s.mu.Lock()
	s.Cycle++
	n := s.Cycle
	s.mu.Unlock()
	return n
}

// finish records st as this Session's real terminal state — called
// exactly once per Session, by the loop goroutine's own defer, so every
// exit path (completed, a bound hit, presence lost, a manual stop, or a
// real unrecoverable error) leaves State/Message in the same consistent
// place a status snapshot or the stop HTTP handler reads from.
func (s *Session) finish(st State, message string) {
	s.mu.Lock()
	s.State = st
	s.Message = message
	s.mu.Unlock()
}

func (s *Session) isRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.State == StateRunning
}
