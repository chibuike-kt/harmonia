// Package companionrelay is the backend half of ADR-010's relay
// protocol — the only path an agent's file-write or shell-exec tool call
// has to reach a human's local companion process, since a cloud-hosted
// backend can never open a connection to a loopback-bound process
// directly. The real path is: this package publishes the action onto the
// room's existing live channel (internal/realtime's Hub/SSE stream, the
// same one every chat message and event already rides); the one browser
// tab with that room's companion WebSocket open receives it, forwards it
// to the companion, and POSTs the real result back to this package's own
// HTTP handler; Dispatch, which has been blocking the whole time,
// returns it to the caller.
//
// The one thing this package exists to get right is what CallWithTimeout
// (internal/provider/timeout.go) already got right for provider calls:
// nothing that calls Dispatch can hang forever. A closed tab, a crashed
// browser, a companion process that quit mid-command — none of those
// ever send the POST that resolves a pending action, so Dispatch must
// time out cleanly on its own rather than waiting on a call back that
// may never come. Same lesson, same shape, applied one layer further out.
package companionrelay

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/realtime"
)

// Result is what the frontend's POST back to this package's HTTP handler
// carries — the real outcome of relaying one action to the companion and
// back, or a real error if the companion itself reported one (a
// path-traversal rejection, a missing open folder, and so on).
type Result struct {
	Output string
	Err    string
}

// ErrUnknownAction is returned by Resolve when actionID names an action
// this Coordinator never dispatched, or one that already resolved or
// timed out — the frontend's POST landing after Dispatch already gave up
// (a slow companion, a browser tab closed right as the result arrived)
// must get a clear, honest answer, not a silent success that implies
// someone is still listening.
var ErrUnknownAction = errors.New("companionrelay: unknown or already-completed action")

// Coordinator correlates one Dispatch call with the one Resolve call
// that eventually answers it, entirely in memory — a real network round
// trip through the browser in between, but nothing here is durable or
// needs to be: a pending action that never resolves before its deadline
// is simply gone, the same as any other request this server never
// finished serving.
type Coordinator struct {
	mu      sync.Mutex
	pending map[uuid.UUID]chan Result
}

// NewCoordinator builds an empty Coordinator.
func NewCoordinator() *Coordinator {
	return &Coordinator{pending: make(map[uuid.UUID]chan Result)}
}

// PendingCount reports how many actions are currently awaiting a result
// — exported for tests to prove Dispatch cleans up after itself on every
// exit path (result, timeout, or context cancellation), not just the
// happy one; a leaked entry here is exactly the kind of slow, silent
// leak subscriberCount already exists to catch on the Hub side.
func (c *Coordinator) PendingCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pending)
}

// Dispatch publishes action onto roomID's live channel via hub and
// blocks until either a real Resolve call answers it, ctx is done, or
// timeout elapses — whichever comes first. Every exit path removes
// action's entry from the pending map before returning, so a
// caller that gives up never leaves this Coordinator waiting on a result
// that will now never be read.
func Dispatch(ctx context.Context, c *Coordinator, hub realtime.Publisher, roomID uuid.UUID, actionType, path, data string, timeout time.Duration) (Result, error) {
	actionID := uuid.New()
	resultCh := make(chan Result, 1)

	c.mu.Lock()
	c.pending[actionID] = resultCh
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, actionID)
		c.mu.Unlock()
	}()

	hub.Publish(roomID, realtime.NewCompanionActionMessage(realtime.CompanionAction{
		ID:     actionID,
		RoomID: roomID,
		Type:   actionType,
		Path:   path,
		Data:   data,
		Actor:  "agent",
	}))

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case r := <-resultCh:
		return r, nil
	case <-ctx.Done():
		return Result{}, fmt.Errorf("companionrelay: action canceled: %w", ctx.Err())
	case <-timer.C:
		return Result{}, fmt.Errorf("companionrelay: action %s timed out after %s waiting for the companion's result — the browser tab may have closed mid-command", actionID, timeout)
	}
}

// Resolve delivers result to the goroutine blocked in Dispatch for
// actionID, if one is still waiting. Returns ErrUnknownAction if
// actionID isn't currently pending — either it was never dispatched, or
// Dispatch already returned (timeout or cancellation) and cleaned up its
// entry, in which case this result has genuinely arrived too late and
// there is nothing left to deliver it to.
func (c *Coordinator) Resolve(actionID uuid.UUID, result Result) error {
	c.mu.Lock()
	ch, ok := c.pending[actionID]
	c.mu.Unlock()
	if !ok {
		return ErrUnknownAction
	}
	// Buffered size 1 — this send never blocks even if, in a genuine
	// race, Dispatch's own timeout fires the same instant and nobody
	// ever reads it back out; the entry is removed by Dispatch's own
	// defer regardless of which side "wins."
	ch <- result
	return nil
}
