// Package actionproposal implements ADR-006 batch C's pending,
// human-approvable action proposals. An agent proposing an action (today,
// only request_handoff) is a different question from whether the
// receiving agent later accepts or rejects the resulting handoff
// (handoffs.status's own REQUESTED/ACCEPTED/REJECTED state machine) — so
// this is its own table and its own state machine, never blurred into
// that one. create_task has no proposal step at all: ADR-006 has it
// execute immediately, the same as a human hitting POST /v1/tasks
// directly.
package actionproposal

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/chibuike-kt/harmonia/internal/store"
)

type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusRejected Status = "rejected"
)

type ActionType string

const (
	// ActionRequestHandoff is the only action_type the database's own
	// CHECK constraint allows today (migrations/0011_multi_agent_dynamics
	// .up.sql) — create_task never reaches this table at all.
	ActionRequestHandoff ActionType = "request_handoff"
)

// Event types recorded to the audit trail for each proposal
// transition — distinct from protocol.Operation, the wire message type.
const (
	EventActionProposed = "ACTION_PROPOSED"
	EventActionResolved = "ACTION_RESOLVED"
)

// ErrNotFound is returned when no proposal matches the given ID.
var ErrNotFound = errors.New("actionproposal: not found")

// ErrNotPending is returned when Resolve is called on a proposal that
// isn't currently pending — already approved, or already rejected. Zero
// rows affected must mean "reject," the same conditional-write discipline
// CLAUDE.md calls out for task claims and handoffs (never read-then-write).
var ErrNotPending = errors.New("actionproposal: not currently pending, cannot resolve")

type Proposal struct {
	ID               uuid.UUID      `json:"id"`
	RoomID           uuid.UUID      `json:"room_id"`
	ProposingAgentID uuid.UUID      `json:"proposing_agent_id"`
	ActionType       ActionType     `json:"action_type"`
	Payload          map[string]any `json:"payload"`
	Status           Status         `json:"status"`
	CreatedAt        time.Time      `json:"created_at"`
	ResolvedAt       *time.Time     `json:"resolved_at,omitempty"`
}

type Store struct {
	pool store.Querier
}

// NewStore accepts store.Querier rather than *pgxpool.Pool so a caller
// can bind a Store to a transaction in progress — same reasoning as
// every other domain Store in this codebase.
func NewStore(pool store.Querier) *Store {
	return &Store{pool: pool}
}

const proposalColumns = `id, room_id, proposing_agent_id, action_type, payload, status, created_at, resolved_at`

func scanProposal(row interface {
	Scan(dest ...any) error
}) (Proposal, error) {
	var p Proposal
	err := row.Scan(&p.ID, &p.RoomID, &p.ProposingAgentID, &p.ActionType, &p.Payload, &p.Status, &p.CreatedAt, &p.ResolvedAt)
	return p, err
}

// Create inserts a new proposal in pending status. payload carries
// everything Resolve later needs to actually execute the action on
// approval — for request_handoff, the fields handoff.Store.Request
// itself takes, with to_agent_id already resolved to a real agent ID
// (never a raw name the model supplied — see internal/message's own
// resolution before this is ever called).
func (s *Store) Create(ctx context.Context, roomID, proposingAgentID uuid.UUID, actionType ActionType, payload map[string]any) (Proposal, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO agent_action_proposals (room_id, proposing_agent_id, action_type, payload)
		VALUES ($1, $2, $3, $4)
		RETURNING `+proposalColumns,
		roomID, proposingAgentID, actionType, payload,
	)
	return scanProposal(row)
}

// GetByID fetches a single proposal. Returns ErrNotFound if no proposal
// matches — used by the approve/reject handlers to validate a proposal
// actually belongs to the room it's being resolved in, the same
// non-leaking pattern every other cross-resource lookup in this API uses.
func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (Proposal, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+proposalColumns+` FROM agent_action_proposals WHERE id = $1`, id)
	p, err := scanProposal(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Proposal{}, ErrNotFound
	}
	return p, err
}

// Resolve atomically transitions id from pending to status (approved or
// rejected) — the WHERE clause on current status is what makes two
// concurrent resolutions safe, not application-level locking, the same
// reasoning task.Store.Claim's own WHERE clause already established.
func (s *Store) Resolve(ctx context.Context, id uuid.UUID, status Status) (Proposal, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE agent_action_proposals
		SET status = $2, resolved_at = now()
		WHERE id = $1 AND status = 'pending'
		RETURNING `+proposalColumns,
		id, status,
	)
	p, err := scanProposal(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Proposal{}, ErrNotPending
	}
	return p, err
}

// ListPendingByRoom returns roomID's currently-pending proposals, oldest
// first — the SSE snapshot's source for which approval cards a client
// connecting mid-session should see, without needing to replay the
// events audit trail to reconstruct "what's still pending."
func (s *Store) ListPendingByRoom(ctx context.Context, roomID uuid.UUID) ([]Proposal, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+proposalColumns+`
		FROM agent_action_proposals
		WHERE room_id = $1 AND status = 'pending'
		ORDER BY created_at ASC
	`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	proposals := make([]Proposal, 0)
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		proposals = append(proposals, p)
	}
	return proposals, rows.Err()
}
