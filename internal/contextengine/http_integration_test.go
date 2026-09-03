package contextengine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/event"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/task"
)

// TestIntegration_TaskHandler exercises GET /v1/context/tasks/{task_id}
// against real Postgres: a same-room query succeeds and records
// CONTEXT_REQUESTED, a cross-room query 404s. Requires a live Postgres —
// run via `make test-integration` after `make up`.
func TestIntegration_TaskHandler(t *testing.T) {
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
	agents := agent.NewStore(pool)
	tasks := task.NewStore(pool)
	events := event.NewStore(pool)
	contexts := NewStore(pool)

	roomA, err := rooms.Create(ctx, "context-handler-test-room-a")
	if err != nil {
		t.Fatalf("create room A: %v", err)
	}
	roomB, err := rooms.Create(ctx, "context-handler-test-room-b")
	if err != nil {
		t.Fatalf("create room B: %v", err)
	}

	member, err := agents.Register(ctx, roomA.ID, "member", agent.ProviderAnthropic, nil, "hash-member")
	if err != nil {
		t.Fatalf("register member: %v", err)
	}
	outsider, err := agents.Register(ctx, roomB.ID, "outsider", agent.ProviderAnthropic, nil, "hash-outsider")
	if err != nil {
		t.Fatalf("register outsider: %v", err)
	}

	tk, err := tasks.Create(ctx, roomA.ID, "investigate the bug", nil)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	h := contexts.TaskHandler(events)

	rec := doRequest(t, h, tk.ID.String(), member)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got TaskResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.TaskID != tk.ID {
		t.Fatalf("TaskID = %s, want %s", got.TaskID, tk.ID)
	}
	if got.Objective != tk.Objective {
		t.Fatalf("Objective = %q, want %q", got.Objective, tk.Objective)
	}

	rec = doRequest(t, h, tk.ID.String(), outsider)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-room status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}

	roomEvents, err := events.ListByRoom(ctx, roomA.ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	found := false
	for _, e := range roomEvents {
		if e.Type == EventContextRequested && e.TaskID != nil && *e.TaskID == tk.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a CONTEXT_REQUESTED event for the task")
	}
}

func doRequest(t *testing.T, h http.HandlerFunc, taskIDParam string, a agent.Agent) *httptest.ResponseRecorder {
	t.Helper()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("task_id", taskIDParam)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/context/tasks/"+taskIDParam, nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(agent.NewContext(req.Context(), a))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
