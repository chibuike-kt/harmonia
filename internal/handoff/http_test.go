package handoff

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/task"
)

func TestRequestHandler_Unauthenticated(t *testing.T) {
	s := &Store{}
	h := s.RequestHandler(&task.Store{}, &agent.Store{}, nil, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/handoffs", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusUnauthorized)
}

func TestRequestHandler_InvalidBody(t *testing.T) {
	s := &Store{}
	h := s.RequestHandler(&task.Store{}, &agent.Store{}, nil, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/handoffs", strings.NewReader("not json"))
	req = req.WithContext(agent.NewContext(req.Context(), agent.Agent{ID: uuid.New(), RoomID: uuid.New()}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusBadRequest)
}

func TestRequestHandler_MissingSummary(t *testing.T) {
	s := &Store{}
	h := s.RequestHandler(&task.Store{}, &agent.Store{}, nil, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/handoffs", strings.NewReader(`{"task_id":"`+uuid.New().String()+`","to_agent_id":"`+uuid.New().String()+`"}`))
	req = req.WithContext(agent.NewContext(req.Context(), agent.Agent{ID: uuid.New(), RoomID: uuid.New()}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusBadRequest)
}

func TestAcceptHandler_Unauthenticated(t *testing.T) {
	s := &Store{}
	h := s.AcceptHandler(nil, nil)

	rec := doAcceptRequest(t, h, uuid.New().String(), false)
	assertJSONError(t, rec, http.StatusUnauthorized)
}

func TestAcceptHandler_InvalidHandoffID(t *testing.T) {
	s := &Store{}
	h := s.AcceptHandler(nil, nil)

	rec := doAcceptRequest(t, h, "not-a-uuid", true)
	assertJSONError(t, rec, http.StatusBadRequest)
}

func doAcceptRequest(t *testing.T, h http.HandlerFunc, idParam string, authed bool) *httptest.ResponseRecorder {
	t.Helper()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", idParam)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/handoffs/"+idParam+"/accept", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	if authed {
		req = req.WithContext(agent.NewContext(req.Context(), agent.Agent{ID: uuid.New(), RoomID: uuid.New()}))
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
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
