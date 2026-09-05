package task

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/agent"
)

func TestCreateHandler_Unauthenticated(t *testing.T) {
	s := &Store{}
	h := s.CreateHandler(nil, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/tasks", strings.NewReader(`{"objective":"x"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusUnauthorized)
}

func TestCreateHandler_MissingObjective(t *testing.T) {
	s := &Store{}
	h := s.CreateHandler(nil, nil)

	req := withAgent(t, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/tasks", strings.NewReader(`{}`)), agent.Agent{ID: uuid.New()})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusBadRequest)
}

func TestClaimHandler_Unauthenticated(t *testing.T) {
	s := &Store{}
	h := s.ClaimHandler(nil, nil)

	rec := doTaskRequest(t, h, uuid.New().String(), false, agent.Agent{})
	assertJSONError(t, rec, http.StatusUnauthorized)
}

func TestClaimHandler_InvalidTaskID(t *testing.T) {
	s := &Store{}
	h := s.ClaimHandler(nil, nil)

	rec := doTaskRequest(t, h, "not-a-uuid", true, agent.Agent{ID: uuid.New()})
	assertJSONError(t, rec, http.StatusBadRequest)
}

func TestCompleteHandler_Unauthenticated(t *testing.T) {
	s := &Store{}
	h := s.CompleteHandler(nil, nil)

	rec := doTaskRequest(t, h, uuid.New().String(), false, agent.Agent{})
	assertJSONError(t, rec, http.StatusUnauthorized)
}

func TestCompleteHandler_InvalidTaskID(t *testing.T) {
	s := &Store{}
	h := s.CompleteHandler(nil, nil)

	rec := doTaskRequest(t, h, "not-a-uuid", true, agent.Agent{ID: uuid.New()})
	assertJSONError(t, rec, http.StatusBadRequest)
}

func doTaskRequest(t *testing.T, h http.HandlerFunc, idParam string, authed bool, a agent.Agent) *httptest.ResponseRecorder {
	t.Helper()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", idParam)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/tasks/"+idParam+"/claim", nil)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	if authed {
		req = withAgent(t, req, a)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func withAgent(t *testing.T, req *http.Request, a agent.Agent) *http.Request {
	t.Helper()
	return req.WithContext(agent.NewContext(req.Context(), a))
}

func assertJSONError(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, wantStatus, rec.Body.String())
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
