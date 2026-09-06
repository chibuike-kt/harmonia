package decision

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/user"
)

func withRoomAndMessageIDParams(req *http.Request, roomID, messageID string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("room_id", roomID)
	rctx.URLParams.Add("message_id", messageID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

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

// These all fail before touching rooms/messages/pool, so a bare
// &Store{} and nil dependencies are safe — same pattern as
// message.TestCreateHandler_Unauthenticated.

func TestPinHandler_Unauthenticated(t *testing.T) {
	s := &Store{}
	h := s.PinHandler(nil, nil)

	roomID, messageID := uuid.New().String(), uuid.New().String()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/rooms/"+roomID+"/messages/"+messageID+"/decisions", nil)
	req = withRoomAndMessageIDParams(req, roomID, messageID)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusUnauthorized)
}

func TestPinHandler_InvalidRoomID(t *testing.T) {
	s := &Store{}
	h := s.PinHandler(nil, nil)

	req := httptest.NewRequestWithContext(authedContext(), http.MethodPost, "/v1/rooms/not-a-uuid/messages/"+uuid.New().String()+"/decisions", nil)
	req = withRoomAndMessageIDParams(req, "not-a-uuid", uuid.New().String())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusBadRequest)
}

func TestPinHandler_InvalidMessageID(t *testing.T) {
	s := &Store{}
	h := s.PinHandler(nil, nil)

	roomID := uuid.New().String()
	req := httptest.NewRequestWithContext(authedContext(), http.MethodPost, "/v1/rooms/"+roomID+"/messages/not-a-uuid/decisions", nil)
	req = withRoomAndMessageIDParams(req, roomID, "not-a-uuid")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusBadRequest)
}

func TestListByRoomHandler_Unauthenticated(t *testing.T) {
	s := &Store{}
	h := s.ListByRoomHandler(nil)

	roomID := uuid.New().String()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/rooms/"+roomID+"/decisions", nil)
	req = withRoomIDParam(req, roomID)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusUnauthorized)
}

func TestListByRoomHandler_InvalidRoomID(t *testing.T) {
	s := &Store{}
	h := s.ListByRoomHandler(nil)

	req := httptest.NewRequestWithContext(authedContext(), http.MethodGet, "/v1/rooms/not-a-uuid/decisions", nil)
	req = withRoomIDParam(req, "not-a-uuid")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusBadRequest)
}
