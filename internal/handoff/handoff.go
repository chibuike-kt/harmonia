// Package handoff implements the HANDOFF.REQUEST / HANDOFF.ACCEPT flow.
// A handoff carries its summary, decisions, and risks inline as jsonb —
// deliberately not normalized into separate linked entities yet. That's
// the right amount of structure for one handoff at a time (design doc
// section 4).
package handoff

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Status string

const (
	StatusRequested Status = "REQUESTED"
	StatusAccepted  Status = "ACCEPTED"
	StatusRejected  Status = "REJECTED"
)

type Handoff struct {
	ID           uuid.UUID
	RoomID       uuid.UUID
	TaskID       uuid.UUID
	FromAgentID  uuid.UUID
	ToAgentID    uuid.UUID
	Summary      string
	Completed    []string
	Remaining    []string
	Artifacts    []string
	Decisions    []string
	Risks        []string
	Status       Status
	CreatedAt    time.Time
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Request(ctx context.Context, h Handoff) (Handoff, error) {
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

func (s *Store) Accept(ctx context.Context, handoffID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE handoffs SET status = 'ACCEPTED' WHERE id = $1
	`, handoffID)
	return err
}
