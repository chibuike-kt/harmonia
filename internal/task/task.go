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
	"github.com/jackc/pgx/v5"

	"github.com/chibuike-kt/harmonia/internal/store"
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

// ErrNotClaimed is returned when Complete is called on a task that isn't
// currently CLAIMED — already completed, or never claimed. Zero rows
// affected must mean "reject," never "silently do nothing," or two
// concurrent completions could both look like they succeeded.
var ErrNotClaimed = errors.New("task: not currently claimed, cannot complete")

// ErrNotFound is returned when no task matches the given ID.
var ErrNotFound = errors.New("task: not found")

// Event types recorded to the audit trail for each task lifecycle
// transition (see internal/event). Distinct from the AACP protocol.Operation
// constants — these are the events.type values, not the message type on
// the wire.
const (
	EventTaskCreated   = "TASK_CREATED"
	EventTaskClaimed   = "TASK_CLAIMED"
	EventTaskCompleted = "TASK_COMPLETED"
)

type Task struct {
	ID           uuid.UUID  `json:"id"`
	RoomID       uuid.UUID  `json:"room_id"`
	OwnerAgentID *uuid.UUID `json:"owner_agent_id,omitempty"`
	ParentTaskID *uuid.UUID `json:"parent_task_id,omitempty"`
	Objective    string     `json:"objective"`
	Status       Status     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	ClaimedAt    *time.Time `json:"claimed_at,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

type Store struct {
	pool store.Querier
}

func NewStore(pool store.Querier) *Store {
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

// Complete atomically transitions a task from CLAIMED to COMPLETED. The
// WHERE clause on current status is what makes two concurrent completions
// safe, the same reasoning as Claim's WHERE clause on QUEUED — not
// application-level locking, and not a read-then-write check beforehand.
func (s *Store) Complete(ctx context.Context, taskID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE tasks
		SET status = 'COMPLETED', completed_at = now()
		WHERE id = $1 AND status = 'CLAIMED'
	`, taskID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotClaimed
	}
	return nil
}

// GetByID fetches a single task. Returns ErrNotFound if no task matches.
func (s *Store) GetByID(ctx context.Context, taskID uuid.UUID) (Task, error) {
	var t Task
	err := s.pool.QueryRow(ctx, `
		SELECT id, room_id, owner_agent_id, parent_task_id, objective, status, created_at, claimed_at, completed_at
		FROM tasks WHERE id = $1
	`, taskID).Scan(
		&t.ID, &t.RoomID, &t.OwnerAgentID, &t.ParentTaskID, &t.Objective, &t.Status, &t.CreatedAt, &t.ClaimedAt, &t.CompletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	return t, err
}
