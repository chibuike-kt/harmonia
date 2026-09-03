// Package agent implements agent identity and registration. Each agent
// authenticates as itself via a scoped API key, never as a shared system
// secret (design doc section 9).
package agent

import (
	"context"
	"time"

	"github.com/google/uuid"
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
	ID           uuid.UUID
	RoomID       uuid.UUID
	Name         string
	Provider     Provider
	Capabilities []string
	Status       Status
	CreatedAt    time.Time
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
