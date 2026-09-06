ALTER TABLE messages ADD COLUMN mentioned_agent_id uuid REFERENCES agents(id);
UPDATE messages m SET mentioned_agent_id = (
    SELECT agent_id FROM message_mentions WHERE message_id = m.id LIMIT 1
);
DROP TABLE IF EXISTS agent_action_proposals;
DROP TABLE IF EXISTS message_mentions;
ALTER TABLE rooms DROP COLUMN IF EXISTS agent_cascading_enabled;
