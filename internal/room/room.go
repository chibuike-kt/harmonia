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
	// PinnedAt is nullable — not-null means pinned, same style as
	// sessions.revoked_at, not a separate boolean column.
	PinnedAt *time.Time `json:"pinned_at,omitempty"`
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
		RETURNING id, name, status, owner_id, created_at, pinned_at
	`, name, ownerID).Scan(&r.ID, &r.Name, &r.Status, &r.OwnerID, &r.CreatedAt, &r.PinnedAt)
	return r, err
}

// GetByID fetches a single room. Returns ErrNotFound if no room matches.
func (s *Store) GetByID(ctx context.Context, roomID uuid.UUID) (Room, error) {
	var r Room
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, status, owner_id, created_at, pinned_at FROM rooms WHERE id = $1
	`, roomID).Scan(&r.ID, &r.Name, &r.Status, &r.OwnerID, &r.CreatedAt, &r.PinnedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Room{}, ErrNotFound
	}
	return r, err
}

// Update partially updates a room's name and/or pinned state — nil
// leaves that field unchanged, same COALESCE pattern as
// internal/user.Store.UpdateMe. pinned is a bool at this API boundary
// (what a PATCH body naturally carries) but maps to the nullable
// pinned_at timestamp underneath: true sets it to now(), false clears
// it, nil (omitted in the request) leaves it alone. Returns ErrNotFound
// if no room matches roomID — ownership is the caller's job, same as
// every other room-scoped handler (see realtime.StreamHandler).
func (s *Store) Update(ctx context.Context, roomID uuid.UUID, name *string, pinned *bool) (Room, error) {
	var r Room
	err := s.pool.QueryRow(ctx, `
		UPDATE rooms
		SET name = COALESCE($2, name),
		    pinned_at = CASE
		        WHEN $3::boolean IS NULL THEN pinned_at
		        WHEN $3::boolean THEN now()
		        ELSE NULL
		    END
		WHERE id = $1
		RETURNING id, name, status, owner_id, created_at, pinned_at
	`, roomID, name, pinned).Scan(&r.ID, &r.Name, &r.Status, &r.OwnerID, &r.CreatedAt, &r.PinnedAt)
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
	ID              uuid.UUID  `json:"id"`
	Name            string     `json:"name"`
	LastActivityAt  time.Time  `json:"last_activity_at"`
	HasRunningAgent bool       `json:"has_running_agent"`
	PinnedAt        *time.Time `json:"pinned_at,omitempty"`
}

// ListByOwner returns ownerID's rooms, pinned first, then most recently
// active first within each group. The EXISTS subquery answers "does
// this room have a running agent right now" per row without a second
// round-trip per room — one query regardless of how many rooms ownerID
// has, not N+1.
func (s *Store) ListByOwner(ctx context.Context, ownerID uuid.UUID) ([]Summary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.name, r.last_activity_at, r.pinned_at,
		       EXISTS (
		           SELECT 1 FROM agents a WHERE a.room_id = r.id AND a.status = 'running'
		       ) AS has_running_agent
		FROM rooms r
		WHERE r.owner_id = $1
		ORDER BY (r.pinned_at IS NULL), r.last_activity_at DESC
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
		if err := rows.Scan(&sm.ID, &sm.Name, &sm.LastActivityAt, &sm.PinnedAt, &sm.HasRunningAgent); err != nil {
			return nil, err
		}
		summaries = append(summaries, sm)
	}
	return summaries, rows.Err()
}

// DeleteCascade permanently deletes roomID and everything that cascades
// from it (agents, tasks, events, handoffs — see 0001_init.up.sql's
// ON DELETE CASCADE chain), via the delete_room_cascade SQL function
// added in migrations/0005_harmonia_app_role.up.sql.
//
// This goes through that function rather than a plain
// "DELETE FROM rooms" because the application's own database role
// (harmonia_app) has no DELETE on events — by design, events are
// append-only at the database level, not just by application
// convention (see that migration's own comments). delete_room_cascade
// is SECURITY DEFINER, so it runs this one specific, narrow delete with
// the elevated privilege it needs, without the application role ever
// holding a general DELETE grant on events itself.
//
// Ownership is the caller's job, same as every other room-scoped
// write — this performs no authorization check of its own.
func (s *Store) DeleteCascade(ctx context.Context, roomID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `SELECT delete_room_cascade($1)`, roomID)
	return err
}
