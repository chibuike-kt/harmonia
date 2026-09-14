package realtime

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// TestHumanPresence_SetAndExpire is the real proof for ADR-010's presence
// gate: a room reports no human present until SetHumanPresent is called,
// reports present immediately after, and — with no further heartbeat —
// reports not present again once the real TTL elapses, against a real
// Redis instance, not a fake clock.
func TestHumanPresence_SetAndExpire(t *testing.T) {
	redisAddr := os.Getenv("HARMONIA_REDIS_ADDR")
	if redisAddr == "" {
		t.Skip("HARMONIA_REDIS_ADDR not set; skipping integration test")
	}
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	defer func() { _ = rdb.Close() }()

	ctx := context.Background()
	roomID := uuid.New()

	present, err := IsHumanPresent(ctx, rdb, roomID)
	if err != nil {
		t.Fatalf("IsHumanPresent: %v", err)
	}
	if present {
		t.Fatal("a room nobody has ever heartbeated reports present")
	}

	if err := SetHumanPresent(ctx, rdb, roomID, 150*time.Millisecond); err != nil {
		t.Fatalf("SetHumanPresent: %v", err)
	}
	present, err = IsHumanPresent(ctx, rdb, roomID)
	if err != nil {
		t.Fatalf("IsHumanPresent: %v", err)
	}
	if !present {
		t.Fatal("IsHumanPresent reports false immediately after a real SetHumanPresent")
	}

	time.Sleep(250 * time.Millisecond)
	present, err = IsHumanPresent(ctx, rdb, roomID)
	if err != nil {
		t.Fatalf("IsHumanPresent: %v", err)
	}
	if present {
		t.Fatal("presence is still true after its real TTL elapsed with no further heartbeat — this is exactly the gap that would let agent tools stay available after a tab silently closes")
	}
}

// TestHumanPresence_Clear proves the explicit-leave path: presence set,
// then cleared, is gone immediately — no need to wait out the TTL.
func TestHumanPresence_Clear(t *testing.T) {
	redisAddr := os.Getenv("HARMONIA_REDIS_ADDR")
	if redisAddr == "" {
		t.Skip("HARMONIA_REDIS_ADDR not set; skipping integration test")
	}
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	defer func() { _ = rdb.Close() }()

	ctx := context.Background()
	roomID := uuid.New()

	if err := SetHumanPresent(ctx, rdb, roomID, time.Minute); err != nil {
		t.Fatalf("SetHumanPresent: %v", err)
	}
	if err := ClearHumanPresent(ctx, rdb, roomID); err != nil {
		t.Fatalf("ClearHumanPresent: %v", err)
	}
	present, err := IsHumanPresent(ctx, rdb, roomID)
	if err != nil {
		t.Fatalf("IsHumanPresent: %v", err)
	}
	if present {
		t.Fatal("ClearHumanPresent didn't actually clear presence")
	}
}
