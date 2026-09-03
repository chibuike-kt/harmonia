package handoff

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
	"github.com/chibuike-kt/harmonia/internal/task"
)

// TestIntegration_HandoffLifecycle walks request -> accept against real
// Postgres, checking the room-scoping RequestHandler adds on top of the
// bare Store methods, that only the addressed agent can accept, that a
// second accept 409s, and that each transition records its event.
// Requires a live Postgres — run via `make test-integration` after
// `make up`.
func TestIntegration_HandoffLifecycle(t *testing.T) {
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
	tasks := task.NewStore(pool)
	events := event.NewStore(pool)
	handoffs := NewStore(pool)

	roomA, err := rooms.Create(ctx, nil, "handoff-lifecycle-room-a")
	if err != nil {
		t.Fatalf("create room A: %v", err)
	}
	roomB, err := rooms.Create(ctx, nil, "handoff-lifecycle-room-b")
	if err != nil {
		t.Fatalf("create room B: %v", err)
	}

	from, err := agents.Register(ctx, roomA.ID, "from-agent", agentpkg.ProviderAnthropic, nil, "hash-from")
	if err != nil {
		t.Fatalf("register from agent: %v", err)
	}
	to, err := agents.Register(ctx, roomA.ID, "to-agent", agentpkg.ProviderAnthropic, nil, "hash-to")
	if err != nil {
		t.Fatalf("register to agent: %v", err)
	}
	outsider, err := agents.Register(ctx, roomB.ID, "outsider", agentpkg.ProviderAnthropic, nil, "hash-outsider")
	if err != nil {
		t.Fatalf("register outsider: %v", err)
	}
	outsiderTask, err := tasks.Create(ctx, roomB.ID, "outsider task", nil)
	if err != nil {
		t.Fatalf("create outsider task: %v", err)
	}

	tk, err := tasks.Create(ctx, roomA.ID, "write the report", nil)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	beginner := store.PoolBeginner{Pool: pool}
	requestHandler := handoffs.RequestHandler(tasks, agents, beginner)
	acceptHandler := handoffs.AcceptHandler(beginner)

	// Requesting a handoff for a task in another room 404s.
	rec := doRequest(t, requestHandler, from, `{"task_id":"`+outsiderTask.ID.String()+`","to_agent_id":"`+to.ID.String()+`","summary":"s"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-room task status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}

	// Requesting a handoff to an agent in another room 404s.
	rec = doRequest(t, requestHandler, from, `{"task_id":"`+tk.ID.String()+`","to_agent_id":"`+outsider.ID.String()+`","summary":"s"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-room to_agent status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}

	// Request, as the from agent.
	rec = doRequest(t, requestHandler, from, `{"task_id":"`+tk.ID.String()+`","to_agent_id":"`+to.ID.String()+`","summary":"handing off","completed":["step 1"],"remaining":["step 2"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("request status = %d, want %d, body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var requested Handoff
	if err := json.Unmarshal(rec.Body.Bytes(), &requested); err != nil {
		t.Fatalf("decode request response: %v", err)
	}
	if requested.Status != StatusRequested {
		t.Fatalf("requested Status = %q, want %q", requested.Status, StatusRequested)
	}

	// Accepting as anyone but the addressed agent is forbidden.
	rec = doAccept(t, acceptHandler, requested.ID.String(), from)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong-agent accept status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}

	// Accept, as the to agent.
	rec = doAccept(t, acceptHandler, requested.ID.String(), to)
	if rec.Code != http.StatusOK {
		t.Fatalf("accept status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var accepted Handoff
	if err := json.Unmarshal(rec.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode accept response: %v", err)
	}
	if accepted.Status != StatusAccepted {
		t.Fatalf("accepted Status = %q, want %q", accepted.Status, StatusAccepted)
	}

	// Accepting again 409s — already accepted.
	rec = doAccept(t, acceptHandler, requested.ID.String(), to)
	if rec.Code != http.StatusConflict {
		t.Fatalf("re-accept status = %d, want %d, body = %s", rec.Code, http.StatusConflict, rec.Body.String())
	}

	roomEvents, err := events.ListByRoom(ctx, roomA.ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	var gotTypes []string
	for _, e := range roomEvents {
		if e.TaskID != nil && *e.TaskID == tk.ID {
			gotTypes = append(gotTypes, e.Type)
		}
	}
	wantTypes := []string{EventHandoffRequested, EventHandoffAccepted}
	if len(gotTypes) != len(wantTypes) {
		t.Fatalf("event types = %v, want %v", gotTypes, wantTypes)
	}
	for i, want := range wantTypes {
		if gotTypes[i] != want {
			t.Fatalf("event[%d] = %q, want %q", i, gotTypes[i], want)
		}
	}
}

func doRequest(t *testing.T, h http.HandlerFunc, a agentpkg.Agent, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/handoffs", strings.NewReader(body))
	req = req.WithContext(agentpkg.NewContext(req.Context(), a))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func doAccept(t *testing.T, h http.HandlerFunc, handoffIDParam string, a agentpkg.Agent) *httptest.ResponseRecorder {
	t.Helper()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", handoffIDParam)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/handoffs/"+handoffIDParam+"/accept", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(agentpkg.NewContext(req.Context(), a))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
