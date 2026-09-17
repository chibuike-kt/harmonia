package actionproposal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/companionrelay"
	"github.com/chibuike-kt/harmonia/internal/realtime"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/store"
	"github.com/chibuike-kt/harmonia/internal/user"
)

// TestIntegration_CreateFileEditProposal_RecordsRealEvent proves the
// real creation path: a real proposal row, action_type propose_file_edit
// (past the DB's own widened CHECK constraint — migrations/
// 0017_propose_file_edit), and a real ACTION_PROPOSED event carrying the
// real path, both durably committed.
func TestIntegration_CreateFileEditProposal_RecordsRealEvent(t *testing.T) {
	pool := connectActionProposalTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	beginner := store.PoolBeginner{Pool: pool}
	hub := realtime.NewHub()

	owner := seedActionProposalTestUser(t, ctx, users, "fileedit-create-owner-")
	rm, err := rooms.Create(ctx, &owner.ID, "fileedit-create-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	proposer, err := agents.Register(ctx, rm.ID, "ChatGPT", agent.ProviderOpenAI, nil, "hash-fileedit-create")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}

	sub, unsubscribe := hub.Subscribe(rm.ID)
	defer unsubscribe()

	p, err := CreateFileEditProposal(ctx, beginner, hub, rm.ID, proposer.ID, "src/main.go", "b2xk", "bmV3")
	if err != nil {
		t.Fatalf("CreateFileEditProposal: %v", err)
	}
	if p.ActionType != ActionProposeFileEdit || p.Status != StatusPending {
		t.Fatalf("proposal = %+v, want pending propose_file_edit", p)
	}

	var eventCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE room_id = $1 AND type = 'ACTION_PROPOSED'`, rm.ID).Scan(&eventCount); err != nil {
		t.Fatalf("count ACTION_PROPOSED events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("ACTION_PROPOSED events = %d, want 1", eventCount)
	}

	select {
	case msg := <-sub:
		if msg.Kind != realtime.KindEvent || msg.Event == nil {
			t.Fatalf("published message = %+v, want a real event", msg)
		}
		if msg.Event.Payload["path"] != "src/main.go" {
			t.Fatalf("published event payload = %+v, missing real path", msg.Event.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CreateFileEditProposal never published its real event")
	}
}

// TestIntegration_ApproveHandler_FileEdit_DispatchesRealWrite is the
// central proof for this whole feature: approving a propose_file_edit
// proposal makes a real companionrelay.Dispatch call, which — standing
// in for the reviewing human's own browser tab, exactly like
// companionrelay's own tests do for its other real caller — this test
// answers with a real Resolve, and confirms ApproveHandler both
// completed with the resolved proposal and genuinely wanted to write the
// exact content given as the override (the real per-hunk-merge path, not
// the proposal's own stored new_content).
func TestIntegration_ApproveHandler_FileEdit_DispatchesRealWrite(t *testing.T) {
	pool := connectActionProposalTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	proposals := NewStore(pool)
	beginner := store.PoolBeginner{Pool: pool}
	hub := realtime.NewHub()
	relay := companionrelay.NewCoordinator()

	owner := seedActionProposalTestUser(t, ctx, users, "fileedit-approve-owner-")
	rm, err := rooms.Create(ctx, &owner.ID, "fileedit-approve-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	proposer, err := agents.Register(ctx, rm.ID, "ChatGPT", agent.ProviderOpenAI, nil, "hash-fileedit-approve")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}

	p, err := proposals.Create(ctx, rm.ID, proposer.ID, ActionProposeFileEdit, map[string]any{
		"path":               "src/main.go",
		"old_content_base64": "b2xk",
		"new_content_base64": "d2hvbGVzYWxlLW5ldw==", // the stored full "accept all" content
	})
	if err != nil {
		t.Fatalf("seed proposal: %v", err)
	}

	h := proposals.ApproveHandler(rooms, beginner, hub, relay)

	// Standing in for the reviewing human's own browser tab: subscribe to
	// the room's real live channel, wait for the real companion_action
	// Dispatch publishes, and answer it with a real Resolve — the exact
	// round trip companionrelay's own tests already prove for its other
	// caller, now driven by a real approve request instead of a direct
	// Dispatch call.
	sub, unsubscribe := hub.Subscribe(rm.ID)
	defer unsubscribe()

	mergedContentB64 := "bWVyZ2VkLWNvbnRlbnQ=" // the per-hunk-merged real result
	dispatchDone := make(chan struct{})
	go func() {
		defer close(dispatchDone)
		deadline := time.After(5 * time.Second)
		// The room's live channel also carries the real ACTION.RESOLVE
		// event ApproveHandler publishes before it dispatches the write —
		// skip past that one and wait for the real companion_action.
		for {
			select {
			case action := <-sub:
				if action.CompanionAction == nil {
					continue
				}
				if action.CompanionAction.Type != "write_file" ||
					action.CompanionAction.Path != "src/main.go" ||
					action.CompanionAction.Data != mergedContentB64 {
					t.Errorf("dispatched action = %+v, want write_file for src/main.go with the merged override content", action.CompanionAction)
					return
				}
				_ = relay.Resolve(action.CompanionAction.ID, companionrelay.Result{Output: "written"})
				return
			case <-deadline:
				t.Error("never received the real companion_action dispatch")
				return
			}
		}
	}()

	body, _ := json.Marshal(approveRequest{ContentBase64: &mergedContentB64})
	req := httptest.NewRequestWithContext(user.NewContext(ctx, owner), http.MethodPost, "/v1/action_proposals/"+p.ID.String()+"/approve", strings.NewReader(string(body)))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", p.ID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	<-dispatchDone
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
}

// TestIntegration_ApproveHandler_FileEdit_RelayTimeoutReportsRealFailure
// proves the "reviewer's tab closed mid-review" case: no browser ever
// answers the dispatch, and ApproveHandler reports the real failure —
// the approval itself still committed (a human's decision stays durable
// regardless of whether the mechanical write landed), but the response
// says plainly that the write didn't happen, rather than a bare 200
// implying it did.
func TestIntegration_ApproveHandler_FileEdit_RelayTimeoutReportsRealFailure(t *testing.T) {
	pool := connectActionProposalTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	proposals := NewStore(pool)
	beginner := store.PoolBeginner{Pool: pool}
	hub := realtime.NewHub()
	relay := companionrelay.NewCoordinator()

	owner := seedActionProposalTestUser(t, ctx, users, "fileedit-timeout-owner-")
	rm, err := rooms.Create(ctx, &owner.ID, "fileedit-timeout-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	proposer, err := agents.Register(ctx, rm.ID, "ChatGPT", agent.ProviderOpenAI, nil, "hash-fileedit-timeout")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	p, err := proposals.Create(ctx, rm.ID, proposer.ID, ActionProposeFileEdit, map[string]any{
		"path":               "src/orphaned.go",
		"old_content_base64": "b2xk",
		"new_content_base64": "bmV3",
	})
	if err != nil {
		t.Fatalf("seed proposal: %v", err)
	}

	// Nobody ever subscribes or resolves — simulating no browser tab
	// watching this room right now.
	h := proposals.ApproveHandler(rooms, beginner, hub, relay)
	req := httptest.NewRequestWithContext(user.NewContext(ctx, owner), http.MethodPost, "/v1/action_proposals/"+p.ID.String()+"/approve", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", p.ID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	start := time.Now()
	h.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}
	if elapsed > 25*time.Second {
		t.Fatalf("took %s — not bounded by fileEditDispatchTimeout", elapsed)
	}

	resolved, err := proposals.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if resolved.Status != StatusApproved {
		t.Fatalf("Status = %q, want %q — the human's own approval must stay durable even though the write failed", resolved.Status, StatusApproved)
	}
}

// TestIntegration_GetHandler_ReturnsFullPayloadOwnerOnly proves the real
// fetch a diff view uses to get a file edit's actual old/new content —
// the ACTION.PROPOSE event itself never carries the real content, only a
// summary — and that only the room's real owner can read it.
func TestIntegration_GetHandler_ReturnsFullPayloadOwnerOnly(t *testing.T) {
	pool := connectActionProposalTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	proposals := NewStore(pool)

	owner := seedActionProposalTestUser(t, ctx, users, "fileedit-get-owner-")
	other := seedActionProposalTestUser(t, ctx, users, "fileedit-get-other-")
	rm, err := rooms.Create(ctx, &owner.ID, "fileedit-get-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	proposer, err := agents.Register(ctx, rm.ID, "ChatGPT", agent.ProviderOpenAI, nil, "hash-fileedit-get")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	p, err := proposals.Create(ctx, rm.ID, proposer.ID, ActionProposeFileEdit, map[string]any{
		"path":               "src/main.go",
		"old_content_base64": "b2xk",
		"new_content_base64": "bmV3",
	})
	if err != nil {
		t.Fatalf("seed proposal: %v", err)
	}

	h := proposals.GetHandler(rooms)
	get := func(caller user.User) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(user.NewContext(ctx, caller), http.MethodGet, "/v1/action_proposals/"+p.ID.String(), nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", p.ID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	forbiddenRec := get(other)
	if forbiddenRec.Code != http.StatusForbidden {
		t.Fatalf("non-owner status = %d, want %d", forbiddenRec.Code, http.StatusForbidden)
	}

	okRec := get(owner)
	if okRec.Code != http.StatusOK {
		t.Fatalf("owner status = %d, want %d, body = %s", okRec.Code, http.StatusOK, okRec.Body.String())
	}
	var got Proposal
	if err := json.Unmarshal(okRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Payload["old_content_base64"] != "b2xk" || got.Payload["new_content_base64"] != "bmV3" {
		t.Fatalf("payload = %+v, missing the real old/new content", got.Payload)
	}
}
