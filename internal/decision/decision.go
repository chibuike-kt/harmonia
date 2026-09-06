// Package decision implements the room info panel's "decisions"
// section: a human manually pinning a specific message as a decision
// worth surfacing. There is deliberately no AI-driven extraction here —
// see the build brief for the room info panel's addendum. A decision
// exists only because a person chose to pin it.
package decision

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/store"
)

type Decision struct {
	ID        uuid.UUID `json:"id"`
	RoomID    uuid.UUID `json:"room_id"`
	MessageID uuid.UUID `json:"message_id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type Store struct {
	pool store.Querier
}

func NewStore(pool store.Querier) *Store {
	return &Store{pool: pool}
}

// Pin records messageID's content as a decision for roomID. Idempotent
// by design (migrations/0008_decisions.up.sql's UNIQUE(message_id)):
// pinning an already-pinned message returns the existing decision
// rather than erroring or creating a duplicate row — a double-click or
// a retried request is harmless, not a conflict to reject.
func (s *Store) Pin(ctx context.Context, roomID, messageID uuid.UUID, content string) (Decision, error) {
	var d Decision
	err := s.pool.QueryRow(ctx, `
		INSERT INTO decisions (room_id, message_id, content)
		VALUES ($1, $2, $3)
		ON CONFLICT (message_id) DO UPDATE SET message_id = decisions.message_id
		RETURNING id, room_id, message_id, content, created_at
	`, roomID, messageID, content).Scan(&d.ID, &d.RoomID, &d.MessageID, &d.Content, &d.CreatedAt)
	return d, err
}

// ListByRoom returns roomID's pinned decisions, oldest first — the room
// info panel's own decisions section.
func (s *Store) ListByRoom(ctx context.Context, roomID uuid.UUID) ([]Decision, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, room_id, message_id, content, created_at
		FROM decisions WHERE room_id = $1 ORDER BY created_at ASC
	`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	decisions := make([]Decision, 0)
	for rows.Next() {
		var d Decision
		if err := rows.Scan(&d.ID, &d.RoomID, &d.MessageID, &d.Content, &d.CreatedAt); err != nil {
			return nil, err
		}
		decisions = append(decisions, d)
	}
	return decisions, rows.Err()
}
