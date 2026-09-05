// Package event implements the append-only audit trail. Nothing in this
// package ever issues UPDATE or DELETE against the events table — that
// guarantee is also enforced at the database role level (see
// migrations/0001_init.up.sql). This is the audit trail from day one,
// not bolted on later (design doc section 9).
package event

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/store"
)

type Event struct {
	ID        int64          `json:"id"`
	RoomID    uuid.UUID      `json:"room_id"`
	TaskID    *uuid.UUID     `json:"task_id,omitempty"`
	AgentID   *uuid.UUID     `json:"agent_id,omitempty"`
	Type      string         `json:"type"`
	Payload   map[string]any `json:"payload"`
	CreatedAt time.Time      `json:"created_at"`
}

type Store struct {
	pool store.Querier
}

func NewStore(pool store.Querier) *Store {
	return &Store{pool: pool}
}

// Record appends an event. There is no corresponding Update or Delete —
// that omission is intentional, not incomplete.
//
// It also bumps rooms.last_activity_at for roomID — every real write to
// this package's callers (task/handoff transactions, the context-engine's
// standalone call against the bare pool) is exactly what "activity" means
// for dashboard recency sorting (see docs/design/dashboard-build-brief.md),
// so this is the one choke point every one of those writes already passes
// through, rather than a separate call each handler would otherwise need
// to remember.
//
// The two writes are one SQL statement (a data-modifying CTE), not two
// sequential Exec calls, deliberately: s.pool is store.Querier, which has
// no Begin — Record cannot open its own transaction, and can't assume its
// caller opened one either (the context-engine's call passes the bare
// pool, no ambient transaction at all). A single statement is atomic on
// its own regardless of which case applies: when s.pool is a transaction
// it also naturally still commits/rolls back with the rest of that
// transaction, and when it's the bare pool the event insert and the
// rooms update can't land as a partial pair.
func (s *Store) Record(ctx context.Context, roomID uuid.UUID, taskID, agentID *uuid.UUID, eventType string, payload map[string]any) error {
	_, err := s.pool.Exec(ctx, `
		WITH inserted AS (
			INSERT INTO events (room_id, task_id, agent_id, type, payload)
			VALUES ($1, $2, $3, $4, $5)
		)
		UPDATE rooms SET last_activity_at = now() WHERE id = $1
	`, roomID, taskID, agentID, eventType, payload)
	return err
}

// ListByRoom returns the full ordered event history for a room — the
// queryable audit trail required by the Milestone 1 acceptance test
// (design doc section 11, step 8).
func (s *Store) ListByRoom(ctx context.Context, roomID uuid.UUID) ([]Event, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, room_id, task_id, agent_id, type, payload, created_at
		FROM events WHERE room_id = $1 ORDER BY created_at ASC
	`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.RoomID, &e.TaskID, &e.AgentID, &e.Type, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
