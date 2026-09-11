package message

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/realtime"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/store"
	"github.com/chibuike-kt/harmonia/internal/user"
)

type createRequest struct {
	Content           string      `json:"content"`
	MentionedAgentIDs []uuid.UUID `json:"mentioned_agent_ids,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// writeError writes a JSON error body, {"error": "..."} — the shape
// every handler in this API uses.
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

// CreateHandler returns the handler for POST /v1/rooms/{room_id}/messages.
// Mount it behind user.Authenticate — a human posts here, never an
// agent. Ownership is checked the same 404-then-403 way as every other
// room-scoped route (see room.UpdateHandler); each mentioned_agent_id
// gets the same non-leaking treatment: an agent that doesn't exist, or
// exists in a different room, both 404 identically, so a caller learns
// nothing about an agent it can't reach.
//
// The message write is transactional and published only after commit —
// the same pattern every other write in this API follows — even though
// today it's a single insert with no accompanying event record: chat
// messages are their own first-class grammar (ADR-004), not folded into
// the events audit trail. If any mentions are present, their replies are
// each triggered asynchronously after the human message has committed
// and published: this handler returns as soon as the human message is
// durable, never blocking on a live provider call (ADR-004), regardless
// of how many agents it addresses (ADR-006 batch A).
func (s *Store) CreateHandler(rooms *room.Store, agents *agent.Store, pool store.Beginner, hub realtime.Publisher, orch *Orchestrator, titleGen *TitleGenerator, objectiveGen *ObjectiveGenerator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := user.FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		roomID, err := uuid.Parse(chi.URLParam(r, "room_id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid room_id")
			return
		}

		var req createRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if strings.TrimSpace(req.Content) == "" {
			writeError(w, http.StatusBadRequest, "content is required")
			return
		}

		ctx := r.Context()
		rm, err := rooms.GetByID(ctx, roomID)
		if errors.Is(err, room.ErrNotFound) {
			writeError(w, http.StatusNotFound, "room not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to look up room")
			return
		}
		if rm.OwnerID == nil || *rm.OwnerID != u.ID {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}

		// Dedupe first: a repeated mention in the request isn't an error,
		// it just collapses to one — message_mentions' primary key
		// (message_id, agent_id) would otherwise reject the insert outright.
		seen := make(map[uuid.UUID]bool, len(req.MentionedAgentIDs))
		var mentionedIDs []uuid.UUID
		for _, id := range req.MentionedAgentIDs {
			if !seen[id] {
				seen[id] = true
				mentionedIDs = append(mentionedIDs, id)
			}
		}

		// Fail the whole request if any mentioned agent is invalid, rather
		// than silently dropping the bad one and proceeding with the rest:
		// keeps the write atomic (no message ever exists with a mention
		// the caller can't see was rejected) and matches the single-mention
		// behavior this replaces exactly, just extended to a set.
		mentioned := make([]agent.Agent, 0, len(mentionedIDs))
		for _, id := range mentionedIDs {
			a, err := agents.GetByID(ctx, id)
			if errors.Is(err, agent.ErrNotFound) {
				writeError(w, http.StatusNotFound, "mentioned agent not found")
				return
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to look up mentioned agent")
				return
			}
			if a.RoomID != roomID {
				// An agent that exists, just not in this room, doesn't
				// exist as far as this request is concerned — same
				// "don't leak cross-room existence" reasoning as
				// task.ClaimHandler's own room check.
				writeError(w, http.StatusNotFound, "mentioned agent not found")
				return
			}
			mentioned = append(mentioned, a)
		}

		tx, rollback, err := store.BeginTx(ctx, pool)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to start transaction")
			return
		}
		defer rollback()

		txMessages := NewStore(tx)
		m, err := txMessages.CreateHuman(ctx, roomID, u.ID, req.Content, mentionedIDs)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create message")
			return
		}

		if err := tx.Commit(ctx); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to commit transaction")
			return
		}
		hub.Publish(roomID, realtime.NewChatMessage(toChatMessage(m)))

		// One independent async invocation per mentioned agent (ADR-006
		// batch A) — each follows the existing orchestration path on its
		// own and produces its own reply, all pointing reply_to_message_id
		// back at this one human message.
		for _, a := range mentioned {
			orch.TriggerReply(a.ID, rm.OwnerID, m, 0)
		}

		// Implicit single-agent addressing (ADR-004's 2026-09-07
		// addendum): an unaddressed message in a room with exactly one
		// agent is meant for it — no @mention required, since there's no
		// one else it could possibly mean. The moment a second agent
		// exists, that assumption stops holding and this room reverts to
		// requiring an explicit mention (the pre-existing, unaffected
		// behavior above and the empty-mentions-does-nothing behavior
		// below).
		//
		// The addendum's own "Revisit When" flags the race explicitly: a
		// message sent the instant a second agent is registered. This
		// reads the room's agent count fresh, right here, after the
		// human message's own commit — not the same transaction as agent
		// registration, so whichever write actually lands first in the
		// database is what decides it, the ordinary meaning of "read
		// committed state." A message that loses that race gets no
		// auto-reply — not a dropped reply, just no *implicit* one, the
		// same outcome an unaddressed message in any already-ambiguous
		// room has always had, and the message itself is still there for
		// a human to @mention explicitly.
		if len(mentioned) == 0 {
			roomAgents, err := agents.ListByRoom(ctx, roomID)
			if err != nil {
				log.Printf("ERROR message: load room %s agents for implicit addressing: %v", roomID, err)
			} else if len(roomAgents) == 1 {
				orch.TriggerReply(roomAgents[0].ID, rm.OwnerID, m, 0)
			} else if len(roomAgents) > 1 && rm.AutonomousPickupEnabled {
				// ADR-007 batch B: real ambiguity (2+ agents, still
				// unaddressed) is exactly the case implicit single-agent
				// addressing doesn't cover — autonomous pickup is the
				// room's own opt-in answer to it, off by default, never
				// substituting for an explicit mention on its own.
				orch.EvaluateForPickup(roomAgents, rm.OwnerID, m)
			}
		}

		// Auto-title trigger (ADR-004's nameless-room-creation addendum):
		// fires once per room, on whichever message lands first — not on
		// every message. A count query after commit, not a value derived
		// from the transaction itself, since "how many messages does this
		// room have now" is exactly what answers "was this the first,"
		// and a fresh count is simpler and just as correct as threading
		// that fact out of CreateHuman's own insert.
		// Auto-objective trigger reuses this same post-commit count, one
		// fixed threshold higher (see ObjectiveGenerator's own doc
		// comment for why 1 message isn't enough) — fires exactly once,
		// on whichever message first brings the room to that count, the
		// same "count, don't thread a flag through the transaction"
		// reasoning as the title trigger above.
		if count, err := s.CountByRoom(ctx, roomID); err != nil {
			log.Printf("ERROR message: count messages for room %s to check auto-title/objective triggers: %v", roomID, err)
		} else if count == 1 {
			titleGen.GenerateTitle(roomID, rm.OwnerID, m.Content)
		} else if count == objectiveGenerationThreshold {
			objectiveGen.GenerateObjective(roomID, rm.OwnerID)
		}

		writeJSON(w, http.StatusCreated, m)
	}
}

// ListByRoomHandler returns the handler for GET
// /v1/rooms/{room_id}/messages — the artifacts page's cross-room source
// (there is no per-room artifacts storage; every artifact is re-derived
// from message content, and this is what lets that happen for a room
// that isn't the one currently open in the SSE stream). Mount behind
// user.Authenticate; ownership checked the same 404-then-403 way as
// decision.ListByRoomHandler.
func (s *Store) ListByRoomHandler(rooms *room.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := user.FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		roomID, err := uuid.Parse(chi.URLParam(r, "room_id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid room_id")
			return
		}

		ctx := r.Context()
		rm, err := rooms.GetByID(ctx, roomID)
		if errors.Is(err, room.ErrNotFound) {
			writeError(w, http.StatusNotFound, "room not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to look up room")
			return
		}
		if rm.OwnerID == nil || *rm.OwnerID != u.ID {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}

		msgs, err := s.ListByRoom(ctx, roomID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list messages")
			return
		}
		if msgs == nil {
			msgs = []Message{}
		}

		writeJSON(w, http.StatusOK, msgs)
	}
}

// RetryHandler returns the handler for POST
// /v1/rooms/{room_id}/messages/{message_id}/retry — a human asking the
// agent behind message_id to generate a fresh reply to the same
// triggering message, in place of one that failed or wasn't good enough.
// Mount behind user.Authenticate; ownership is checked the same
// 404-then-403 way as decision.PinHandler, message_id's own room
// membership the same non-leaking 404 as message.CreateHandler's
// mentioned-agent check.
//
// This reuses TriggerReply exactly as CreateHandler does — a fresh
// invocation, asynchronous, off this request goroutine (ADR-004) — rather
// than any new generation path. It doesn't delete or replace
// message_id's own row: the retried reply lands as a new agent message,
// the same as any other reply, leaving the earlier attempt visible in
// history rather than rewriting it.
func (s *Store) RetryHandler(rooms *room.Store, orch *Orchestrator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := user.FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		roomID, err := uuid.Parse(chi.URLParam(r, "room_id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid room_id")
			return
		}
		messageID, err := uuid.Parse(chi.URLParam(r, "message_id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid message_id")
			return
		}

		ctx := r.Context()
		rm, err := rooms.GetByID(ctx, roomID)
		if errors.Is(err, room.ErrNotFound) {
			writeError(w, http.StatusNotFound, "room not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to look up room")
			return
		}
		if rm.OwnerID == nil || *rm.OwnerID != u.ID {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}

		m, err := s.GetByID(ctx, messageID)
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "message not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to look up message")
			return
		}
		if m.RoomID != roomID {
			writeError(w, http.StatusNotFound, "message not found")
			return
		}
		if m.SenderKind != SenderAgent || m.AgentID == nil {
			writeError(w, http.StatusBadRequest, "only an agent's reply can be retried")
			return
		}
		if m.ReplyToMessageID == nil {
			writeError(w, http.StatusBadRequest, "this message has nothing to retry")
			return
		}

		triggering, err := s.GetByID(ctx, *m.ReplyToMessageID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to look up the message this reply was to")
			return
		}

		orch.TriggerReply(*m.AgentID, rm.OwnerID, triggering, 0)

		w.WriteHeader(http.StatusNoContent)
	}
}
