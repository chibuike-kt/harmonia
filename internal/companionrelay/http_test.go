package companionrelay

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chibuike-kt/harmonia/internal/realtime"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/user"
)

func TestResultHandler_Unauthenticated(t *testing.T) {
	h := ResultHandler(nil, NewCoordinator())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/rooms/x/companion_actions/y/result", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func withRoomAndActionIDParams(roomID, actionID uuid.UUID, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("room_id", roomID.String())
		rctx.URLParams.Add("action_id", actionID.String())
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx)))
	}
}

// TestIntegration_ResultHandler_OwnershipAndRealResolve is the real,
// end-to-end proof for the relay protocol's other half: a Coordinator
// with a real pending Dispatch, resolved by a real HTTP POST from the
// room's real owner; a non-owner's POST is rejected and never resolves
// the pending action; a POST for an action ID nobody is waiting on
// (arrived too late, or was never dispatched) gets 410 Gone, not a
// silent 204.
func TestIntegration_ResultHandler_OwnershipAndRealResolve(t *testing.T) {
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

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)

	owner, err := users.UpsertByGitHubID(ctx, "relay-result-owner-"+uuid.New().String(), "relay-result-owner", nil, nil, nil)
	if err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	other, err := users.UpsertByGitHubID(ctx, "relay-result-other-"+uuid.New().String(), "relay-result-other", nil, nil, nil)
	if err != nil {
		t.Fatalf("seed other user: %v", err)
	}
	rm, err := rooms.Create(ctx, &owner.ID, "relay-result-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	c := NewCoordinator()
	h := ResultHandler(rooms, c)
	hub := realtime.NewHub()
	sub, unsubscribe := hub.Subscribe(rm.ID)
	defer unsubscribe()

	// A real pending action — an actual Dispatch call in flight, not a
	// hand-constructed stand-in, so the handler's real interaction with
	// Resolve's cleanup (via Dispatch's own defer) is what's under test.
	dispatchDone := make(chan struct{})
	go func() {
		defer close(dispatchDone)
		_, _ = Dispatch(context.Background(), c, hub, rm.ID, "shell_input", "", "echo hi\n", 5*time.Second)
	}()
	var action realtime.Message
	select {
	case action = <-sub:
	case <-time.After(2 * time.Second):
		t.Fatal("Dispatch never published its action")
	}
	actionID := action.CompanionAction.ID

	body, _ := json.Marshal(resultRequest{Output: "real output"})

	// Non-owner: rejected, and must not resolve the pending action.
	forbiddenReq := httptest.NewRequestWithContext(user.NewContext(ctx, other), http.MethodPost, "/v1/rooms/"+rm.ID.String()+"/companion_actions/"+actionID.String()+"/result", bytes.NewReader(body))
	forbiddenRec := httptest.NewRecorder()
	withRoomAndActionIDParams(rm.ID, actionID, h).ServeHTTP(forbiddenRec, forbiddenReq)
	if forbiddenRec.Code != http.StatusForbidden {
		t.Fatalf("non-owner status = %d, want %d, body = %s", forbiddenRec.Code, http.StatusForbidden, forbiddenRec.Body.String())
	}
	if c.PendingCount() != 1 {
		t.Fatal("a rejected (403) result POST must not resolve the pending action")
	}

	// The real owner resolves it for real.
	okReq := httptest.NewRequestWithContext(user.NewContext(ctx, owner), http.MethodPost, "/v1/rooms/"+rm.ID.String()+"/companion_actions/"+actionID.String()+"/result", bytes.NewReader(body))
	okRec := httptest.NewRecorder()
	withRoomAndActionIDParams(rm.ID, actionID, h).ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusNoContent {
		t.Fatalf("owner status = %d, want %d, body = %s", okRec.Code, http.StatusNoContent, okRec.Body.String())
	}

	select {
	case <-dispatchDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the owner's real POST didn't actually unblock the pending Dispatch call")
	}

	// A second POST for the same, now-resolved action ID: genuinely too
	// late, must not report success.
	lateRec := httptest.NewRecorder()
	lateReq := httptest.NewRequestWithContext(user.NewContext(ctx, owner), http.MethodPost, "/v1/rooms/"+rm.ID.String()+"/companion_actions/"+actionID.String()+"/result", bytes.NewReader(body))
	withRoomAndActionIDParams(rm.ID, actionID, h).ServeHTTP(lateRec, lateReq)
	if lateRec.Code != http.StatusGone {
		t.Fatalf("late/unknown-action status = %d, want %d, body = %s", lateRec.Code, http.StatusGone, lateRec.Body.String())
	}
}
