package task

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/protocol"
	"github.com/chibuike-kt/harmonia/internal/realtime"
	"github.com/chibuike-kt/harmonia/internal/store"
)

// fakeRow answers Scan for the exact column set task.Store.Create RETURNS —
// enough to let a fake transaction stand in for Postgres in a unit test.
type fakeRow struct{}

func (fakeRow) Scan(dest ...any) error {
	for _, d := range dest {
		switch v := d.(type) {
		case *uuid.UUID:
			*v = uuid.New()
		case **uuid.UUID:
			*v = nil
		case *string:
			*v = "objective"
		case *Status:
			*v = StatusQueued
		case *time.Time:
			*v = time.Now()
		}
	}
	return nil
}

// fakeTx is a store.Tx that fails on a configured call number, so a test
// can force the event write (or any other call) to fail without a live
// Postgres transaction to actually break.
type fakeTx struct {
	calls      int
	failOnCall int
	failCommit bool
	committed  bool
	rolledBack bool
}

func (f *fakeTx) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	f.calls++
	return fakeRow{}
}

func (f *fakeTx) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	f.calls++
	if f.calls == f.failOnCall {
		return pgconn.CommandTag{}, errors.New("fake: exec failed")
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (f *fakeTx) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	f.calls++
	return nil, errors.New("fake: Query not supported")
}

func (f *fakeTx) Commit(context.Context) error {
	if f.failCommit {
		return errors.New("fake: commit failed")
	}
	f.committed = true
	return nil
}

func (f *fakeTx) Rollback(context.Context) error {
	f.rolledBack = true
	return nil
}

type fakeBeginner struct {
	tx *fakeTx
}

func (b fakeBeginner) Begin(context.Context) (store.Tx, error) {
	return b.tx, nil
}

// fakePublisher records every Publish call instead of fanning out to real
// subscribers — enough to prove a handler did or didn't publish, and with
// what, without a real Hub.
type fakePublisher struct {
	calls []publishCall
}

type publishCall struct {
	roomID uuid.UUID
	msg    realtime.Message
}

func (f *fakePublisher) Publish(roomID uuid.UUID, msg realtime.Message) {
	f.calls = append(f.calls, publishCall{roomID: roomID, msg: msg})
}

// TestCreateHandler_EventFailureRollsBackTask confirms the task write and
// the event write are one transaction: when the event write (the second
// store call — the first is the task INSERT) fails, the handler must
// never commit, and must roll back instead of leaving the task write to
// stand alone. No live Postgres involved — the fake tx is the whole point.
// It also confirms the ordering step 2 of the Phase 3 build brief calls
// for: a rolled-back transaction must never reach Publish at all.
func TestCreateHandler_EventFailureRollsBackTask(t *testing.T) {
	tx := &fakeTx{failOnCall: 2}
	pub := &fakePublisher{}
	h := (&Store{}).CreateHandler(fakeBeginner{tx: tx}, pub)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/tasks", strings.NewReader(`{"objective":"write the report"}`))
	req = req.WithContext(agent.NewContext(req.Context(), agent.Agent{ID: uuid.New(), RoomID: uuid.New()}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if tx.calls != 2 {
		t.Fatalf("expected exactly 2 store calls (task write, event write), got %d", tx.calls)
	}
	if tx.committed {
		t.Fatal("transaction must not commit when the event write fails")
	}
	if !tx.rolledBack {
		t.Fatal("transaction must roll back when the event write fails")
	}
	if len(pub.calls) != 0 {
		t.Fatalf("expected zero Publish calls when the transaction rolled back, got %d: %+v", len(pub.calls), pub.calls)
	}
}

// TestCreateHandler_CommitFailureNeverPublishes forces tx.Commit itself to
// fail — the task and event writes both succeed, but the transaction as a
// whole never lands. Publish must never be called in this case either:
// the ordering the brief calls for is "after commit succeeds," not
// "after the writes are issued."
func TestCreateHandler_CommitFailureNeverPublishes(t *testing.T) {
	tx := &fakeTx{failCommit: true}
	pub := &fakePublisher{}
	h := (&Store{}).CreateHandler(fakeBeginner{tx: tx}, pub)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/tasks", strings.NewReader(`{"objective":"write the report"}`))
	req = req.WithContext(agent.NewContext(req.Context(), agent.Agent{ID: uuid.New(), RoomID: uuid.New()}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if tx.committed {
		t.Fatal("committed must be false when Commit itself returns an error")
	}
	if len(pub.calls) != 0 {
		t.Fatalf("expected zero Publish calls when Commit fails, got %d: %+v", len(pub.calls), pub.calls)
	}
}

// TestCreateHandler_PublishesAfterCommit confirms the positive case: once
// Commit actually succeeds, the handler publishes exactly the same
// envelope it recorded, to the task's room, exactly once.
func TestCreateHandler_PublishesAfterCommit(t *testing.T) {
	tx := &fakeTx{}
	pub := &fakePublisher{}
	h := (&Store{}).CreateHandler(fakeBeginner{tx: tx}, pub)

	roomID := uuid.New()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/tasks", strings.NewReader(`{"objective":"write the report"}`))
	req = req.WithContext(agent.NewContext(req.Context(), agent.Agent{ID: uuid.New(), RoomID: roomID}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if !tx.committed {
		t.Fatal("expected the transaction to commit on the happy path")
	}

	var created Task
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(pub.calls) != 1 {
		t.Fatalf("expected exactly 1 Publish call, got %d: %+v", len(pub.calls), pub.calls)
	}
	got := pub.calls[0]
	// fakeRow.Scan fills every *uuid.UUID destination with a fresh random
	// value rather than echoing back what was inserted, so the room id
	// Publish receives is compared against the created task's own RoomID
	// from the response — the value the handler actually had in hand —
	// not against the room id used to build the request.
	if got.roomID != created.RoomID {
		t.Fatalf("Publish roomID = %s, want %s (the created task's own RoomID)", got.roomID, created.RoomID)
	}
	if got.msg.Kind != realtime.KindEvent || got.msg.Event == nil {
		t.Fatalf("Publish msg = %+v, want a KindEvent envelope", got.msg)
	}
	if got.msg.Event.Type != protocol.OpTaskCreate {
		t.Fatalf("Publish envelope Type = %q, want %q", got.msg.Event.Type, protocol.OpTaskCreate)
	}
}
