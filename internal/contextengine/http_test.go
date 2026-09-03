package contextengine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/event"
)

func TestTaskHandler_Unauthenticated(t *testing.T) {
	s := &Store{}
	h := s.TaskHandler(&event.Store{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/context/tasks/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusUnauthorized)
}

func TestTaskHandler_InvalidTaskID(t *testing.T) {
	s := &Store{}
	h := s.TaskHandler(&event.Store{})

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("task_id", "not-a-uuid")
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/context/tasks/not-a-uuid", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(agent.NewContext(req.Context(), agent.Agent{ID: uuid.New()}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusBadRequest)
}

func assertJSONError(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, wantStatus, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", ct, "application/json")
	}
}
