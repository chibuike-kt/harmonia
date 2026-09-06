package decision

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chibuike-kt/harmonia/internal/message"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/user"
)

func connectDecisionTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("HARMONIA_DATABASE_URL")
	if dbURL == "" {
		t.Skip("HARMONIA_DATABASE_URL not set; skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedDecisionTestUser(t *testing.T, ctx context.Context, users *user.Store, githubIDPrefix string) user.User {
	t.Helper()
	u, err := users.UpsertByGitHubID(ctx, githubIDPrefix+uuid.New().String(), githubIDPrefix+"user", nil, nil, nil)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u
}

// TestIntegration_PinHandler exercises POST
// /v1/rooms/{room_id}/messages/{message_id}/decisions end to end against
// real Postgres: the room's owner can pin a real message in it (the
// decision's content is the message's own content, not something the
// caller supplies), a different user cannot (403), a message from
// another room 404s the same non-leaking way an unknown message would,
// and pinning the same message twice is idempotent — one row, not two,
// per migrations/0008_decisions.up.sql's own UNIQUE(message_id).
// Requires a live Postgres — run via `make test-integration` after
// `make up`.
func TestIntegration_PinHandler(t *testing.T) {
	pool := connectDecisionTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	messages := message.NewStore(pool)
	decisions := NewStore(pool)

	owner := seedDecisionTestUser(t, ctx, users, "decision-pin-owner-")
	other := seedDecisionTestUser(t, ctx, users, "decision-pin-other-")

	rm, err := rooms.Create(ctx, &owner.ID, "decision-pin-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	otherRoom, err := rooms.Create(ctx, &owner.ID, "other-room")
	if err != nil {
		t.Fatalf("create other room: %v", err)
	}

	msg, err := messages.CreateHuman(ctx, rm.ID, owner.ID, "we should ship the fix behind a flag", nil)
	if err != nil {
		t.Fatalf("seed message: %v", err)
	}
	elsewhereMsg, err := messages.CreateHuman(ctx, otherRoom.ID, owner.ID, "unrelated message", nil)
	if err != nil {
		t.Fatalf("seed message in other room: %v", err)
	}

	h := decisions.PinHandler(rooms, messages)

	doPin := func(caller user.User, roomID, messageID string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(user.NewContext(ctx, caller), http.MethodPost, "/v1/rooms/"+roomID+"/messages/"+messageID+"/decisions", nil)
		req = withRoomAndMessageIDParams(req, roomID, messageID)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := doPin(other, rm.ID.String(), msg.ID.String()); rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if rec := doPin(owner, rm.ID.String(), uuid.New().String()); rec.Code != http.StatusNotFound {
		t.Fatalf("nonexistent message status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if rec := doPin(owner, rm.ID.String(), elsewhereMsg.ID.String()); rec.Code != http.StatusNotFound {
		t.Fatalf("message from another room status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}

	rec := doPin(owner, rm.ID.String(), msg.ID.String())
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var got Decision
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.MessageID != msg.ID {
		t.Fatalf("MessageID = %s, want %s", got.MessageID, msg.ID)
	}
	if got.Content != msg.Content {
		t.Fatalf("Content = %q, want the message's own content %q", got.Content, msg.Content)
	}

	// Pinning the same message again is idempotent — same decision id,
	// not a second row.
	rec2 := doPin(owner, rm.ID.String(), msg.ID.String())
	if rec2.Code != http.StatusCreated {
		t.Fatalf("second pin status = %d, want %d, body = %s", rec2.Code, http.StatusCreated, rec2.Body.String())
	}
	var got2 Decision
	if err := json.Unmarshal(rec2.Body.Bytes(), &got2); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if got2.ID != got.ID {
		t.Fatalf("second pin ID = %s, want the same decision %s (idempotent, not a duplicate)", got2.ID, got.ID)
	}

	all, err := decisions.ListByRoom(ctx, rm.ID)
	if err != nil {
		t.Fatalf("ListByRoom: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("decisions for room = %+v, want exactly 1 despite pinning twice", all)
	}
}

// TestIntegration_ListByRoomHandler exercises GET
// /v1/rooms/{room_id}/decisions: the room's owner sees pinned decisions
// in the room info panel's own shape, a different user gets 403, and an
// unknown room 404s.
func TestIntegration_ListByRoomHandler(t *testing.T) {
	pool := connectDecisionTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	messages := message.NewStore(pool)
	decisions := NewStore(pool)

	owner := seedDecisionTestUser(t, ctx, users, "decision-list-owner-")
	other := seedDecisionTestUser(t, ctx, users, "decision-list-other-")

	rm, err := rooms.Create(ctx, &owner.ID, "decision-list-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	msg, err := messages.CreateHuman(ctx, rm.ID, owner.ID, "let's ship it Friday", nil)
	if err != nil {
		t.Fatalf("seed message: %v", err)
	}
	if _, err := decisions.Pin(ctx, rm.ID, msg.ID, msg.Content); err != nil {
		t.Fatalf("pin decision: %v", err)
	}

	h := decisions.ListByRoomHandler(rooms)

	doList := func(caller user.User, roomID string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(user.NewContext(ctx, caller), http.MethodGet, "/v1/rooms/"+roomID+"/decisions", nil)
		req = withRoomIDParam(req, roomID)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := doList(owner, rm.ID.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got []Decision
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 1 || got[0].Content != "let's ship it Friday" {
		t.Fatalf("decisions = %+v, want exactly the one pinned decision", got)
	}

	if rec := doList(other, rm.ID.String()); rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if rec := doList(owner, uuid.New().String()); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown room status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}
