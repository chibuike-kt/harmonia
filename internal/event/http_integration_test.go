package event

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chibuike-kt/harmonia/internal/room"
)

// TestIntegration_ListByRoomHandler exercises GET /v1/rooms/{id}/events
// against real Postgres: an empty room returns [] (never null), and a
// recorded event shows up in the response. Room-scoping itself is
// agent.RequireRoom's job, exercised in the agent package and via full
// wiring in main.go — this test only proves the handler's own behavior.
// Requires a live Postgres — run via `make test-integration` after
// `make up`.
func TestIntegration_ListByRoomHandler(t *testing.T) {
	dbURL := os.Getenv("HARMONIA_DATABASE_URL")
	if dbURL == "" {
		t.Skip("HARMONIA_DATABASE_URL not set; skipping integration test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	rooms := room.NewStore(pool)
	events := NewStore(pool)

	rm, err := rooms.Create(ctx, nil, "event-history-handler-test-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	h := events.ListByRoomHandler()

	rec := doRequest(t, h, rm.ID.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Body.String() != "[]\n" {
		t.Fatalf("empty room body = %q, want %q", rec.Body.String(), "[]\n")
	}

	if err := events.Record(ctx, rm.ID, nil, nil, "ROOM_TEST_EVENT", map[string]any{"note": "hello"}); err != nil {
		t.Fatalf("record event: %v", err)
	}

	rec = doRequest(t, h, rm.ID.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got []Event
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].Type != "ROOM_TEST_EVENT" {
		t.Fatalf("Type = %q, want %q", got[0].Type, "ROOM_TEST_EVENT")
	}
}

// TestIntegration_Record_AtomicOnBarePool proves Record's two writes are
// genuinely one atomic unit even with zero ambient transaction — the
// exact gap raised before committing this: the context-engine's call
// path (internal/contextengine/http.go) never wraps Record in a
// transaction, so does the event-insert-plus-rooms-update pair still
// stand or fall together there?
//
// A foreign-key violation on the INSERT itself doesn't prove this: with
// the two writes as separate sequential Exec calls, a failed first Exec
// never reaches the second one anyway, so "the room wasn't touched"
// would hold true even without any atomicity at all — that failure mode
// is safe by accident, not by proof. The real gap is the INSERT
// succeeding and the UPDATE failing after it: under two separate Execs
// that leaves a real event row with no matching last_activity_at bump.
// A BEFORE UPDATE trigger on rooms forces exactly that ordering — it
// fires only on the second statement, deterministically, regardless of
// which implementation Record uses. This test was run against the
// previous two-Exec Record and confirmed to fail there (a real event row
// landed despite the forced failure) before being kept as the permanent
// regression guard for the one-statement version.
func TestIntegration_Record_AtomicOnBarePool(t *testing.T) {
	dbURL := os.Getenv("HARMONIA_DATABASE_URL")
	if dbURL == "" {
		t.Skip("HARMONIA_DATABASE_URL not set; skipping integration test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	// t.Cleanup, not a plain defer: a local defer in this function runs
	// as the function itself unwinds, which happens before the testing
	// package invokes any t.Cleanup callback — a plain `defer
	// pool.Close()` here would close the pool before the trigger/function
	// drop below (also registered via t.Cleanup, LIFO) ever got to run
	// against it. Cost of getting this wrong once already: a stray
	// trigger left behind on the real dev database, breaking every next
	// run of this test with "trigger already exists" until removed by hand.
	t.Cleanup(func() { pool.Close() })

	const sentinelName = "atomic-on-bare-pool-test-room-force-fail"
	if _, err := pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION pg_temp_force_room_update_failure() RETURNS trigger AS $$
		BEGIN
			IF OLD.name = '`+sentinelName+`' THEN
				RAISE EXCEPTION 'forced failure for atomicity test';
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql;
	`); err != nil {
		t.Fatalf("create trigger function: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TRIGGER test_force_room_update_failure
		BEFORE UPDATE ON rooms FOR EACH ROW
		EXECUTE FUNCTION pg_temp_force_room_update_failure()
	`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	// Registered after pool.Close's cleanup above, so LIFO ordering runs
	// this one first, while the pool is still open.
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS test_force_room_update_failure ON rooms`); err != nil {
			t.Errorf("drop test trigger (left behind on the real database — remove by hand): %v", err)
		}
		if _, err := pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS pg_temp_force_room_update_failure()`); err != nil {
			t.Errorf("drop test trigger function (left behind on the real database — remove by hand): %v", err)
		}
	})

	rooms := room.NewStore(pool)
	events := NewStore(pool) // bare pool — no transaction, on purpose

	rm, err := rooms.Create(ctx, nil, sentinelName)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	if err := events.Record(ctx, rm.ID, nil, nil, "SHOULD_NOT_LAND", map[string]any{}); err == nil {
		t.Fatal("expected Record to fail via the forced trigger, got nil error")
	}

	var eventCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE room_id = $1`, rm.ID).Scan(&eventCount); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if eventCount != 0 {
		t.Fatalf("event insert landed despite the room update failing in the same statement: found %d event rows", eventCount)
	}
}

// TestIntegration_Record_UpdatesRoomLastActivity proves Record's second
// write actually lands: rooms.last_activity_at moves forward on a real
// Record call, against real Postgres — not just that the SQL compiles.
// This is the one signal GET /v1/rooms sorts by (see
// docs/design/dashboard-build-brief.md), so it gets its own direct test
// rather than relying on indirect coverage through task/handoff's own
// integration tests.
func TestIntegration_Record_UpdatesRoomLastActivity(t *testing.T) {
	dbURL := os.Getenv("HARMONIA_DATABASE_URL")
	if dbURL == "" {
		t.Skip("HARMONIA_DATABASE_URL not set; skipping integration test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	rooms := room.NewStore(pool)
	events := NewStore(pool)

	rm, err := rooms.Create(ctx, nil, "last-activity-test-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	// Backdated so the post-Record read is unambiguously later, not just
	// "close enough to now that a fast test run can't tell."
	if _, err := pool.Exec(ctx, `UPDATE rooms SET last_activity_at = now() - interval '1 hour' WHERE id = $1`, rm.ID); err != nil {
		t.Fatalf("backdate room: %v", err)
	}
	var before time.Time
	if err := pool.QueryRow(ctx, `SELECT last_activity_at FROM rooms WHERE id = $1`, rm.ID).Scan(&before); err != nil {
		t.Fatalf("read last_activity_at before Record: %v", err)
	}

	if err := events.Record(ctx, rm.ID, nil, nil, "ROOM_ACTIVITY_TEST_EVENT", map[string]any{}); err != nil {
		t.Fatalf("record event: %v", err)
	}

	var after time.Time
	if err := pool.QueryRow(ctx, `SELECT last_activity_at FROM rooms WHERE id = $1`, rm.ID).Scan(&after); err != nil {
		t.Fatalf("read last_activity_at after Record: %v", err)
	}
	if !after.After(before) {
		t.Fatalf("last_activity_at did not advance: before = %v, after = %v", before, after)
	}
}

func doRequest(t *testing.T, h http.HandlerFunc, roomIDParam string) *httptest.ResponseRecorder {
	t.Helper()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", roomIDParam)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/rooms/"+roomIDParam+"/events", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
