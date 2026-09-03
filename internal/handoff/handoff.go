// Package handoff implements the HANDOFF.REQUEST / HANDOFF.ACCEPT flow.
// A handoff carries its summary, decisions, and risks inline as jsonb —
// deliberately not normalized into separate linked entities yet. That's
// the right amount of structure for one handoff at a time (design doc
// section 4).
package handoff

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/chibuike-kt/harmonia/internal/store"
)

type Status string

const (
	StatusRequested Status = "REQUESTED"
	StatusAccepted  Status = "ACCEPTED"
	StatusRejected  Status = "REJECTED"
)

// ErrNotFound is returned when no handoff matches the given ID.
var ErrNotFound = errors.New("handoff: not found")

// ErrNotRequested is returned when Accept is called on a handoff that
// isn't currently REQUESTED — already accepted, or rejected. Zero rows
// affected must mean "reject," the same reasoning as task.ErrNotClaimed:
// CLAUDE.md names handoffs explicitly alongside task claims as state
// transitions that use conditional writes, never read-then-write.
var ErrNotRequested = errors.New("handoff: not currently requested, cannot accept")

type Handoff struct {
	ID          uuid.UUID `json:"id"`
	RoomID      uuid.UUID `json:"room_id"`
	TaskID      uuid.UUID `json:"task_id"`
	FromAgentID uuid.UUID `json:"from_agent_id"`
	ToAgentID   uuid.UUID `json:"to_agent_id"`
	Summary     string    `json:"summary"`
	Completed   []string  `json:"completed"`
	Remaining   []string  `json:"remaining"`
	Artifacts   []string  `json:"artifacts"`
	Decisions   []string  `json:"decisions"`
	Risks       []string  `json:"risks"`
	Status      Status    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

type Store struct {
	pool store.Querier
}

func NewStore(pool store.Querier) *Store {
	return &Store{pool: pool}
}

// Request inserts a new handoff in REQUESTED state. A nil slice field
// marshals to JSON null, which pgx sends as SQL NULL against these
// NOT NULL jsonb columns — same fix as agent.Store.Register's capabilities.
func (s *Store) Request(ctx context.Context, h Handoff) (Handoff, error) {
	if h.Completed == nil {
		h.Completed = []string{}
	}
	if h.Remaining == nil {
		h.Remaining = []string{}
	}
	if h.Artifacts == nil {
		h.Artifacts = []string{}
	}
	if h.Decisions == nil {
		h.Decisions = []string{}
	}
	if h.Risks == nil {
		h.Risks = []string{}
	}

	err := s.pool.QueryRow(ctx, `
		INSERT INTO handoffs (room_id, task_id, from_agent_id, to_agent_id, summary,
			completed, remaining, artifacts, decisions, risks, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'REQUESTED')
		RETURNING id, created_at
	`, h.RoomID, h.TaskID, h.FromAgentID, h.ToAgentID, h.Summary,
		h.Completed, h.Remaining, h.Artifacts, h.Decisions, h.Risks,
	).Scan(&h.ID, &h.CreatedAt)
	h.Status = StatusRequested
	return h, err
}

// Accept atomically transitions a handoff from REQUESTED to ACCEPTED. The
// WHERE clause on current status is what makes concurrent accepts safe —
// the same reasoning as task.Store.Claim and task.Store.Complete.
func (s *Store) Accept(ctx context.Context, handoffID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE handoffs SET status = 'ACCEPTED' WHERE id = $1 AND status = 'REQUESTED'
	`, handoffID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotRequested
	}
	return nil
}

// GetByID fetches a single handoff. Returns ErrNotFound if no handoff
// matches.
func (s *Store) GetByID(ctx context.Context, handoffID uuid.UUID) (Handoff, error) {
	var h Handoff
	err := s.pool.QueryRow(ctx, `
		SELECT id, room_id, task_id, from_agent_id, to_agent_id, summary,
			completed, remaining, artifacts, decisions, risks, status, created_at
		FROM handoffs WHERE id = $1
	`, handoffID).Scan(
		&h.ID, &h.RoomID, &h.TaskID, &h.FromAgentID, &h.ToAgentID, &h.Summary,
		&h.Completed, &h.Remaining, &h.Artifacts, &h.Decisions, &h.Risks, &h.Status, &h.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Handoff{}, ErrNotFound
	}
	return h, err
}
