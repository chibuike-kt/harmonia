package handoff

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/protocol"
	"github.com/chibuike-kt/harmonia/internal/realtime"
	"github.com/chibuike-kt/harmonia/internal/store"
)

// withHandoffIDParam attaches idParam as chi's "id" URL param — the way
// AcceptHandler reads the handoff id out of the request in production,
// not a shortcut around it.
func withHandoffIDParam(req *http.Request, idParam string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", idParam)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// fakeHandoffRow answers Scan for the exact column order handoff.Store's
// SELECT/RETURNING queries use: id, room_id, task_id, from_agent_id,
// to_agent_id, summary, completed, remaining, artifacts, decisions,
// risks, status, created_at. Order-dependent rather than type-switched
// like task's fakeRow, because Handoff has five uuid.UUID columns in a
// row that a type switch alone can't tell apart.
type fakeHandoffRow struct{ h Handoff }

// scanAssign type-asserts dest to *T and stores v through it, in the
// checked (comma-ok) form errcheck's check-type-assertions requires —
// a bare dest[i].(*T) is flagged even though it can't fail here, since
// dest's shape is fixed by fakeHandoffRow's own column-order contract.
func scanAssign[T any](dest any, v T) error {
	p, ok := dest.(*T)
	if !ok {
		return fmt.Errorf("fakeHandoffRow: scan target is %T, want *%T", dest, v)
	}
	*p = v
	return nil
}

func (r fakeHandoffRow) Scan(dest ...any) error {
	if len(dest) != 13 {
		return fmt.Errorf("fakeHandoffRow: got %d scan targets, want 13", len(dest))
	}
	assignments := [13]error{
		scanAssign(dest[0], r.h.ID),
		scanAssign(dest[1], r.h.RoomID),
		scanAssign(dest[2], r.h.TaskID),
		scanAssign(dest[3], r.h.FromAgentID),
		scanAssign(dest[4], r.h.ToAgentID),
		scanAssign(dest[5], r.h.Summary),
		scanAssign(dest[6], r.h.Completed),
		scanAssign(dest[7], r.h.Remaining),
		scanAssign(dest[8], r.h.Artifacts),
		scanAssign(dest[9], r.h.Decisions),
		scanAssign(dest[10], r.h.Risks),
		scanAssign(dest[11], r.h.Status),
		scanAssign(dest[12], r.h.CreatedAt),
	}
	for _, err := range assignments {
		if err != nil {
			return err
		}
	}
	return nil
}

// fakeOuterQuerier stands in for the plain (non-transactional) pool
// AcceptHandler reads through for its pre-transaction GetByID lookup —
// enough to hand back one fixed Handoff row without live Postgres.
type fakeOuterQuerier struct {
	handoff Handoff
}

func (f fakeOuterQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	return fakeHandoffRow{h: f.handoff}
}

func (f fakeOuterQuerier) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("fake: Exec not supported on the outer querier")
}

func (f fakeOuterQuerier) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("fake: Query not supported on the outer querier")
}

// fakeTx is a store.Tx that fails on a configured call number, mirroring
// task's own fakeTx — enough to force the event write (or Commit itself)
// to fail without a live Postgres transaction to actually break.
type fakeTx struct {
	handoff    Handoff
	calls      int
	failOnCall int
	failCommit bool
	committed  bool
	rolledBack bool
}

func (f *fakeTx) QueryRow(context.Context, string, ...any) pgx.Row {
	f.calls++
	return fakeHandoffRow{h: f.handoff}
}

func (f *fakeTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	f.calls++
	if f.calls == f.failOnCall {
		return pgconn.CommandTag{}, errors.New("fake: exec failed")
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (f *fakeTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
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

// acceptTestFixture builds a REQUESTED handoff addressed to caller, and
// the caller's own agent identity, consistent enough with each other to
// pass AcceptHandler's room/ownership checks.
func acceptTestFixture() (Handoff, agent.Agent) {
	roomID := uuid.New()
	toAgentID := uuid.New()
	h := Handoff{
		ID:          uuid.New(),
		RoomID:      roomID,
		TaskID:      uuid.New(),
		FromAgentID: uuid.New(),
		ToAgentID:   toAgentID,
		Summary:     "handing off",
		Completed:   []string{"step 1"},
		Remaining:   []string{"step 2"},
		Artifacts:   []string{},
		Decisions:   []string{},
		Risks:       []string{},
		Status:      StatusRequested,
		CreatedAt:   time.Now(),
	}
	caller := agent.Agent{ID: toAgentID, RoomID: roomID}
	return h, caller
}

// TestAcceptHandler_EventFailureRollsBackAccept confirms the status
// update and the event write are one transaction: when the event write
// (the second store call inside the tx — Accept's UPDATE is the first,
// the post-accept GetByID is the second, the event INSERT is the third)
// fails, the handler must never commit and must roll back. It also
// confirms the Phase 3 build brief's step 2 ordering: a rolled-back
// transaction must never reach Publish at all.
func TestAcceptHandler_EventFailureRollsBackAccept(t *testing.T) {
	h, caller := acceptTestFixture()
	tx := &fakeTx{handoff: h, failOnCall: 3}
	pub := &fakePublisher{}
	s := &Store{pool: fakeOuterQuerier{handoff: h}}
	handler := s.AcceptHandler(fakeBeginner{tx: tx}, pub)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/handoffs/"+h.ID.String()+"/accept", nil)
	req = req.WithContext(agent.NewContext(req.Context(), caller))
	req = withHandoffIDParam(req, h.ID.String())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
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

// TestAcceptHandler_CommitFailureNeverPublishes forces tx.Commit itself
// to fail — the status update and event write both succeed, but the
// transaction as a whole never lands. Publish must never be called
// either: the ordering is "after commit succeeds," not "after the writes
// are issued."
func TestAcceptHandler_CommitFailureNeverPublishes(t *testing.T) {
	h, caller := acceptTestFixture()
	tx := &fakeTx{handoff: h, failCommit: true}
	pub := &fakePublisher{}
	s := &Store{pool: fakeOuterQuerier{handoff: h}}
	handler := s.AcceptHandler(fakeBeginner{tx: tx}, pub)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/handoffs/"+h.ID.String()+"/accept", nil)
	req = req.WithContext(agent.NewContext(req.Context(), caller))
	req = withHandoffIDParam(req, h.ID.String())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

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

// TestAcceptHandler_PublishesAfterCommit confirms the positive case: once
// Commit actually succeeds, the handler publishes exactly the same
// envelope it recorded, to the handoff's room, exactly once.
func TestAcceptHandler_PublishesAfterCommit(t *testing.T) {
	h, caller := acceptTestFixture()
	tx := &fakeTx{handoff: h}
	pub := &fakePublisher{}
	s := &Store{pool: fakeOuterQuerier{handoff: h}}
	handler := s.AcceptHandler(fakeBeginner{tx: tx}, pub)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/handoffs/"+h.ID.String()+"/accept", nil)
	req = req.WithContext(agent.NewContext(req.Context(), caller))
	req = withHandoffIDParam(req, h.ID.String())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !tx.committed {
		t.Fatal("expected the transaction to commit on the happy path")
	}
	if len(pub.calls) != 1 {
		t.Fatalf("expected exactly 1 Publish call, got %d: %+v", len(pub.calls), pub.calls)
	}
	got := pub.calls[0]
	if got.roomID != h.RoomID {
		t.Fatalf("Publish roomID = %s, want %s", got.roomID, h.RoomID)
	}
	if got.msg.Kind != realtime.KindEvent || got.msg.Event == nil {
		t.Fatalf("Publish msg = %+v, want a KindEvent envelope", got.msg)
	}
	if got.msg.Event.Type != protocol.OpHandoffAccept {
		t.Fatalf("Publish envelope Type = %q, want %q", got.msg.Event.Type, protocol.OpHandoffAccept)
	}
}
