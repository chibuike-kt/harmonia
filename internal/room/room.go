// Package room implements the minimal room entity for Milestone 1 — no
// UI, no participant permissions model yet (deferred, design doc section 12).
package room

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when no room matches the given lookup.
var ErrNotFound = errors.New("room: not found")

type Room struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Status string    `json:"status"`
	// OwnerID is nullable only for rooms created before Phase 2 — every
	// room created through CreateHandler has one (see ADR-002).
	OwnerID   *uuid.UUID `json:"owner_id,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Create creates a room, optionally owned by ownerID. ownerID is nil
// only for pre-Phase-2 callers (other packages' test fixtures that need
// a room to exist but don't exercise ownership); CreateHandler always
// passes the authenticated caller's id.
func (s *Store) Create(ctx context.Context, ownerID *uuid.UUID, name string) (Room, error) {
	var r Room
	err := s.pool.QueryRow(ctx, `
		INSERT INTO rooms (name, status, owner_id) VALUES ($1, 'active', $2)
		RETURNING id, name, status, owner_id, created_at
	`, name, ownerID).Scan(&r.ID, &r.Name, &r.Status, &r.OwnerID, &r.CreatedAt)
	return r, err
}

// GetByID fetches a single room. Returns ErrNotFound if no room matches.
func (s *Store) GetByID(ctx context.Context, roomID uuid.UUID) (Room, error) {
	var r Room
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, status, owner_id, created_at FROM rooms WHERE id = $1
	`, roomID).Scan(&r.ID, &r.Name, &r.Status, &r.OwnerID, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Room{}, ErrNotFound
	}
	return r, err
}

// Summary is one row of GET /v1/rooms — deliberately not the full Room
// shape (no owner_id: this endpoint is already scoped to the caller's
// own rooms, and status/name are all a dashboard room-list item needs
// beyond recency and live state).
type Summary struct {
	ID              uuid.UUID `json:"id"`
	Name            string    `json:"name"`
	LastActivityAt  time.Time `json:"last_activity_at"`
	HasRunningAgent bool      `json:"has_running_agent"`
}

// ListByOwner returns ownerID's rooms, most recently active first. The
// EXISTS subquery answers "does this room have a running agent right
// now" per row without a second round-trip per room — one query
// regardless of how many rooms ownerID has, not N+1.
func (s *Store) ListByOwner(ctx context.Context, ownerID uuid.UUID) ([]Summary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.name, r.last_activity_at,
		       EXISTS (
		           SELECT 1 FROM agents a WHERE a.room_id = r.id AND a.status = 'running'
		       ) AS has_running_agent
		FROM rooms r
		WHERE r.owner_id = $1
		ORDER BY r.last_activity_at DESC
	`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// A brand-new user with zero rooms gets [] in the response, not a
	// JSON null a frontend can't spread — same nil-slice guard as
	// event.Store.ListByRoom's own handler needed for the same reason.
	summaries := make([]Summary, 0)
	for rows.Next() {
		var sm Summary
		if err := rows.Scan(&sm.ID, &sm.Name, &sm.LastActivityAt, &sm.HasRunningAgent); err != nil {
			return nil, err
		}
		summaries = append(summaries, sm)
	}
	return summaries, rows.Err()
}
