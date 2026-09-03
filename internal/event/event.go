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
	ID        int64
	RoomID    uuid.UUID
	TaskID    *uuid.UUID
	AgentID   *uuid.UUID
	Type      string
	Payload   map[string]any
	CreatedAt time.Time
}

type Store struct {
	pool store.Querier
}

func NewStore(pool store.Querier) *Store {
	return &Store{pool: pool}
}

// Record appends an event. There is no corresponding Update or Delete —
// that omission is intentional, not incomplete.
func (s *Store) Record(ctx context.Context, roomID uuid.UUID, taskID, agentID *uuid.UUID, eventType string, payload map[string]any) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO events (room_id, task_id, agent_id, type, payload)
		VALUES ($1, $2, $3, $4, $5)
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
