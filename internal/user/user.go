// Package user implements human account identity for Phase 2: OAuth-only
// sign-in (GitHub, Google), no password auth. A user authenticates to the
// platform via a session, never via the agent API-key system in
// internal/agent — the two auth layers are deliberately kept separate
// (see ADR-002).
package user

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when no user matches the given lookup.
var ErrNotFound = errors.New("user: not found")

type User struct {
	ID          uuid.UUID `json:"id"`
	GitHubID    *string   `json:"github_id,omitempty"`
	GoogleID    *string   `json:"google_id,omitempty"`
	Username    string    `json:"username"`
	DisplayName *string   `json:"display_name,omitempty"`
	AvatarURL   *string   `json:"avatar_url,omitempty"`
	Email       *string   `json:"email,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// GetByID fetches a single user. Returns ErrNotFound if no user matches.
func (s *Store) GetByID(ctx context.Context, userID uuid.UUID) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT id, github_id, google_id, username, display_name, avatar_url, email, created_at
		FROM users WHERE id = $1
	`, userID).Scan(&u.ID, &u.GitHubID, &u.GoogleID, &u.Username, &u.DisplayName, &u.AvatarURL, &u.Email, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}
