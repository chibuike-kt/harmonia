package realtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/user"
)

func TestHumanPresenceHeartbeatHandler_Unauthenticated(t *testing.T) {
	h := HumanPresenceHeartbeatHandler(nil, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/rooms/"+uuid.New().String()+"/presence/heartbeat", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusUnauthorized)
}

func TestHumanPresenceHeartbeatHandler_InvalidRoomID(t *testing.T) {
	h := HumanPresenceHeartbeatHandler(nil, nil)

	req := httptest.NewRequestWithContext(user.NewContext(context.Background(), user.User{ID: uuid.New()}), http.MethodPost, "/v1/rooms/not-a-uuid/presence/heartbeat", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("room_id", "not-a-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusBadRequest)
}

func withRoomIDParam(roomID uuid.UUID, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("room_id", roomID.String())
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx)))
	}
}

// TestIntegration_HumanPresenceHeartbeat_OwnershipAndRealEffect is the
// real, end-to-end proof for the write side of ADR-010's presence gate:
// the room's real owner heartbeating actually sets presence a concurrent
// IsHumanPresent read observes; a non-owner is rejected and leaves no
// trace in Redis; an unknown room 404s.
func TestIntegration_HumanPresenceHeartbeat_OwnershipAndRealEffect(t *testing.T) {
	dbURL := os.Getenv("HARMONIA_DATABASE_URL")
	if dbURL == "" {
		t.Skip("HARMONIA_DATABASE_URL not set; skipping integration test")
	}
	redisAddr := os.Getenv("HARMONIA_REDIS_ADDR")
	if redisAddr == "" {
		t.Skip("HARMONIA_REDIS_ADDR not set; skipping integration test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	defer func() { _ = rdb.Close() }()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)

	owner := seedStreamTestUser(t, ctx, users, "presence-heartbeat-owner-")
	other := seedStreamTestUser(t, ctx, users, "presence-heartbeat-other-")

	rm, err := rooms.Create(ctx, &owner.ID, "presence-heartbeat-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	h := HumanPresenceHeartbeatHandler(rooms, rdb)

	// A non-owner's heartbeat is rejected and must not set presence.
	forbiddenReq := httptest.NewRequestWithContext(user.NewContext(ctx, other), http.MethodPost, "/v1/rooms/"+rm.ID.String()+"/presence/heartbeat", nil)
	forbiddenRec := httptest.NewRecorder()
	withRoomIDParam(rm.ID, h).ServeHTTP(forbiddenRec, forbiddenReq)
	if forbiddenRec.Code != http.StatusForbidden {
		t.Fatalf("non-owner status = %d, want %d, body = %s", forbiddenRec.Code, http.StatusForbidden, forbiddenRec.Body.String())
	}
	present, err := IsHumanPresent(ctx, rdb, rm.ID)
	if err != nil {
		t.Fatalf("IsHumanPresent: %v", err)
	}
	if present {
		t.Fatal("a rejected (403) heartbeat must not set real presence")
	}

	// An unknown room 404s.
	unknownRoomID := uuid.New()
	notFoundReq := httptest.NewRequestWithContext(user.NewContext(ctx, owner), http.MethodPost, "/v1/rooms/"+unknownRoomID.String()+"/presence/heartbeat", nil)
	notFoundRec := httptest.NewRecorder()
	withRoomIDParam(unknownRoomID, h).ServeHTTP(notFoundRec, notFoundReq)
	if notFoundRec.Code != http.StatusNotFound {
		t.Fatalf("unknown room status = %d, want %d, body = %s", notFoundRec.Code, http.StatusNotFound, notFoundRec.Body.String())
	}

	// The real owner's heartbeat sets real presence.
	okReq := httptest.NewRequestWithContext(user.NewContext(ctx, owner), http.MethodPost, "/v1/rooms/"+rm.ID.String()+"/presence/heartbeat", nil)
	okRec := httptest.NewRecorder()
	withRoomIDParam(rm.ID, h).ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusNoContent {
		t.Fatalf("owner status = %d, want %d, body = %s", okRec.Code, http.StatusNoContent, okRec.Body.String())
	}
	present, err = IsHumanPresent(ctx, rdb, rm.ID)
	if err != nil {
		t.Fatalf("IsHumanPresent: %v", err)
	}
	if !present {
		t.Fatal("the owner's real heartbeat didn't set presence a concurrent read observes")
	}

	// The leave handler clears it again.
	leave := HumanPresenceLeaveHandler(rooms, rdb)
	leaveReq := httptest.NewRequestWithContext(user.NewContext(ctx, owner), http.MethodDelete, "/v1/rooms/"+rm.ID.String()+"/presence/heartbeat", nil)
	leaveRec := httptest.NewRecorder()
	withRoomIDParam(rm.ID, leave).ServeHTTP(leaveRec, leaveReq)
	if leaveRec.Code != http.StatusNoContent {
		t.Fatalf("leave status = %d, want %d, body = %s", leaveRec.Code, http.StatusNoContent, leaveRec.Body.String())
	}
	present, err = IsHumanPresent(ctx, rdb, rm.ID)
	if err != nil {
		t.Fatalf("IsHumanPresent: %v", err)
	}
	if present {
		t.Fatal("the real leave request didn't clear presence")
	}
}
