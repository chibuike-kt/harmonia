DROP TABLE agent_pickup_evaluations;
ALTER TABLE messages DROP COLUMN pickup_claimed_at;
ALTER TABLE messages DROP COLUMN pickup_claimed_by;
ALTER TABLE rooms DROP COLUMN autonomous_pickup_enabled;
