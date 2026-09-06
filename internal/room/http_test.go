package room

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

// withRoomIDParam attaches idParam as chi's "id" URL param — the way
// UpdateHandler reads the room id out of the request in production, not
// a shortcut around it.
func withRoomIDParam(req *http.Request, idParam string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", idParam)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func authedContext() context.Context {
	return user.NewContext(context.Background(), user.User{ID: uuid.New()})
}

func TestCreateHandler_Unauthenticated(t *testing.T) {
	s := &Store{}
	h := s.CreateHandler()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/rooms", strings.NewReader(`{"name":"a"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusUnauthorized)
}

func TestCreateHandler_InvalidBody(t *testing.T) {
	s := &Store{}
	h := s.CreateHandler()

	req := httptest.NewRequestWithContext(authedContext(), http.MethodPost, "/v1/rooms", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusBadRequest)
}

func TestListHandler_Unauthenticated(t *testing.T) {
	s := &Store{}
	h := s.ListHandler()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/rooms", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusUnauthorized)
}

func TestUpdateHandler_Unauthenticated(t *testing.T) {
	s := &Store{}
	h := s.UpdateHandler()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/v1/rooms/"+uuid.New().String(), strings.NewReader(`{"name":"a"}`))
	req = withRoomIDParam(req, uuid.New().String())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusUnauthorized)
}

func TestUpdateHandler_InvalidRoomID(t *testing.T) {
	s := &Store{}
	h := s.UpdateHandler()

	req := httptest.NewRequestWithContext(authedContext(), http.MethodPatch, "/v1/rooms/not-a-uuid", strings.NewReader(`{"name":"a"}`))
	req = withRoomIDParam(req, "not-a-uuid")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusBadRequest)
}

func TestUpdateHandler_EmptyName(t *testing.T) {
	s := &Store{}
	h := s.UpdateHandler()

	roomID := uuid.New().String()
	req := httptest.NewRequestWithContext(authedContext(), http.MethodPatch, "/v1/rooms/"+roomID, strings.NewReader(`{"name":""}`))
	req = withRoomIDParam(req, roomID)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusBadRequest)
}

func TestDeleteHandler_Unauthenticated(t *testing.T) {
	s := &Store{}
	h := s.DeleteHandler()

	roomID := uuid.New().String()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/v1/rooms/"+roomID, nil)
	req = withRoomIDParam(req, roomID)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusUnauthorized)
}

func TestDeleteHandler_InvalidRoomID(t *testing.T) {
	s := &Store{}
	h := s.DeleteHandler()

	req := httptest.NewRequestWithContext(authedContext(), http.MethodDelete, "/v1/rooms/not-a-uuid", nil)
	req = withRoomIDParam(req, "not-a-uuid")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusBadRequest)
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
