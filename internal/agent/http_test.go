package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/user"
)

func authedContext() context.Context {
	return user.NewContext(context.Background(), user.User{ID: uuid.New()})
}

func TestRegisterHandler_Unauthenticated(t *testing.T) {
	s := &Store{}
	rooms := &room.Store{}
	h := s.RegisterHandler(rooms)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(RoomIDParam, uuid.New().String())
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/rooms/x/agents", strings.NewReader(`{"name":"a","provider":"anthropic"}`))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assertJSONError(t, rec, http.StatusUnauthorized)
}

// TestRegisterHandler_InvalidRoomID is the only body-shape-adjacent case
// that stays a pure unit test: an unparseable room_id is rejected before
// RegisterHandler ever needs to look the room up, so a zero-value
// *room.Store (nil pool) is a safe stand-in. Every other validation case
// (bad body, missing name, bad provider) now runs after the room
// ownership check — see TestIntegration_RegisterHandler_ValidationOrder
// — so it needs a real room and owner, not a nil pool.
func TestRegisterHandler_InvalidRoomID(t *testing.T) {
	s := &Store{}
	rec := doRegister(t, s, &room.Store{}, "not-a-uuid", `{"name":"a","provider":"anthropic"}`)
	assertJSONError(t, rec, http.StatusBadRequest)
}

// doRegister drives RegisterHandler with an authenticated context.
func doRegister(t *testing.T, s *Store, rooms *room.Store, roomIDParam, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := s.RegisterHandler(rooms)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(RoomIDParam, roomIDParam)

	req := httptest.NewRequestWithContext(authedContext(), http.MethodPost, "/v1/rooms/"+roomIDParam+"/agents", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func assertJSONError(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d", rec.Code, wantStatus)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", ct, "application/json")
	}
	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error == "" {
		t.Fatal("expected non-empty error message")
	}
}
