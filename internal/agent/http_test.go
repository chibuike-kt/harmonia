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
)

func TestRegisterHandler_InvalidRoomID(t *testing.T) {
	s := &Store{}
	rec := doRegister(t, s, "not-a-uuid", `{"name":"a","provider":"anthropic"}`)
	assertJSONError(t, rec, http.StatusBadRequest)
}

func TestRegisterHandler_InvalidBody(t *testing.T) {
	s := &Store{}
	rec := doRegister(t, s, uuid.New().String(), "not json")
	assertJSONError(t, rec, http.StatusBadRequest)
}

func TestRegisterHandler_MissingName(t *testing.T) {
	s := &Store{}
	rec := doRegister(t, s, uuid.New().String(), `{"provider":"anthropic"}`)
	assertJSONError(t, rec, http.StatusBadRequest)
}

func TestRegisterHandler_InvalidProvider(t *testing.T) {
	s := &Store{}
	rec := doRegister(t, s, uuid.New().String(), `{"name":"a","provider":"not-a-provider"}`)
	assertJSONError(t, rec, http.StatusBadRequest)
}

func doRegister(t *testing.T, s *Store, roomIDParam, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := s.RegisterHandler()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(RoomIDParam, roomIDParam)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/rooms/"+roomIDParam+"/agents", strings.NewReader(body))
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
