-- Conversational chat: human/agent messages in a room. See
-- docs/adr/ADR-004-conversational-chat-and-first-orchestration.md.

CREATE TABLE messages (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id            uuid NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    sender_kind        text NOT NULL,
    user_id            uuid REFERENCES users(id),
    agent_id           uuid REFERENCES agents(id),
    mentioned_agent_id uuid REFERENCES agents(id),
    reply_to_message_id uuid REFERENCES messages(id),
    content            text NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT messages_sender_kind_check CHECK (sender_kind IN ('human', 'agent')),
    -- A message is authored by exactly one kind of sender — never both,
    -- never neither.
    CONSTRAINT messages_sender_matches_kind CHECK (
        (sender_kind = 'human' AND user_id IS NOT NULL AND agent_id IS NULL)
        OR
        (sender_kind = 'agent' AND agent_id IS NOT NULL AND user_id IS NULL)
    )
);

CREATE INDEX idx_messages_room_id_created_at ON messages(room_id, created_at);
