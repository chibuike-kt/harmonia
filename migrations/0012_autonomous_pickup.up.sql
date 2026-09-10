-- Autonomous message pickup and busy-agent redirection. See
-- docs/adr/ADR-007-autonomous-pickup-and-redirect.md — batch A (busy
-- redirect) needed no schema change (it's all in the existing
-- agent_cascading_enabled-gated tool-use path); this is batch B's own
-- toggle, the atomic claim primitive phase 2 needs, and a place for
-- phase 1's own real spend to live.

-- Off by default, independent of agent_cascading_enabled: two separate
-- capabilities with fundamentally different cost profiles per the ADR,
-- gated separately on purpose.
ALTER TABLE rooms ADD COLUMN autonomous_pickup_enabled boolean NOT NULL DEFAULT false;

-- The atomic claim primitive for phase 2 (exactly one agent that
-- flagged a message "yes" in phase 1 gets to actually generate a real
-- reply) — the same "conditional UPDATE, check rows affected" idiom
-- task.Store.Claim already uses for WHERE status = 'QUEUED', just with
-- IS NULL as the "unclaimed" sentinel instead of a status column, since
-- an ordinary message has no pre-existing queued state the way a task
-- row does. pickup_claimed_by has no ON DELETE clause, matching
-- messages.agent_id's own existing bare reference to agents(id) — both
-- resolve the same way under delete_room_cascade's single-statement
-- cascade (agents and messages are both removed together via room_id,
-- so the deferred FK check on this column is satisfied by the time the
-- statement completes).
ALTER TABLE messages ADD COLUMN pickup_claimed_by uuid REFERENCES agents(id);
ALTER TABLE messages ADD COLUMN pickup_claimed_at timestamptz;

-- Phase 1's own classification calls are real spend (ADR-007 batch B,
-- build brief item 7) and need to be captured, but they are not
-- conversation turns — a classification call has no reply content, no
-- sender a human should see, and running it through the messages table
-- (even flagged/hidden) would dilute ListByRoom's recency window with
-- rows that were never meant to be conversation context, and would need
-- the frontend to special-case "a message that isn't really a message"
-- out of the timeline. A dedicated table keeps that boundary clean:
-- messages stays exactly what ADR-004 defined it as, and this is purely
-- a running cost ledger the room's cost pill (and any future usage
-- reporting) sums from directly.
CREATE TABLE agent_pickup_evaluations (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id       uuid NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    agent_id      uuid NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    input_tokens  integer NOT NULL,
    output_tokens integer NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_agent_pickup_evaluations_room_id ON agent_pickup_evaluations(room_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON agent_pickup_evaluations TO harmonia_app;
