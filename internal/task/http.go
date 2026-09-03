package task

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/event"
	"github.com/chibuike-kt/harmonia/internal/protocol"
	"github.com/chibuike-kt/harmonia/internal/store"
)

type createRequest struct {
	Objective    string     `json:"objective"`
	ParentTaskID *uuid.UUID `json:"parent_task_id,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// writeError writes a JSON error body, {"error": "..."} — the shape every
// handler in this API uses, not just this one.
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: message})
}

// foreignKeyViolation is the Postgres SQLSTATE for a foreign-key
// constraint failure — used here to tell "parent_task_id doesn't exist"
// apart from an unexpected server error.
const foreignKeyViolation = "23503"

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// beginTx starts a transaction and returns a rollback func safe to defer
// unconditionally — calling it after a successful Commit is a documented
// no-op (pgx.ErrTxClosed, discarded here), so `defer rollback()` before a
// possible `tx.Commit` is the standard pgx pattern, not a bug.
func beginTx(ctx context.Context, pool store.Beginner) (store.Tx, func(), error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	return tx, func() { _ = tx.Rollback(ctx) }, nil
}

// CreateHandler returns the handler for POST /v1/tasks. The task's room is
// the authenticated agent's own room — there is no separate room_id to
// mismatch, since an agent only ever acts in the room it registered in.
// The task insert and its TASK_CREATED event are one transaction: either
// both land or neither does.
func (s *Store) CreateHandler(pool store.Beginner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, ok := agent.FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		var req createRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Objective == "" {
			writeError(w, http.StatusBadRequest, "objective is required")
			return
		}

		ctx := r.Context()
		tx, rollback, err := beginTx(ctx, pool)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to start transaction")
			return
		}
		defer rollback()

		txTasks := NewStore(tx)
		t, err := txTasks.Create(ctx, a.RoomID, req.Objective, req.ParentTaskID)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
				writeError(w, http.StatusBadRequest, "parent_task_id not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to create task")
			return
		}

		payload := map[string]any{"objective": t.Objective}
		if t.ParentTaskID != nil {
			payload["parent_task_id"] = t.ParentTaskID.String()
		}
		env := protocol.NewEnvelope(t.RoomID, protocol.OpTaskCreate, protocol.Participant{AgentID: a.ID}, payload)

		txEvents := event.NewStore(tx)
		if err := txEvents.Record(ctx, t.RoomID, &t.ID, &a.ID, EventTaskCreated, env.Payload); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to record event")
			return
		}

		if err := tx.Commit(ctx); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to commit transaction")
			return
		}

		writeJSON(w, http.StatusCreated, t)
	}
}

// ClaimHandler returns the handler for POST /v1/tasks/{id}/claim. The claim
// and its TASK_CLAIMED event are one transaction: either both land or
// neither does.
func (s *Store) ClaimHandler(pool store.Beginner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, ok := agent.FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		taskID, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid task id")
			return
		}

		ctx := r.Context()
		t, err := s.GetByID(ctx, taskID)
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "task not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load task")
			return
		}
		// A task in another room doesn't exist as far as this agent is
		// concerned — same "don't leak cross-room existence" reasoning as
		// the room-not-found response on agent registration.
		if t.RoomID != a.RoomID {
			writeError(w, http.StatusNotFound, "task not found")
			return
		}

		tx, rollback, err := beginTx(ctx, pool)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to start transaction")
			return
		}
		defer rollback()

		txTasks := NewStore(tx)
		if err := txTasks.Claim(ctx, taskID, a.ID); err != nil {
			if errors.Is(err, ErrAlreadyClaimed) {
				writeError(w, http.StatusConflict, "task already claimed")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to claim task")
			return
		}

		claimed, err := txTasks.GetByID(ctx, taskID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load claimed task")
			return
		}

		env := protocol.NewEnvelope(claimed.RoomID, protocol.OpTaskClaim, protocol.Participant{AgentID: a.ID}, map[string]any{})

		txEvents := event.NewStore(tx)
		if err := txEvents.Record(ctx, claimed.RoomID, &claimed.ID, &a.ID, EventTaskClaimed, env.Payload); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to record event")
			return
		}

		if err := tx.Commit(ctx); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to commit transaction")
			return
		}

		writeJSON(w, http.StatusOK, claimed)
	}
}

// CompleteHandler returns the handler for POST /v1/tasks/{id}/complete. The
// completion and its TASK_COMPLETED event are one transaction: either both
// land or neither does.
func (s *Store) CompleteHandler(pool store.Beginner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, ok := agent.FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		taskID, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid task id")
			return
		}

		ctx := r.Context()
		t, err := s.GetByID(ctx, taskID)
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "task not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load task")
			return
		}
		if t.RoomID != a.RoomID {
			writeError(w, http.StatusNotFound, "task not found")
			return
		}
		// Only the agent that claimed a task may complete it — owner_agent_id
		// exists to answer exactly this, not just to record history.
		if t.OwnerAgentID == nil || *t.OwnerAgentID != a.ID {
			writeError(w, http.StatusForbidden, "task not owned by this agent")
			return
		}

		tx, rollback, err := beginTx(ctx, pool)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to start transaction")
			return
		}
		defer rollback()

		txTasks := NewStore(tx)
		if err := txTasks.Complete(ctx, taskID); err != nil {
			if errors.Is(err, ErrNotClaimed) {
				writeError(w, http.StatusConflict, "task already completed or not claimed")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to complete task")
			return
		}

		completed, err := txTasks.GetByID(ctx, taskID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load completed task")
			return
		}

		env := protocol.NewEnvelope(completed.RoomID, protocol.OpTaskComplete, protocol.Participant{AgentID: a.ID}, map[string]any{})

		txEvents := event.NewStore(tx)
		if err := txEvents.Record(ctx, completed.RoomID, &completed.ID, &a.ID, EventTaskCompleted, env.Payload); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to record event")
			return
		}

		if err := tx.Commit(ctx); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to commit transaction")
			return
		}

		writeJSON(w, http.StatusOK, completed)
	}
}
