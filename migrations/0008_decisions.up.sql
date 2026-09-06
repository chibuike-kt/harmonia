-- Manually-pinned decisions for a room's info panel. There is
-- deliberately no AI-driven extraction here — a human decides what's
-- worth surfacing by pinning a specific message (see the hover-action
-- row on each message in the room timeline); this table just records
-- that judgment call.
--
-- UNIQUE(message_id) makes pinning idempotent: a message can only ever
-- back one decision row, so a duplicate pin request (a double-click, a
-- retried request) can't create a second entry for the same message.
CREATE TABLE decisions (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id    uuid NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    message_id uuid NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    content    text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT decisions_message_id_unique UNIQUE (message_id)
);

CREATE INDEX idx_decisions_room_id_created_at ON decisions(room_id, created_at);

-- Grant included in this same migration, not a follow-up one — 0007
-- exists precisely because 0006's own table creation didn't do this and
-- the gap was only caught by testing against the real harmonia_app role
-- afterward. Doing it right here the first time avoids repeating that.
GRANT SELECT, INSERT, UPDATE, DELETE ON decisions TO harmonia_app;
