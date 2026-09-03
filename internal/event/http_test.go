package event

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestListByRoomHandler_InvalidRoomID(t *testing.T) {
	s := &Store{}
	h := s.ListByRoomHandler()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "not-a-uuid")
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/rooms/not-a-uuid/events", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", ct, "application/json")
	}
}
