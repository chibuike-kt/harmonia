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
	// PreferredName and CustomInstructions are pure user preferences
	// (ADR-005) — never touched by the OAuth upsert path, only ever set
	// via UpdateMe. PreferredName is what agents/the app call someone
	// casually, distinct from Username (the OAuth handle) and
	// DisplayName (their real name). CustomInstructions is prepended
	// into every agent Generate call's context — see
	// internal/message's own context-building code.
	PreferredName      *string `json:"preferred_name,omitempty"`
	CustomInstructions *string `json:"custom_instructions,omitempty"`
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// userColumns is the one place every User-returning query's column list
// is spelled out, so a query and its Scan can't drift out of sync as
// fields get added (see internal/message's messageColumns for the same
// reasoning).
const userColumns = `id, github_id, google_id, username, display_name, avatar_url, email, created_at, preferred_name, custom_instructions`

func scanUser(row interface {
	Scan(dest ...any) error
}) (User, error) {
	var u User
	err := row.Scan(
		&u.ID, &u.GitHubID, &u.GoogleID, &u.Username, &u.DisplayName, &u.AvatarURL, &u.Email, &u.CreatedAt,
		&u.PreferredName, &u.CustomInstructions,
	)
	return u, err
}

// GetByID fetches a single user. Returns ErrNotFound if no user matches.
func (s *Store) GetByID(ctx context.Context, userID uuid.UUID) (User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// UpsertByGitHubID creates the user identified by githubID on first
// login, or refreshes their profile fields from GitHub on every
// subsequent one. preferred_name/custom_instructions are deliberately
// absent from both the insert and the conflict update — they're pure
// user preferences with no OAuth-provider equivalent to sync from, so a
// re-login must never touch them.
func (s *Store) UpsertByGitHubID(ctx context.Context, githubID, username string, displayName, avatarURL, email *string) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, `
		INSERT INTO users (github_id, username, display_name, avatar_url, email)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (github_id) DO UPDATE
		SET username = EXCLUDED.username, display_name = EXCLUDED.display_name,
			avatar_url = EXCLUDED.avatar_url, email = EXCLUDED.email
		RETURNING `+userColumns,
		githubID, username, displayName, avatarURL, email,
	))
}

// UpsertByGoogleID creates the user identified by googleID on first
// login, or refreshes their profile fields from Google on every
// subsequent one. Same preferred_name/custom_instructions exclusion as
// UpsertByGitHubID, for the same reason.
func (s *Store) UpsertByGoogleID(ctx context.Context, googleID, username string, displayName, avatarURL, email *string) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, `
		INSERT INTO users (google_id, username, display_name, avatar_url, email)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (google_id) DO UPDATE
		SET username = EXCLUDED.username, display_name = EXCLUDED.display_name,
			avatar_url = EXCLUDED.avatar_url, email = EXCLUDED.email
		RETURNING `+userColumns,
		googleID, username, displayName, avatarURL, email,
	))
}

// UpdateMe updates userID's own mutable settings. A nil argument leaves
// that field unchanged; username can never be set to empty since the
// column is NOT NULL and UpdateMeHandler rejects an empty string before
// this is ever called. preferredName/customInstructions have no such
// restriction — both are nullable, free-text preferences, and an empty
// string is a legitimate way to clear either one.
func (s *Store) UpdateMe(ctx context.Context, userID uuid.UUID, username, displayName, preferredName, customInstructions *string) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, `
		UPDATE users
		SET username = COALESCE($2, username),
		    display_name = COALESCE($3, display_name),
		    preferred_name = COALESCE($4, preferred_name),
		    custom_instructions = COALESCE($5, custom_instructions)
		WHERE id = $1
		RETURNING `+userColumns,
		userID, username, displayName, preferredName, customInstructions,
	))
}
