package realtime

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/event"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/user"
)

// subscriberCount reads h's internal subscriber count for roomID
// directly — the most reliable way to assert "the leak is gone" is to
// look at the exact state a leak would corrupt, not an indirect proxy
// for it.
func subscriberCount(h *Hub, roomID uuid.UUID) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs[roomID])
}

// TestIntegration_StreamHandler_DisconnectUnsubscribes opens a real SSE
// stream over a real TCP connection (httptest.NewServer, not a
// ResponseRecorder — a recorder can't simulate a client actually going
// away), confirms the hub picks up exactly one subscriber, then cancels
// the client's request context — the same effect a closed browser tab or
// an aborted EventSource has: the underlying connection closes. It then
// asserts the hub's subscriber count for that room returns to zero.
//
// This is the test the Phase 3 build brief calls a hard requirement, not
// a nice-to-have: a goroutine/subscriber leak here is exactly the kind of
// slow, silent server-killer that only shows up under real sustained
// load, not in a quick manual check.
//
// It only exercises the reliable disconnect path — a clean TCP close,
// which Go's net/http server detects via a background read on the
// connection and uses to cancel the request's context promptly. See
// StreamHandler's doc comment for why a truly silent network drop (no
// FIN/RST ever sent) is a different, weaker guarantee that this test
// does not and cannot claim to cover.
func TestIntegration_StreamHandler_DisconnectUnsubscribes(t *testing.T) {
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
	agents := agent.NewStore(pool)
	events := event.NewStore(pool)

	owner, err := users.UpsertByGitHubID(ctx, "stream-leak-test-owner-"+uuid.New().String(), "stream-leak-test-owner", nil, nil, nil)
	if err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	sessionPlaintext, sessionHash, err := user.GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken: %v", err)
	}
	if _, err := users.CreateSession(ctx, owner.ID, sessionHash, nil, nil); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	rm, err := rooms.Create(ctx, &owner.ID, "stream-leak-test-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	hub := NewHub()
	streamHandler := StreamHandler(rooms, agents, events, hub, rdb)

	// StreamHandler reads chi.URLParam("room_id") — inject a route
	// context directly rather than standing up a full chi.Router, since
	// this test only needs the one route.
	withRoomIDParam := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("room_id", rm.ID.String())
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx)))
		}
	}

	srv := httptest.NewServer(user.Authenticate(users)(withRoomIDParam(streamHandler)))
	defer srv.Close()

	if got := subscriberCount(hub, rm.ID); got != 0 {
		t.Fatalf("subscriber count before connecting = %d, want 0", got)
	}

	reqCtx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, srv.URL+"/v1/rooms/"+rm.ID.String()+"/stream", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: user.SessionCookieName, Value: sessionPlaintext})

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// Read the snapshot line so we know the handler is actually past
	// Subscribe and into the stream loop, not still setting up.
	reader := bufio.NewReader(resp.Body)
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("read snapshot event line: %v", err)
	}

	if got := subscriberCount(hub, rm.ID); got != 1 {
		t.Fatalf("subscriber count while connected = %d, want 1", got)
	}

	// Simulate the client going away: cancel its context (closing the
	// underlying connection) and close the response body, the same as an
	// EventSource being torn down or a tab closing.
	cancel()
	_ = resp.Body.Close()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := subscriberCount(hub, rm.ID); got == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("subscriber count did not return to 0 within 2s of disconnect; still %d", subscriberCount(hub, rm.ID))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestIntegration_StreamHandler_RoomOwnershipAndSnapshot exercises the
// same 404/403 room-ownership pattern agent registration uses, and
// confirms the initial snapshot actually reflects real Postgres/Redis
// state: an agent registered in the room, with a status mirrored into
// Redis, shows up in the snapshot's presence list. Requires live
// Postgres and Redis — run via `make test-integration` after `make up`.
func TestIntegration_StreamHandler_RoomOwnershipAndSnapshot(t *testing.T) {
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
	agents := agent.NewStore(pool)
	events := event.NewStore(pool)

	owner := seedStreamTestUser(t, ctx, users, "stream-ownership-owner-")
	other := seedStreamTestUser(t, ctx, users, "stream-ownership-other-")

	rm, err := rooms.Create(ctx, &owner.ID, "stream-ownership-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	registered, err := agents.Register(ctx, rm.ID, "watcher", agent.ProviderAnthropic, nil, "hash-watcher")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	if err := SetPresence(ctx, rdb, registered.ID, string(agent.StatusRunning)); err != nil {
		t.Fatalf("SetPresence: %v", err)
	}

	hub := NewHub()
	h := StreamHandler(rooms, agents, events, hub, rdb)

	withRoomIDParam := func(roomID uuid.UUID, next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("room_id", roomID.String())
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx)))
		}
	}

	// A non-owner gets 403, not a leak of whether the room exists.
	forbiddenReq := httptest.NewRequestWithContext(user.NewContext(ctx, other), http.MethodGet, "/v1/rooms/"+rm.ID.String()+"/stream", nil)
	forbiddenRec := httptest.NewRecorder()
	withRoomIDParam(rm.ID, h).ServeHTTP(forbiddenRec, forbiddenReq)
	if forbiddenRec.Code != http.StatusForbidden {
		t.Fatalf("non-owner status = %d, want %d, body = %s", forbiddenRec.Code, http.StatusForbidden, forbiddenRec.Body.String())
	}
	if subscriberCount(hub, rm.ID) != 0 {
		t.Fatal("a rejected (403) request must not leave a subscriber behind")
	}

	// An unknown room 404s.
	unknownRoomID := uuid.New()
	notFoundReq := httptest.NewRequestWithContext(user.NewContext(ctx, owner), http.MethodGet, "/v1/rooms/"+unknownRoomID.String()+"/stream", nil)
	notFoundRec := httptest.NewRecorder()
	withRoomIDParam(unknownRoomID, h).ServeHTTP(notFoundRec, notFoundReq)
	if notFoundRec.Code != http.StatusNotFound {
		t.Fatalf("unknown room status = %d, want %d, body = %s", notFoundRec.Code, http.StatusNotFound, notFoundRec.Body.String())
	}

	// The owner connecting for real gets a snapshot reflecting the
	// registered agent's Redis-mirrored presence.
	srv := httptest.NewServer(user.Authenticate(users)(withRoomIDParam(rm.ID, h)))
	defer srv.Close()

	sessionPlaintext, sessionHash, err := user.GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken: %v", err)
	}
	if _, err := users.CreateSession(ctx, owner.ID, sessionHash, nil, nil); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/rooms/"+rm.ID.String()+"/stream", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: user.SessionCookieName, Value: sessionPlaintext})

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	reader := bufio.NewReader(resp.Body)
	eventLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read event line: %v", err)
	}
	if eventLine != "event: snapshot\n" {
		t.Fatalf("first SSE line = %q, want %q", eventLine, "event: snapshot\n")
	}
	dataLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read data line: %v", err)
	}
	rawData := strings.TrimPrefix(strings.TrimSuffix(dataLine, "\n"), "data: ")

	// This room has no task/handoff activity, so its events list is
	// empty — and Go's json.Unmarshal happily accepts JSON null into a
	// nil []event.Event without complaint, which is exactly why this
	// specific regression (event.Store.ListByRoom's nil slice encoding
	// as "events":null instead of "events":[]) was invisible to Go-only
	// tests and only surfaced against a real browser: JavaScript's
	// `[...null]` throws, breaking the room-view page outright for any
	// brand-new room. Asserting on the raw wire bytes here, before
	// unmarshaling erases the distinction, is what actually catches it.
	if strings.Contains(rawData, `"events":null`) {
		t.Fatalf("snapshot JSON has \"events\":null, want \"events\":[] — raw = %s", rawData)
	}

	var got snapshot
	if err := json.Unmarshal([]byte(rawData), &got); err != nil {
		t.Fatalf("decode snapshot data: %v", err)
	}
	if got.Events == nil {
		t.Fatal("snapshot Events must never be nil, even for a room with no events yet")
	}
	if len(got.Events) != 0 {
		t.Fatalf("snapshot Events = %+v, want empty (this room has no task/handoff activity)", got.Events)
	}
	if len(got.Presence) != 1 || got.Presence[0].AgentID != registered.ID || got.Presence[0].Status != string(agent.StatusRunning) {
		t.Fatalf("snapshot Presence = %+v, want one entry for agent=%s status=%s", got.Presence, registered.ID, agent.StatusRunning)
	}
}

func seedStreamTestUser(t *testing.T, ctx context.Context, users *user.Store, githubIDPrefix string) user.User {
	t.Helper()
	u, err := users.UpsertByGitHubID(ctx, githubIDPrefix+uuid.New().String(), githubIDPrefix+"user", nil, nil, nil)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u
}
