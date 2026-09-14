package realtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// humanPresenceKey is the one place the room:{id}:human_present format is
// spelled out — mirrors presenceKey's own reasoning in presence.go, so a
// writer (the heartbeat handler) and a reader (Batch 2's tool-gating
// check) can't drift on format.
func humanPresenceKey(roomID uuid.UUID) string {
	return fmt.Sprintf("room:%s:human_present", roomID)
}

// SetHumanPresent marks a human as currently, actively present in
// roomID, expiring after ttl if nothing refreshes it. This is ADR-010's
// primary safety mechanism for agent file/shell execution: presence is a
// live heartbeat a client must keep resending, not a flag that's set
// once and stays true — a closed tab, a crashed browser, or a laptop put
// to sleep must stop being "present" within one missed heartbeat
// interval, not linger true until something explicitly clears it.
func SetHumanPresent(ctx context.Context, rdb *redis.Client, roomID uuid.UUID, ttl time.Duration) error {
	return rdb.Set(ctx, humanPresenceKey(roomID), "1", ttl).Err()
}

// ClearHumanPresent removes roomID's presence immediately — a real
// "I'm leaving" signal (e.g. the IDE page's own unload handler, on a
// best-effort basis) rather than waiting out the full TTL. Best-effort
// because a genuinely closed tab can't reliably fire this at all
// (browsers don't guarantee a request completes during unload); TTL
// expiry is the actual, unconditional guarantee this gate relies on, not
// this call.
func ClearHumanPresent(ctx context.Context, rdb *redis.Client, roomID uuid.UUID) error {
	return rdb.Del(ctx, humanPresenceKey(roomID)).Err()
}

// IsHumanPresent reports whether a human is genuinely, currently present
// in roomID right now.
//
// This is the actual gate Batch 2's tool-declaration wiring must call
// before including any companion file-write or shell-exec ToolDef in a
// generation call's tool list for that room — per ADR-010, those tools
// must be genuinely absent from what the agent is offered when this
// returns false, never present-but-queued or present-but-approval-gated.
// Call this once per generation call, immediately before building that
// call's tool list; it is cheap (one Redis GET) and deliberately not
// cached, since presence can change between one generation call and the
// next.
func IsHumanPresent(ctx context.Context, rdb *redis.Client, roomID uuid.UUID) (bool, error) {
	_, err := rdb.Get(ctx, humanPresenceKey(roomID)).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
