// Package contextengine (assembly, not Go's context.Context) implements the
// minimal CONTEXT.REQUEST / CONTEXT.RESPONSE lookup for Milestone 1: a
// direct fetch of one task's result. No relevance ranking, no memory
// layer, no context-window budgeting yet — those require a project
// memory table that doesn't exist in this milestone (deferred, design
// doc section 12).
package contextengine

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when no task matches the given ID.
var ErrNotFound = errors.New("contextengine: not found")

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// TaskResult is the minimal payload returned for a CONTEXT.REQUEST about
// a specific task — just enough for the acceptance test's step 5. RoomID
// isn't part of that payload's original scope, but the HTTP layer needs it
// to reject a request for a task outside the caller's room, the same way
// every other cross-room lookup in this API does.
type TaskResult struct {
	TaskID    uuid.UUID `json:"task_id"`
	RoomID    uuid.UUID `json:"room_id"`
	Objective string    `json:"objective"`
	Status    string    `json:"status"`
}

func (s *Store) TaskByID(ctx context.Context, taskID uuid.UUID) (TaskResult, error) {
	var r TaskResult
	err := s.pool.QueryRow(ctx, `
		SELECT id, room_id, objective, status FROM tasks WHERE id = $1
	`, taskID).Scan(&r.TaskID, &r.RoomID, &r.Objective, &r.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return TaskResult{}, ErrNotFound
	}
	return r, err
}
