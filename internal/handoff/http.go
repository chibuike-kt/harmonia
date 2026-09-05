package handoff

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/event"
	"github.com/chibuike-kt/harmonia/internal/protocol"
	"github.com/chibuike-kt/harmonia/internal/realtime"
	"github.com/chibuike-kt/harmonia/internal/store"
	"github.com/chibuike-kt/harmonia/internal/task"
)

// Event types recorded to the audit trail for each handoff transition —
// distinct from protocol.Operation, the wire message type.
const (
	EventHandoffRequested = "HANDOFF_REQUESTED"
	EventHandoffAccepted  = "HANDOFF_ACCEPTED"
)

type requestBody struct {
	TaskID    uuid.UUID `json:"task_id"`
	ToAgentID uuid.UUID `json:"to_agent_id"`
	Summary   string    `json:"summary"`
	Completed []string  `json:"completed"`
	Remaining []string  `json:"remaining"`
	Artifacts []string  `json:"artifacts"`
	Decisions []string  `json:"decisions"`
	Risks     []string  `json:"risks"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// RequestHandler returns the handler for POST /v1/handoffs. The handoff's
// room is the authenticated (from) agent's own room; task_id and
// to_agent_id are each checked against that same room before the insert —
// nothing here lets one room's handoff reference another room's task or
// agent, even though the schema's FKs alone wouldn't catch that. The
// handoff insert and its HANDOFF_REQUESTED event are one transaction. The
// same event is published to hub strictly after commit — a rolled-back
// write must never reach a subscriber.
func (s *Store) RequestHandler(tasks *task.Store, agents *agent.Store, pool store.Beginner, hub realtime.Publisher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, ok := agent.FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		var req requestBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Summary == "" {
			writeError(w, http.StatusBadRequest, "summary is required")
			return
		}

		ctx := r.Context()

		tk, err := tasks.GetByID(ctx, req.TaskID)
		if errors.Is(err, task.ErrNotFound) {
			writeError(w, http.StatusNotFound, "task not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load task")
			return
		}
		if tk.RoomID != a.RoomID {
			writeError(w, http.StatusNotFound, "task not found")
			return
		}

		toAgent, err := agents.GetByID(ctx, req.ToAgentID)
		if errors.Is(err, agent.ErrNotFound) {
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load agent")
			return
		}
		if toAgent.RoomID != a.RoomID {
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}

		tx, rollback, err := store.BeginTx(ctx, pool)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to start transaction")
			return
		}
		defer rollback()

		txHandoffs := NewStore(tx)
		h, err := txHandoffs.Request(ctx, Handoff{
			RoomID:      a.RoomID,
			TaskID:      req.TaskID,
			FromAgentID: a.ID,
			ToAgentID:   req.ToAgentID,
			Summary:     req.Summary,
			Completed:   req.Completed,
			Remaining:   req.Remaining,
			Artifacts:   req.Artifacts,
			Decisions:   req.Decisions,
			Risks:       req.Risks,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to request handoff")
			return
		}

		payload := map[string]any{
			"summary":     h.Summary,
			"completed":   h.Completed,
			"remaining":   h.Remaining,
			"artifacts":   h.Artifacts,
			"decisions":   h.Decisions,
			"risks":       h.Risks,
			"to_agent_id": h.ToAgentID.String(),
		}
		env := protocol.NewEnvelope(a.RoomID, protocol.OpHandoffRequest, protocol.Participant{AgentID: a.ID}, payload)

		txEvents := event.NewStore(tx)
		if err := txEvents.Record(ctx, a.RoomID, &h.TaskID, &a.ID, EventHandoffRequested, env.Payload); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to record event")
			return
		}

		if err := tx.Commit(ctx); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to commit transaction")
			return
		}
		hub.Publish(a.RoomID, realtime.NewEventMessage(env))

		writeJSON(w, http.StatusCreated, h)
	}
}

// AcceptHandler returns the handler for POST /v1/handoffs/{id}/accept.
// Only the agent the handoff is addressed to may accept it. The status
// update and its HANDOFF_ACCEPTED event are one transaction. The same
// event is published to hub strictly after commit.
func (s *Store) AcceptHandler(pool store.Beginner, hub realtime.Publisher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, ok := agent.FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		handoffID, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid handoff id")
			return
		}

		ctx := r.Context()
		h, err := s.GetByID(ctx, handoffID)
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "handoff not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load handoff")
			return
		}
		if h.RoomID != a.RoomID {
			writeError(w, http.StatusNotFound, "handoff not found")
			return
		}
		if h.ToAgentID != a.ID {
			writeError(w, http.StatusForbidden, "handoff not addressed to this agent")
			return
		}

		tx, rollback, err := store.BeginTx(ctx, pool)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to start transaction")
			return
		}
		defer rollback()

		txHandoffs := NewStore(tx)
		if err := txHandoffs.Accept(ctx, handoffID); err != nil {
			if errors.Is(err, ErrNotRequested) {
				writeError(w, http.StatusConflict, "handoff already accepted")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to accept handoff")
			return
		}

		accepted, err := txHandoffs.GetByID(ctx, handoffID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load accepted handoff")
			return
		}

		env := protocol.NewEnvelope(accepted.RoomID, protocol.OpHandoffAccept, protocol.Participant{AgentID: a.ID}, map[string]any{})

		txEvents := event.NewStore(tx)
		if err := txEvents.Record(ctx, accepted.RoomID, &accepted.TaskID, &a.ID, EventHandoffAccepted, env.Payload); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to record event")
			return
		}

		if err := tx.Commit(ctx); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to commit transaction")
			return
		}
		hub.Publish(accepted.RoomID, realtime.NewEventMessage(env))

		writeJSON(w, http.StatusOK, accepted)
	}
}
