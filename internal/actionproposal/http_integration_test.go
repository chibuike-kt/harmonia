package actionproposal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/handoff"
	"github.com/chibuike-kt/harmonia/internal/realtime"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/store"
	"github.com/chibuike-kt/harmonia/internal/task"
	"github.com/chibuike-kt/harmonia/internal/user"
)

func connectActionProposalTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("HARMONIA_DATABASE_URL")
	if dbURL == "" {
		t.Skip("HARMONIA_DATABASE_URL not set; skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedActionProposalTestUser(t *testing.T, ctx context.Context, users *user.Store, githubIDPrefix string) user.User {
	t.Helper()
	u, err := users.UpsertByGitHubID(ctx, githubIDPrefix+uuid.New().String(), githubIDPrefix+"user", nil, nil, nil)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u
}

func withIDParam(req *http.Request, id string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// TestIntegration_ApproveHandler_ExecutesRealHandoffAndLeavesHandoffStatusMachineUntouched
// is ADR-006 batch C's central approval proof: approving a
// request_handoff proposal against real Postgres produces a real
// handoff row via the exact same path a human's own direct
// POST /v1/handoffs call would use — status REQUESTED, the receiving
// agent's own accept/reject state machine (internal/handoff's
// ErrNotRequested-guarded Accept) completely untouched by the approval
// itself, since ADR-006 is explicit these are two different questions.
// Requires a live Postgres — run via `make test-integration` after
// `make up`.
func TestIntegration_ApproveHandler_ExecutesRealHandoffAndLeavesHandoffStatusMachineUntouched(t *testing.T) {
	pool := connectActionProposalTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	tasks := task.NewStore(pool)
	handoffs := handoff.NewStore(pool)
	proposals := NewStore(pool)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedActionProposalTestUser(t, ctx, users, "actionproposal-approve-owner-")
	rm, err := rooms.Create(ctx, &owner.ID, "actionproposal-approve-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	from, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-approve-from")
	if err != nil {
		t.Fatalf("register from agent: %v", err)
	}
	to, err := agents.Register(ctx, rm.ID, "GPT", agent.ProviderOpenAI, nil, "hash-approve-to")
	if err != nil {
		t.Fatalf("register to agent: %v", err)
	}
	tk, err := tasks.Create(ctx, rm.ID, "the task being handed off", nil)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	p, err := proposals.Create(ctx, rm.ID, from.ID, ActionRequestHandoff, map[string]any{
		"task_id":     tk.ID.String(),
		"to_agent_id": to.ID.String(),
		"summary":     "handing this off",
		"completed":   []string{"step one"},
		"remaining":   []string{"step two"},
		"risks":       []string{},
	})
	if err != nil {
		t.Fatalf("seed proposal: %v", err)
	}

	h := proposals.ApproveHandler(rooms, beginner, realtime.NewHub())

	doApprove := func(caller user.User, id string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(user.NewContext(ctx, caller), http.MethodPost, "/v1/action_proposals/"+id+"/approve", nil)
		req = withIDParam(req, id)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := doApprove(owner, p.ID.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got Proposal
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Status != StatusApproved {
		t.Fatalf("Status = %q, want %q", got.Status, StatusApproved)
	}
	if got.ResolvedAt == nil {
		t.Fatal("expected ResolvedAt to be set")
	}

	var handoffCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM handoffs WHERE room_id = $1`, rm.ID).Scan(&handoffCount); err != nil {
		t.Fatalf("count handoffs: %v", err)
	}
	if handoffCount != 1 {
		t.Fatalf("handoffs for room = %d, want exactly 1", handoffCount)
	}

	var hID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM handoffs WHERE room_id = $1`, rm.ID).Scan(&hID); err != nil {
		t.Fatalf("find created handoff: %v", err)
	}

	// The receiving agent's own accept/reject state machine — untouched
	// by approval itself, exactly as if a human had called
	// POST /v1/handoffs directly and no one had accepted yet.
	if err := handoffs.Accept(ctx, hID); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if err := handoffs.Accept(ctx, hID); err == nil {
		t.Fatal("expected second Accept to fail — handoff already accepted")
	}
}

// TestIntegration_ApproveHandler_Ownership exercises the same
// 404-then-403 pattern every other room-scoped route uses, reached via
// the proposal's own room_id rather than a room_id path segment.
func TestIntegration_ApproveHandler_Ownership(t *testing.T) {
	pool := connectActionProposalTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	tasks := task.NewStore(pool)
	proposals := NewStore(pool)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedActionProposalTestUser(t, ctx, users, "actionproposal-ownership-owner-")
	other := seedActionProposalTestUser(t, ctx, users, "actionproposal-ownership-other-")
	rm, err := rooms.Create(ctx, &owner.ID, "actionproposal-ownership-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	from, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-ownership-from")
	if err != nil {
		t.Fatalf("register from agent: %v", err)
	}
	to, err := agents.Register(ctx, rm.ID, "GPT", agent.ProviderOpenAI, nil, "hash-ownership-to")
	if err != nil {
		t.Fatalf("register to agent: %v", err)
	}
	tk, err := tasks.Create(ctx, rm.ID, "task", nil)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	p, err := proposals.Create(ctx, rm.ID, from.ID, ActionRequestHandoff, map[string]any{
		"task_id": tk.ID.String(), "to_agent_id": to.ID.String(), "summary": "s",
	})
	if err != nil {
		t.Fatalf("seed proposal: %v", err)
	}

	h := proposals.ApproveHandler(rooms, beginner, realtime.NewHub())

	do := func(caller user.User, id string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(user.NewContext(ctx, caller), http.MethodPost, "/v1/action_proposals/"+id+"/approve", nil)
		req = withIDParam(req, id)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := do(owner, uuid.New().String()); rec.Code != http.StatusNotFound {
		t.Fatalf("nonexistent proposal status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if rec := do(other, p.ID.String()); rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

// TestIntegration_ApproveHandler_AlreadyResolved confirms Resolve's own
// atomic pending -> approved/rejected transition is what the HTTP layer
// surfaces as a real 409, not a silent no-op or a 500.
func TestIntegration_ApproveHandler_AlreadyResolved(t *testing.T) {
	pool := connectActionProposalTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	tasks := task.NewStore(pool)
	proposals := NewStore(pool)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedActionProposalTestUser(t, ctx, users, "actionproposal-already-resolved-")
	rm, err := rooms.Create(ctx, &owner.ID, "actionproposal-already-resolved-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	from, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-already-from")
	if err != nil {
		t.Fatalf("register from agent: %v", err)
	}
	to, err := agents.Register(ctx, rm.ID, "GPT", agent.ProviderOpenAI, nil, "hash-already-to")
	if err != nil {
		t.Fatalf("register to agent: %v", err)
	}
	tk, err := tasks.Create(ctx, rm.ID, "task", nil)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	p, err := proposals.Create(ctx, rm.ID, from.ID, ActionRequestHandoff, map[string]any{
		"task_id": tk.ID.String(), "to_agent_id": to.ID.String(), "summary": "s",
	})
	if err != nil {
		t.Fatalf("seed proposal: %v", err)
	}

	approve := proposals.ApproveHandler(rooms, beginner, realtime.NewHub())
	reject := proposals.RejectHandler(rooms, beginner, realtime.NewHub())

	do := func(h http.HandlerFunc) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(user.NewContext(ctx, owner), http.MethodPost, "/v1/action_proposals/"+p.ID.String()+"/approve", nil)
		req = withIDParam(req, p.ID.String())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := do(approve); rec.Code != http.StatusOK {
		t.Fatalf("first approve status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec := do(approve); rec.Code != http.StatusConflict {
		t.Fatalf("second approve status = %d, want %d, body = %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if rec := do(reject); rec.Code != http.StatusConflict {
		t.Fatalf("reject-after-approve status = %d, want %d, body = %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

// TestIntegration_RejectHandler_DoesNotExecuteAnything confirms
// rejecting a request_handoff proposal only ever resolves it — no
// handoff is ever created, unlike approval.
func TestIntegration_RejectHandler_DoesNotExecuteAnything(t *testing.T) {
	pool := connectActionProposalTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	tasks := task.NewStore(pool)
	proposals := NewStore(pool)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedActionProposalTestUser(t, ctx, users, "actionproposal-reject-")
	rm, err := rooms.Create(ctx, &owner.ID, "actionproposal-reject-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	from, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-reject-from")
	if err != nil {
		t.Fatalf("register from agent: %v", err)
	}
	to, err := agents.Register(ctx, rm.ID, "GPT", agent.ProviderOpenAI, nil, "hash-reject-to")
	if err != nil {
		t.Fatalf("register to agent: %v", err)
	}
	tk, err := tasks.Create(ctx, rm.ID, "task", nil)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	p, err := proposals.Create(ctx, rm.ID, from.ID, ActionRequestHandoff, map[string]any{
		"task_id": tk.ID.String(), "to_agent_id": to.ID.String(), "summary": "s",
	})
	if err != nil {
		t.Fatalf("seed proposal: %v", err)
	}

	h := proposals.RejectHandler(rooms, beginner, realtime.NewHub())
	req := httptest.NewRequestWithContext(user.NewContext(ctx, owner), http.MethodPost, "/v1/action_proposals/"+p.ID.String()+"/reject", nil)
	req = withIDParam(req, p.ID.String())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got Proposal
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Status != StatusRejected {
		t.Fatalf("Status = %q, want %q", got.Status, StatusRejected)
	}

	var handoffCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM handoffs WHERE room_id = $1`, rm.ID).Scan(&handoffCount); err != nil {
		t.Fatalf("count handoffs: %v", err)
	}
	if handoffCount != 0 {
		t.Fatalf("handoffs for room = %d, want 0 — reject must never execute anything", handoffCount)
	}
}
