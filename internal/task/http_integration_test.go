package task

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	agentpkg "github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/event"
	"github.com/chibuike-kt/harmonia/internal/protocol"
	"github.com/chibuike-kt/harmonia/internal/realtime"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/store"
)

// TestIntegration_TaskLifecycle walks create -> claim -> complete against
// real Postgres and real Redis, checking the room- and ownership-scoping
// the handlers add on top of the bare Store methods, that each
// transition records the matching event, that claiming/completing
// actually flips the claiming agent's status in Postgres (running, then
// back to available) and mirrors it into Redis, and that every one of
// those transitions reaches a real hub subscriber in order through the
// real production handlers. Requires live Postgres and Redis — run via
// `make test-integration` after `make up`.
func TestIntegration_TaskLifecycle(t *testing.T) {
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

	rooms := room.NewStore(pool)
	agents := agentpkg.NewStore(pool)
	tasks := NewStore(pool)
	events := event.NewStore(pool)

	roomA, err := rooms.Create(ctx, nil, "task-lifecycle-room-a")
	if err != nil {
		t.Fatalf("create room A: %v", err)
	}
	roomB, err := rooms.Create(ctx, nil, "task-lifecycle-room-b")
	if err != nil {
		t.Fatalf("create room B: %v", err)
	}

	creator, err := agents.Register(ctx, roomA.ID, "creator", agentpkg.ProviderAnthropic, nil, "hash-creator")
	if err != nil {
		t.Fatalf("register creator: %v", err)
	}
	claimer, err := agents.Register(ctx, roomA.ID, "claimer", agentpkg.ProviderAnthropic, nil, "hash-claimer")
	if err != nil {
		t.Fatalf("register claimer: %v", err)
	}
	outsider, err := agents.Register(ctx, roomB.ID, "outsider", agentpkg.ProviderAnthropic, nil, "hash-outsider")
	if err != nil {
		t.Fatalf("register outsider: %v", err)
	}

	hub := realtime.NewHub()
	sub, unsubscribe := hub.Subscribe(roomA.ID)
	defer unsubscribe()

	beginner := store.PoolBeginner{Pool: pool}
	createHandler := tasks.CreateHandler(beginner, hub)
	claimHandler := tasks.ClaimHandler(beginner, hub, rdb)
	completeHandler := tasks.CompleteHandler(beginner, hub, rdb)

	// Create, as the creator.
	rec := doJSON(t, createHandler, creator, "", `{"objective":"write the report"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d, body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var created Task
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Status != StatusQueued {
		t.Fatalf("created Status = %q, want %q", created.Status, StatusQueued)
	}

	// Claim from another room 404s — cross-room task existence isn't leaked.
	rec = doJSON(t, claimHandler, outsider, created.ID.String(), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-room claim status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}

	// Claim, as the claimer.
	rec = doJSON(t, claimHandler, claimer, created.ID.String(), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("claim status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var claimed Task
	if err := json.Unmarshal(rec.Body.Bytes(), &claimed); err != nil {
		t.Fatalf("decode claim response: %v", err)
	}
	if claimed.Status != StatusClaimed {
		t.Fatalf("claimed Status = %q, want %q", claimed.Status, StatusClaimed)
	}
	if claimed.OwnerAgentID == nil || *claimed.OwnerAgentID != claimer.ID {
		t.Fatalf("claimed OwnerAgentID = %v, want %s", claimed.OwnerAgentID, claimer.ID)
	}

	// The claim actually flipped the claimer's status in Postgres, in the
	// same transaction as the task write — not a side channel that could
	// silently drift from it.
	claimerAfterClaim, err := agents.GetByID(ctx, claimer.ID)
	if err != nil {
		t.Fatalf("GetByID claimer after claim: %v", err)
	}
	if claimerAfterClaim.Status != agentpkg.StatusRunning {
		t.Fatalf("claimer Status after claim = %q, want %q", claimerAfterClaim.Status, agentpkg.StatusRunning)
	}
	// ...and mirrored the same status into Redis.
	if got, err := realtime.GetPresence(ctx, rdb, claimer.ID); err != nil {
		t.Fatalf("GetPresence after claim: %v", err)
	} else if got != string(agentpkg.StatusRunning) {
		t.Fatalf("Redis presence after claim = %q, want %q", got, agentpkg.StatusRunning)
	}

	// Claiming again loses the race — already claimed.
	rec = doJSON(t, claimHandler, creator, created.ID.String(), "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("re-claim status = %d, want %d, body = %s", rec.Code, http.StatusConflict, rec.Body.String())
	}

	// Completing as a non-owner is forbidden.
	rec = doJSON(t, completeHandler, creator, created.ID.String(), "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner complete status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}

	// Complete, as the owner.
	rec = doJSON(t, completeHandler, claimer, created.ID.String(), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("complete status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var completed Task
	if err := json.Unmarshal(rec.Body.Bytes(), &completed); err != nil {
		t.Fatalf("decode complete response: %v", err)
	}
	if completed.Status != StatusCompleted {
		t.Fatalf("completed Status = %q, want %q", completed.Status, StatusCompleted)
	}

	// The completion flipped the claimer's status back to available, in
	// Postgres and mirrored in Redis, the same way the claim did.
	claimerAfterComplete, err := agents.GetByID(ctx, claimer.ID)
	if err != nil {
		t.Fatalf("GetByID claimer after complete: %v", err)
	}
	if claimerAfterComplete.Status != agentpkg.StatusAvailable {
		t.Fatalf("claimer Status after complete = %q, want %q", claimerAfterComplete.Status, agentpkg.StatusAvailable)
	}
	if got, err := realtime.GetPresence(ctx, rdb, claimer.ID); err != nil {
		t.Fatalf("GetPresence after complete: %v", err)
	} else if got != string(agentpkg.StatusAvailable) {
		t.Fatalf("Redis presence after complete = %q, want %q", got, agentpkg.StatusAvailable)
	}

	// Every transition recorded its event, in order.
	roomEvents, err := events.ListByRoom(ctx, roomA.ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	var gotTypes []string
	for _, e := range roomEvents {
		if e.TaskID != nil && *e.TaskID == created.ID {
			gotTypes = append(gotTypes, e.Type)
		}
	}
	wantTypes := []string{EventTaskCreated, EventTaskClaimed, EventTaskCompleted}
	if len(gotTypes) != len(wantTypes) {
		t.Fatalf("event types = %v, want %v", gotTypes, wantTypes)
	}
	for i, want := range wantTypes {
		if gotTypes[i] != want {
			t.Fatalf("event[%d] = %q, want %q", i, gotTypes[i], want)
		}
	}

	// Each committed transition also reached the hub's subscriber, in
	// order, through the real production handlers — not a fake. Claim and
	// Complete each publish two messages (the event, then the presence
	// transition); the rejected attempts (cross-room claim, re-claim,
	// non-owner complete) published nothing, so they don't appear here.
	type wantMsg struct {
		kind   realtime.Kind
		op     protocol.Operation
		status agentpkg.Status
	}
	wantMsgs := []wantMsg{
		{kind: realtime.KindEvent, op: protocol.OpTaskCreate},
		{kind: realtime.KindEvent, op: protocol.OpTaskClaim},
		{kind: realtime.KindPresence, status: agentpkg.StatusRunning},
		{kind: realtime.KindEvent, op: protocol.OpTaskComplete},
		{kind: realtime.KindPresence, status: agentpkg.StatusAvailable},
	}
	for i, want := range wantMsgs {
		select {
		case msg := <-sub:
			if msg.Kind != want.kind {
				t.Fatalf("message[%d] Kind = %q, want %q (msg = %+v)", i, msg.Kind, want.kind, msg)
			}
			switch want.kind {
			case realtime.KindEvent:
				if msg.Event == nil || msg.Event.Type != want.op {
					t.Fatalf("message[%d] = %+v, want event type %q", i, msg, want.op)
				}
				if msg.Event.RoomID != roomA.ID {
					t.Fatalf("message[%d] RoomID = %s, want %s", i, msg.Event.RoomID, roomA.ID)
				}
			case realtime.KindPresence:
				if msg.Presence == nil || msg.Presence.AgentID != claimer.ID || msg.Presence.Status != string(want.status) {
					t.Fatalf("message[%d] = %+v, want presence agent=%s status=%s", i, msg, claimer.ID, want.status)
				}
			}
		case <-time.After(time.Second):
			t.Fatalf("expected message[%d] (%+v), got none", i, want)
		}
	}
	select {
	case extra := <-sub:
		t.Fatalf("unexpected extra hub publish: %+v", extra)
	case <-time.After(50 * time.Millisecond):
	}
}

func doJSON(t *testing.T, h http.HandlerFunc, a agentpkg.Agent, taskIDParam, body string) *httptest.ResponseRecorder {
	t.Helper()

	var bodyReader *strings.Reader
	if body == "" {
		bodyReader = strings.NewReader("")
	} else {
		bodyReader = strings.NewReader(body)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/tasks", bodyReader)
	if taskIDParam != "" {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", taskIDParam)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	}
	req = req.WithContext(agentpkg.NewContext(req.Context(), a))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
