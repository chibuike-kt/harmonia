-- Recency needs a real activity signal, not room-creation order or the
-- append-only events table's own timestamps (querying MAX(events.created_at)
-- per room on every dashboard load doesn't scale the way a denormalized,
-- transactionally-maintained column does). See docs/design/dashboard-build-brief.md.

ALTER TABLE rooms ADD COLUMN last_activity_at timestamptz NOT NULL DEFAULT now();

-- Backs GET /v1/rooms' "my rooms, most recently active first" query.
CREATE INDEX idx_rooms_owner_id_last_activity ON rooms(owner_id, last_activity_at DESC);
