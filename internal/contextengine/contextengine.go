// Package contextengine (assembly, not Go's context.Context) implements the
// minimal CONTEXT.REQUEST / CONTEXT.RESPONSE lookup for Milestone 1: a
// direct fetch of one task's result. No relevance ranking, no memory
// layer, no context-window budgeting yet — those require a project
// memory table that doesn't exist in this milestone (deferred, design
// doc section 12).
package contextengine

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// TaskResult is the minimal payload returned for a CONTEXT.REQUEST about
// a specific task — just enough for the acceptance test's step 5.
type TaskResult struct {
	TaskID    uuid.UUID
	Objective string
	Status    string
}

func (s *Store) TaskByID(ctx context.Context, taskID uuid.UUID) (TaskResult, error) {
	var r TaskResult
	err := s.pool.QueryRow(ctx, `
		SELECT id, objective, status FROM tasks WHERE id = $1
	`, taskID).Scan(&r.TaskID, &r.Objective, &r.Status)
	return r, err
}
