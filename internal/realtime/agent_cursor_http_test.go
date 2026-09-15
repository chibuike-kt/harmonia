package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/user"
)

func TestAgentCursorHandler_Unauthenticated(t *testing.T) {
	h := AgentCursorHandler(nil, NewHub())
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/rooms/"+uuid.New().String()+"/agent_cursor", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assertJSONError(t, rec, http.StatusUnauthorized)
}

// TestIntegration_AgentCursorHandler_PublishesOntoTheRealHub is the real
// proof for the live-cursor signal: a real owner's POST actually lands
// on the room's real Hub, exactly like every other live update a
// subscribed browser tab's SSE stream would receive it as — and a
// non-owner's POST is rejected and publishes nothing.
func TestIntegration_AgentCursorHandler_PublishesOntoTheRealHub(t *testing.T) {
	dbURL := os.Getenv("HARMONIA_DATABASE_URL")
	if dbURL == "" {
		t.Skip("HARMONIA_DATABASE_URL not set; skipping integration test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)

	owner, err := users.UpsertByGitHubID(ctx, "agent-cursor-owner-"+uuid.New().String(), "agent-cursor-owner", nil, nil, nil)
	if err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	other, err := users.UpsertByGitHubID(ctx, "agent-cursor-other-"+uuid.New().String(), "agent-cursor-other", nil, nil, nil)
	if err != nil {
		t.Fatalf("seed other: %v", err)
	}
	rm, err := rooms.Create(ctx, &owner.ID, "agent-cursor-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	hub := NewHub()
	h := AgentCursorHandler(rooms, hub)
	sub, unsubscribe := hub.Subscribe(rm.ID)
	defer unsubscribe()

	agentID := uuid.New()
	body, _ := json.Marshal(agentCursorRequest{
		AgentID: agentID, Path: "src/main.go", Line: 12, Column: 4,
		Active: true, Name: "ChatGPT", Provider: "openai",
	})

	// Non-owner: rejected, publishes nothing.
	forbiddenReq := httptest.NewRequestWithContext(user.NewContext(ctx, other), http.MethodPost, "/v1/rooms/"+rm.ID.String()+"/agent_cursor", bytes.NewReader(body))
	forbiddenRec := httptest.NewRecorder()
	withRoomIDParam(rm.ID, h).ServeHTTP(forbiddenRec, forbiddenReq)
	if forbiddenRec.Code != http.StatusForbidden {
		t.Fatalf("non-owner status = %d, want %d", forbiddenRec.Code, http.StatusForbidden)
	}

	// The real owner's POST publishes for real.
	okReq := httptest.NewRequestWithContext(user.NewContext(ctx, owner), http.MethodPost, "/v1/rooms/"+rm.ID.String()+"/agent_cursor", bytes.NewReader(body))
	okRec := httptest.NewRecorder()
	withRoomIDParam(rm.ID, h).ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusNoContent {
		t.Fatalf("owner status = %d, want %d, body = %s", okRec.Code, http.StatusNoContent, okRec.Body.String())
	}

	select {
	case msg := <-sub:
		if msg.Kind != KindAgentCursor {
			t.Fatalf("kind = %q, want %q", msg.Kind, KindAgentCursor)
		}
		if msg.AgentCursor == nil || msg.AgentCursor.AgentID != agentID || msg.AgentCursor.Path != "src/main.go" || !msg.AgentCursor.Active {
			t.Fatalf("cursor = %+v, doesn't match what was posted", msg.AgentCursor)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the owner's real POST never reached the room's real Hub")
	}
}
