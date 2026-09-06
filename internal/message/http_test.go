package message

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/user"
)

func withRoomIDParam(req *http.Request, roomID string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("room_id", roomID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func authedContext() context.Context {
	return user.NewContext(context.Background(), user.User{ID: uuid.New()})
}

func assertJSONError(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, wantStatus, rec.Body.String())
	}
	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error == "" {
		t.Fatal("expected non-empty error message")
	}
}

// These all fail before touching rooms/agents/pool/hub/orch, so a bare
// &Store{} and nil dependencies are safe — same pattern as
// room.TestCreateHandler_Unauthenticated.

func TestCreateHandler_Unauthenticated(t *testing.T) {
	s := &Store{}
	h := s.CreateHandler(nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/rooms/"+uuid.New().String()+"/messages", strings.NewReader(`{"content":"hi"}`))
	req = withRoomIDParam(req, uuid.New().String())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusUnauthorized)
}

func TestCreateHandler_InvalidRoomID(t *testing.T) {
	s := &Store{}
	h := s.CreateHandler(nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequestWithContext(authedContext(), http.MethodPost, "/v1/rooms/not-a-uuid/messages", strings.NewReader(`{"content":"hi"}`))
	req = withRoomIDParam(req, "not-a-uuid")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusBadRequest)
}

func TestCreateHandler_InvalidBody(t *testing.T) {
	s := &Store{}
	h := s.CreateHandler(nil, nil, nil, nil, nil, nil)

	roomID := uuid.New().String()
	req := httptest.NewRequestWithContext(authedContext(), http.MethodPost, "/v1/rooms/"+roomID+"/messages", strings.NewReader(`not json`))
	req = withRoomIDParam(req, roomID)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusBadRequest)
}

func TestCreateHandler_EmptyContent(t *testing.T) {
	s := &Store{}
	h := s.CreateHandler(nil, nil, nil, nil, nil, nil)

	roomID := uuid.New().String()
	req := httptest.NewRequestWithContext(authedContext(), http.MethodPost, "/v1/rooms/"+roomID+"/messages", strings.NewReader(`{"content":"   "}`))
	req = withRoomIDParam(req, roomID)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusBadRequest)
}
