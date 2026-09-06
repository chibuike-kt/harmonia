// Package message implements the conversational chat layer: human and
// agent messages in a room, and the @mention invocation loop that
// generates an agent's reply. See
// docs/adr/ADR-004-conversational-chat-and-first-orchestration.md.
package message

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/chibuike-kt/harmonia/internal/store"
)

// ErrNotFound is returned when no message matches the given ID.
var ErrNotFound = errors.New("message: not found")

// SenderKind distinguishes who authored a message — exactly one of the
// two, enforced by messages_sender_matches_kind at the database level
// too (migrations/0006_messages.up.sql), not just here.
type SenderKind string

const (
	SenderHuman SenderKind = "human"
	SenderAgent SenderKind = "agent"
)

// recencyLimit bounds how many of a room's most recent messages
// ListByRoom returns — the "plain recency window" ADR-004 specifies as
// v1's entire context-assembly story, not a relevance-ranked engine.
// 50 is generous for the single-agent conversations this phase builds
// depth for, without handing Generate an unbounded, ever-growing prompt.
const recencyLimit = 50

type Message struct {
	ID               uuid.UUID  `json:"id"`
	RoomID           uuid.UUID  `json:"room_id"`
	SenderKind       SenderKind `json:"sender_kind"`
	UserID           *uuid.UUID `json:"user_id,omitempty"`
	AgentID          *uuid.UUID `json:"agent_id,omitempty"`
	MentionedAgentID *uuid.UUID `json:"mentioned_agent_id,omitempty"`
	ReplyToMessageID *uuid.UUID `json:"reply_to_message_id,omitempty"`
	Content          string     `json:"content"`
	CreatedAt        time.Time  `json:"created_at"`
	// InputTokens/OutputTokens are only ever set for sender_kind =
	// 'agent' rows — the real usage the generation behind this message
	// actually consumed (see provider.GenerateResponse). Nil for a human
	// message, which has no generation behind it to meter.
	InputTokens  *int `json:"input_tokens,omitempty"`
	OutputTokens *int `json:"output_tokens,omitempty"`
}

type Store struct {
	pool store.Querier
}

// NewStore accepts store.Querier rather than *pgxpool.Pool so a caller
// can bind a Store to a transaction in progress — same reasoning as
// agent.NewStore and task.NewStore.
func NewStore(pool store.Querier) *Store {
	return &Store{pool: pool}
}

const messageColumns = `id, room_id, sender_kind, user_id, agent_id, mentioned_agent_id, reply_to_message_id, content, created_at, input_tokens, output_tokens`

func scanMessage(row interface {
	Scan(dest ...any) error
}) (Message, error) {
	var m Message
	err := row.Scan(
		&m.ID, &m.RoomID, &m.SenderKind, &m.UserID, &m.AgentID,
		&m.MentionedAgentID, &m.ReplyToMessageID, &m.Content, &m.CreatedAt,
		&m.InputTokens, &m.OutputTokens,
	)
	return m, err
}

// CreateHuman inserts a message posted by userID, optionally @mentioning
// mentionedAgentID — the structured field the invocation loop keys off
// of, never text parsing (see ADR-004).
func (s *Store) CreateHuman(ctx context.Context, roomID, userID uuid.UUID, content string, mentionedAgentID *uuid.UUID) (Message, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO messages (room_id, sender_kind, user_id, mentioned_agent_id, content)
		VALUES ($1, 'human', $2, $3, $4)
		RETURNING `+messageColumns,
		roomID, userID, mentionedAgentID, content,
	)
	return scanMessage(row)
}

// CreateAgent inserts an agent-authored message: a generated reply on
// success, or a visible failure explanation on error — ADR-004 requires
// both to be real messages, never a silent drop, so both go through this
// one insert path. replyToMessageID is always the human message that
// triggered the invocation, so the UI can show "replying to X" once the
// conversation has moved on before the reply lands. inputTokens/
// outputTokens are the real usage from the provider response that
// produced content — nil on the failure path, where no successful
// generation exists to meter.
func (s *Store) CreateAgent(ctx context.Context, roomID, agentID uuid.UUID, content string, replyToMessageID uuid.UUID, inputTokens, outputTokens *int) (Message, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO messages (room_id, sender_kind, agent_id, reply_to_message_id, content, input_tokens, output_tokens)
		VALUES ($1, 'agent', $2, $3, $4, $5, $6)
		RETURNING `+messageColumns,
		roomID, agentID, replyToMessageID, content, inputTokens, outputTokens,
	)
	return scanMessage(row)
}

// GetByID fetches a single message. Returns ErrNotFound if no message
// matches — used by decision.Store.PinHandler to validate that the
// message being pinned actually belongs to the room it's being pinned
// in (the same non-leaking pattern every other cross-resource lookup in
// this API uses).
func (s *Store) GetByID(ctx context.Context, messageID uuid.UUID) (Message, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+messageColumns+` FROM messages WHERE id = $1`, messageID)
	m, err := scanMessage(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	return m, err
}

// ListByRoom returns roomID's most recent messages, oldest first — ready
// to format directly as a conversation for provider.GenerateRequest, and
// as the SSE snapshot's message history. Capped at recencyLimit.
func (s *Store) ListByRoom(ctx context.Context, roomID uuid.UUID) ([]Message, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+messageColumns+`
		FROM (
			SELECT `+messageColumns+`
			FROM messages
			WHERE room_id = $1
			ORDER BY created_at DESC
			LIMIT $2
		) recent
		ORDER BY created_at ASC
	`, roomID, recencyLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := make([]Message, 0)
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// CountByRoom returns how many messages exist in roomID — used to
// detect "this insert was the room's very first message," the trigger
// for the async auto-title job (ADR-004's addendum). A plain count
// rather than reusing ListByRoom's result length: the caller only needs
// the number, not the rows, and a room's message count is unbounded by
// recencyLimit in a way ListByRoom deliberately isn't.
func (s *Store) CountByRoom(ctx context.Context, roomID uuid.UUID) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE room_id = $1`, roomID).Scan(&count)
	return count, err
}
