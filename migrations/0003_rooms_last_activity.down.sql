DROP INDEX IF EXISTS idx_rooms_owner_id_last_activity;
ALTER TABLE rooms DROP COLUMN last_activity_at;
