package task

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	agentpkg "github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/event"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/store"
)

// TestIntegration_TaskLifecycle walks create -> claim -> complete against
// real Postgres, checking the room- and ownership-scoping the handlers add
// on top of the bare Store methods, and that each transition records the
// matching event. Requires a live Postgres — run via `make test-integration`
// after `make up`.
func TestIntegration_TaskLifecycle(t *testing.T) {
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

	beginner := store.PoolBeginner{Pool: pool}
	createHandler := tasks.CreateHandler(beginner)
	claimHandler := tasks.ClaimHandler(beginner)
	completeHandler := tasks.CompleteHandler(beginner)

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
