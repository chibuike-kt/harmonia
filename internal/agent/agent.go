// Package agent implements agent identity and registration. Each agent
// authenticates as itself via a scoped API key, never as a shared system
// secret (design doc section 9).
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/chibuike-kt/harmonia/internal/store"
)

type Status string

const (
	StatusAvailable Status = "available"
	StatusAssigned  Status = "assigned"
	StatusRunning   Status = "running"
)

type Provider string

const (
	ProviderAnthropic Provider = "anthropic"
	ProviderOpenAI    Provider = "openai"
)

type Agent struct {
	ID           uuid.UUID `json:"id"`
	RoomID       uuid.UUID `json:"room_id"`
	Name         string    `json:"name"`
	Provider     Provider  `json:"provider"`
	Capabilities []string  `json:"capabilities"`
	Status       Status    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

type Store struct {
	pool store.Querier
}

// NewStore accepts store.Querier rather than *pgxpool.Pool so a caller
// can bind a Store to a transaction in progress (store.BeginTx's Tx also
// satisfies Querier) — needed so an agent's status transition lands in
// the same transaction as the task write that causes it (task/http.go's
// Claim/Complete handlers), not a second one.
func NewStore(pool store.Querier) *Store {
	return &Store{pool: pool}
}

// Register creates an agent identity scoped to one room, with a hashed
// API key. The caller is responsible for generating and returning the
// plaintext key to the agent exactly once — it is never stored or logged
// in plaintext.
func (s *Store) Register(ctx context.Context, roomID uuid.UUID, name string, provider Provider, capabilities []string, apiKeyHash string) (Agent, error) {
	if capabilities == nil {
		// A nil slice marshals to JSON null, which pgx sends as SQL NULL —
		// the capabilities column is NOT NULL. An agent with no declared
		// capabilities still needs a valid empty JSON array on insert.
		capabilities = []string{}
	}
	var a Agent
	err := s.pool.QueryRow(ctx, `
		INSERT INTO agents (room_id, name, provider, capabilities, status, api_key_hash)
		VALUES ($1, $2, $3, $4, 'available', $5)
		RETURNING id, room_id, name, provider, status, created_at
	`, roomID, name, provider, capabilities, apiKeyHash).Scan(
		&a.ID, &a.RoomID, &a.Name, &a.Provider, &a.Status, &a.CreatedAt,
	)
	a.Capabilities = capabilities
	return a, err
}

// GetByID fetches a single agent. Returns ErrNotFound if no agent matches.
func (s *Store) GetByID(ctx context.Context, agentID uuid.UUID) (Agent, error) {
	var a Agent
	var capabilities []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, room_id, name, provider, capabilities, status, created_at
		FROM agents WHERE id = $1
	`, agentID).Scan(&a.ID, &a.RoomID, &a.Name, &a.Provider, &capabilities, &a.Status, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	if err != nil {
		return Agent{}, err
	}
	if err := json.Unmarshal(capabilities, &a.Capabilities); err != nil {
		return Agent{}, err
	}
	return a, nil
}

// SetStatus transitions agentID to status — running on task claim,
// available on task complete (see task/http.go). No conditional WHERE
// here the way task/handoff status transitions have one: unlike a task
// claim or handoff accept, an agent's presence has no "someone already
// changed it, reject" race to guard against — the caller always knows
// the exact status it's setting and it's always safe to overwrite.
func (s *Store) SetStatus(ctx context.Context, agentID uuid.UUID, status Status) error {
	_, err := s.pool.Exec(ctx, `UPDATE agents SET status = $1 WHERE id = $2`, status, agentID)
	return err
}

// ListByRoom returns every agent registered in roomID — the SSE stream's
// initial snapshot uses this to know whose Redis presence key to read
// (internal/realtime.StreamHandler).
func (s *Store) ListByRoom(ctx context.Context, roomID uuid.UUID) ([]Agent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, room_id, name, provider, capabilities, status, created_at
		FROM agents WHERE room_id = $1
	`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []Agent
	for rows.Next() {
		var a Agent
		var capabilities []byte
		if err := rows.Scan(&a.ID, &a.RoomID, &a.Name, &a.Provider, &capabilities, &a.Status, &a.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(capabilities, &a.Capabilities); err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}
