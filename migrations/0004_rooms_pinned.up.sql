-- Nullable timestamp, not a boolean — same style as sessions.revoked_at:
-- not-null means pinned, and the timestamp itself is a real value (when
-- it was pinned), not information the app immediately has to reconstruct.

ALTER TABLE rooms ADD COLUMN pinned_at timestamptz;
