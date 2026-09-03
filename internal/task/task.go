// Package task implements the Milestone 1 task lifecycle:
// CREATED -> QUEUED -> CLAIMED -> RUNNING -> COMPLETED.
// FAILED and BLOCKED exist in the schema as terminal/interrupt states but
// are not exercised until failure handling is built (deferred, see design
// doc section 12).
package task

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Status string

const (
	StatusCreated   Status = "CREATED"
	StatusQueued    Status = "QUEUED"
	StatusClaimed   Status = "CLAIMED"
	StatusRunning   Status = "RUNNING"
	StatusCompleted Status = "COMPLETED"
	StatusFailed    Status = "FAILED"
	StatusBlocked   Status = "BLOCKED"
)

var ErrAlreadyClaimed = errors.New("task: already claimed by another agent")

type Task struct {
	ID            uuid.UUID
	RoomID        uuid.UUID
	OwnerAgentID  *uuid.UUID
	ParentTaskID  *uuid.UUID
	Objective     string
	Status        Status
	CreatedAt     time.Time
	ClaimedAt     *time.Time
	CompletedAt   *time.Time
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Create inserts a new task in QUEUED state, ready to be claimed.
func (s *Store) Create(ctx context.Context, roomID uuid.UUID, objective string, parentTaskID *uuid.UUID) (Task, error) {
	var t Task
	err := s.pool.QueryRow(ctx, `
		INSERT INTO tasks (room_id, parent_task_id, objective, status)
		VALUES ($1, $2, $3, 'QUEUED')
		RETURNING id, room_id, parent_task_id, objective, status, created_at
	`, roomID, parentTaskID, objective).Scan(
		&t.ID, &t.RoomID, &t.ParentTaskID, &t.Objective, &t.Status, &t.CreatedAt,
	)
	return t, err
}

// Claim atomically transitions a task from QUEUED to CLAIMED. This is the
// entire concurrency story for Milestone 1 (design doc section 7) — the
// WHERE clause on current status is what makes the race safe, not
// application-level locking.
func (s *Store) Claim(ctx context.Context, taskID, agentID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE tasks
		SET status = 'CLAIMED', owner_agent_id = $1, claimed_at = now()
		WHERE id = $2 AND status = 'QUEUED'
	`, agentID, taskID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAlreadyClaimed
	}
	return nil
}

// Complete transitions a claimed/running task to COMPLETED.
func (s *Store) Complete(ctx context.Context, taskID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE tasks
		SET status = 'COMPLETED', completed_at = now()
		WHERE id = $1
	`, taskID)
	return err
}
