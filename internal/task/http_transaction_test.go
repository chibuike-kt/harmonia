package task

import (
	"context"
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

// TestCreateHandler_EventFailureRollsBackTask confirms the task write and
// the event write are one transaction: when the event write (the second
// store call — the first is the task INSERT) fails, the handler must
// never commit, and must roll back instead of leaving the task write to
// stand alone. No live Postgres involved — the fake tx is the whole point.
func TestCreateHandler_EventFailureRollsBackTask(t *testing.T) {
	tx := &fakeTx{failOnCall: 2}
	h := (&Store{}).CreateHandler(fakeBeginner{tx: tx})

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
}
