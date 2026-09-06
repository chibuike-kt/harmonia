-- Multi-agent group dynamics: multi-mention, cascading opt-in, and
-- agent-initiated action proposals. See
-- docs/adr/ADR-006-multi-agent-group-dynamics.md.

CREATE TABLE message_mentions (
    message_id uuid NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    agent_id   uuid NOT NULL REFERENCES agents(id),
    PRIMARY KEY (message_id, agent_id)
);

-- Backfill existing single mentions before dropping the old column.
INSERT INTO message_mentions (message_id, agent_id)
SELECT id, mentioned_agent_id FROM messages WHERE mentioned_agent_id IS NOT NULL;

ALTER TABLE messages DROP COLUMN mentioned_agent_id;

ALTER TABLE rooms ADD COLUMN agent_cascading_enabled boolean NOT NULL DEFAULT false;

CREATE TABLE agent_action_proposals (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id            uuid NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    proposing_agent_id uuid NOT NULL REFERENCES agents(id),
    action_type        text NOT NULL,
    payload            jsonb NOT NULL,
    status             text NOT NULL DEFAULT 'pending',
    created_at         timestamptz NOT NULL DEFAULT now(),
    resolved_at        timestamptz,

    CONSTRAINT agent_action_proposals_type_check CHECK (action_type IN ('request_handoff')),
    CONSTRAINT agent_action_proposals_status_check CHECK (status IN ('pending', 'approved', 'rejected'))
);

CREATE INDEX idx_message_mentions_agent_id ON message_mentions(agent_id);
CREATE INDEX idx_agent_action_proposals_room_id ON agent_action_proposals(room_id);

-- Same grant discipline as every table since the harmonia_app split —
-- a new table needs its own grant, the blanket grant from 0005 only
-- ever covered what existed at that moment.
GRANT SELECT, INSERT, UPDATE, DELETE ON message_mentions TO harmonia_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON agent_action_proposals TO harmonia_app;
