package companionrelay

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/realtime"
)

// TestDispatch_RoundTripsThroughTheRealHub is the real proof for the
// relay protocol's happy path: Dispatch publishes onto roomID's real Hub
// exactly like the SSE stream a browser tab is actually subscribed to
// would deliver it, and a Resolve call — standing in for that tab's own
// POST once the companion answers — is what makes Dispatch return.
func TestDispatch_RoundTripsThroughTheRealHub(t *testing.T) {
	hub := realtime.NewHub()
	roomID := uuid.New()
	c := NewCoordinator()

	sub, unsubscribe := hub.Subscribe(roomID)
	defer unsubscribe()

	type dispatchOutcome struct {
		result Result
		err    error
	}
	done := make(chan dispatchOutcome, 1)
	go func() {
		r, err := Dispatch(context.Background(), c, hub, roomID, "shell_input", "echo hi\n", 5*time.Second)
		done <- dispatchOutcome{r, err}
	}()

	// The "frontend": receive the real published action off the real
	// Hub, exactly as a subscribed browser tab's SSE stream would.
	var action realtime.Message
	select {
	case action = <-sub:
	case <-time.After(2 * time.Second):
		t.Fatal("Dispatch never published a companion_action onto the room's hub")
	}
	if action.Kind != realtime.KindCompanionAction {
		t.Fatalf("published kind = %q, want %q", action.Kind, realtime.KindCompanionAction)
	}
	if action.CompanionAction == nil {
		t.Fatal("published message carries no CompanionAction payload")
	}
	if action.CompanionAction.Actor != "agent" {
		t.Fatalf("Actor = %q, want %q — every relayed action must be unambiguously agent-attributed", action.CompanionAction.Actor, "agent")
	}
	if action.CompanionAction.Type != "shell_input" || action.CompanionAction.Data != "echo hi\n" {
		t.Fatalf("action = %+v, doesn't match what Dispatch was asked to relay", action.CompanionAction)
	}
	if action.CompanionAction.RoomID != roomID {
		t.Fatalf("action.RoomID = %s, want %s", action.CompanionAction.RoomID, roomID)
	}

	if c.PendingCount() != 1 {
		t.Fatalf("PendingCount = %d while Dispatch is still waiting, want 1", c.PendingCount())
	}

	// The "frontend" POSTing the companion's real result back.
	if err := c.Resolve(action.CompanionAction.ID, Result{Output: "hi\n"}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("Dispatch returned an error after a real Resolve: %v", out.err)
		}
		if out.result.Output != "hi\n" {
			t.Fatalf("Dispatch result = %+v, want Output %q", out.result, "hi\n")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Dispatch never returned after Resolve was called")
	}

	if c.PendingCount() != 0 {
		t.Fatalf("PendingCount = %d after Dispatch returned, want 0 — a resolved action must be cleaned up", c.PendingCount())
	}
}

// TestDispatch_TimesOutCleanlyWhenNoResultArrives is this package's own
// version of the exact lesson internal/provider/timeout.go already
// learned: a closed browser tab, a companion that never answers, or any
// other reason the result POST never arrives must not hang Dispatch
// forever. Real proof: Dispatch given a short, real timeout and no
// Resolve call at all returns within a bounded window, with a real
// error, and leaves nothing pending behind.
func TestDispatch_TimesOutCleanlyWhenNoResultArrives(t *testing.T) {
	hub := realtime.NewHub()
	roomID := uuid.New()
	c := NewCoordinator()

	sub, unsubscribe := hub.Subscribe(roomID)
	defer unsubscribe()

	start := time.Now()
	_, err := Dispatch(context.Background(), c, hub, roomID, "shell_input", "rm -rf /\n", 200*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Dispatch returned no error despite no Resolve ever being called — this is the exact silent-hang shape the relay protocol must not have")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %q, want it to name a real timeout, not some other failure", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Dispatch took %s to give up on a 200ms timeout — it isn't bounded by the timeout it was given", elapsed)
	}
	if c.PendingCount() != 0 {
		t.Fatalf("PendingCount = %d after a timed-out Dispatch, want 0 — a timed-out action must not leak in the pending map", c.PendingCount())
	}

	// Drain the one real published action so the test doesn't leave an
	// unread message sitting in the subscriber's buffer unexamined.
	select {
	case <-sub:
	default:
	}
}

// TestDispatch_ContextCanceledReturnsCleanly proves the other real
// cancellation path — the caller's own ctx (an HTTP request context, in
// the real Batch 2 caller this Coordinator doesn't have yet) being
// canceled mid-flight — behaves the same way: a clean, bounded return,
// not a hang, and no leaked pending entry.
func TestDispatch_ContextCanceledReturnsCleanly(t *testing.T) {
	hub := realtime.NewHub()
	roomID := uuid.New()
	c := NewCoordinator()

	sub, unsubscribe := hub.Subscribe(roomID)
	defer unsubscribe()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := Dispatch(ctx, c, hub, roomID, "shell_input", "echo hi\n", 30*time.Second)
		done <- err
	}()

	select {
	case <-sub:
	case <-time.After(2 * time.Second):
		t.Fatal("Dispatch never published its action")
	}
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Dispatch returned no error after its context was canceled")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Dispatch didn't return promptly after its context was canceled")
	}
	if c.PendingCount() != 0 {
		t.Fatalf("PendingCount = %d after a context-canceled Dispatch, want 0", c.PendingCount())
	}
}

// TestResolve_UnknownActionReturnsError proves a result that arrives for
// an action ID this Coordinator isn't waiting on — never dispatched, or
// already timed out — gets a clear, honest answer rather than silently
// vanishing or succeeding.
func TestResolve_UnknownActionReturnsError(t *testing.T) {
	c := NewCoordinator()
	if err := c.Resolve(uuid.New(), Result{Output: "too late"}); err != ErrUnknownAction {
		t.Fatalf("err = %v, want %v", err, ErrUnknownAction)
	}
}
