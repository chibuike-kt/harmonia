package realtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// PresenceTTL bounds how long a mirrored status survives in Redis if
// nothing ever updates it again. A day is plenty for what this is — a
// live-session convenience so a client connecting mid-session can fetch
// a snapshot, not durable state (see ADR-003).
const PresenceTTL = 24 * time.Hour

// presenceKey is the one place the agent:{id}:status format is spelled
// out — both SetPresence and GetPresence go through it, so a writer and
// a reader (the SSE endpoint's initial snapshot) can't drift on format.
func presenceKey(agentID uuid.UUID) string {
	return fmt.Sprintf("agent:%s:status", agentID)
}

// SetPresence mirrors agentID's current status into Redis.
func SetPresence(ctx context.Context, rdb *redis.Client, agentID uuid.UUID, status string) error {
	return rdb.Set(ctx, presenceKey(agentID), status, PresenceTTL).Err()
}

// GetPresence reads agentID's last-mirrored status. Returns "" with a
// nil error if nothing has been mirrored yet or it expired — that's a
// legitimate "never transitioned recently" state, not a failure the
// caller needs to handle specially.
func GetPresence(ctx context.Context, rdb *redis.Client, agentID uuid.UUID) (string, error) {
	status, err := rdb.Get(ctx, presenceKey(agentID)).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return status, err
}
