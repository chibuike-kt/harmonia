// Package room implements the minimal room entity for Milestone 1 — no
// UI, no participant permissions model yet (deferred, design doc section 12).
package room

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Room struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Create(ctx context.Context, name string) (Room, error) {
	var r Room
	err := s.pool.QueryRow(ctx, `
		INSERT INTO rooms (name, status) VALUES ($1, 'active')
		RETURNING id, name, status, created_at
	`, name).Scan(&r.ID, &r.Name, &r.Status, &r.CreatedAt)
	return r, err
}
