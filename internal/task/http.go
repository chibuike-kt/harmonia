package task

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/event"
	"github.com/chibuike-kt/harmonia/internal/protocol"
	"github.com/chibuike-kt/harmonia/internal/realtime"
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

// publishPresence fans agentID's new status out over hub and mirrors it
// into Redis, strictly after the transaction that changed it has
// committed — same ordering rule as the event publish. Both are
// best-effort, ephemeral side effects of an already-durable write (see
// ADR-003): a Redis hiccup here must never turn an already-successful,
// already-committed claim/complete into an error response, so a mirror
// failure is logged, not returned — the hub publish already reached any
// currently-connected subscriber regardless.
//
// The ERROR prefix is deliberate: this codebase's logging is plain
// stdlib log with no level support at all (see cmd/server/main.go's own
// log.Printf for routine startup output) — without a text marker, a real
// Redis outage here would read identically to routine noise in the log
// stream. This isn't a real leveled-logging fix, just the cheapest way
// to make the line grep-able until this codebase adopts one.
func publishPresence(ctx context.Context, hub realtime.Publisher, rdb *redis.Client, roomID, agentID uuid.UUID, status agent.Status) {
	hub.Publish(roomID, realtime.NewPresenceMessage(agentID, string(status)))
	if err := realtime.SetPresence(ctx, rdb, agentID, string(status)); err != nil {
		log.Printf("ERROR realtime: mirror presence for agent %s: %v", agentID, err)
	}
}

// CreateHandler returns the handler for POST /v1/tasks. The task's room is
// the authenticated agent's own room — there is no separate room_id to
// mismatch, since an agent only ever acts in the room it registered in.
// The task insert and its TASK_CREATED event are one transaction: either
// both land or neither does. The same event is published to hub strictly
// after commit — a rolled-back write must never reach a subscriber.
func (s *Store) CreateHandler(pool store.Beginner, hub realtime.Publisher) http.HandlerFunc {
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
		tx, rollback, err := store.BeginTx(ctx, pool)
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
		hub.Publish(t.RoomID, realtime.NewEventMessage(env))

		writeJSON(w, http.StatusCreated, t)
	}
}

// ClaimHandler returns the handler for POST /v1/tasks/{id}/claim. The
// claim, the claiming agent's transition to running, and the TASK_CLAIMED
// event are one transaction: either all three land or none does. The
// event and the presence transition are each published to hub strictly
// after commit.
func (s *Store) ClaimHandler(pool store.Beginner, hub realtime.Publisher, rdb *redis.Client) http.HandlerFunc {
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

		tx, rollback, err := store.BeginTx(ctx, pool)
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

		txAgents := agent.NewStore(tx)
		if err := txAgents.SetStatus(ctx, a.ID, agent.StatusRunning); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update agent status")
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
		hub.Publish(claimed.RoomID, realtime.NewEventMessage(env))
		publishPresence(ctx, hub, rdb, claimed.RoomID, a.ID, agent.StatusRunning)

		writeJSON(w, http.StatusOK, claimed)
	}
}

// CompleteHandler returns the handler for POST /v1/tasks/{id}/complete.
// The completion, the completing agent's transition back to available,
// and the TASK_COMPLETED event are one transaction: either all three land
// or none does. The event and the presence transition are each published
// to hub strictly after commit.
func (s *Store) CompleteHandler(pool store.Beginner, hub realtime.Publisher, rdb *redis.Client) http.HandlerFunc {
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

		tx, rollback, err := store.BeginTx(ctx, pool)
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

		txAgents := agent.NewStore(tx)
		if err := txAgents.SetStatus(ctx, a.ID, agent.StatusAvailable); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update agent status")
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
		hub.Publish(completed.RoomID, realtime.NewEventMessage(env))
		publishPresence(ctx, hub, rdb, completed.RoomID, a.ID, agent.StatusAvailable)

		writeJSON(w, http.StatusOK, completed)
	}
}
