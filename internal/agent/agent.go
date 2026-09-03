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
	"github.com/jackc/pgx/v5/pgxpool"
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
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
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
