package credentials

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

func authedContext() context.Context {
	return user.NewContext(context.Background(), user.User{ID: uuid.New()})
}

func TestConnectHandler_Unauthenticated(t *testing.T) {
	s := NewStore(nil, nil)
	rec := doConnect(t, s, context.Background(), `{"provider":"anthropic","key":"sk-test"}`)
	assertJSONError(t, rec, http.StatusUnauthorized)
}

func TestConnectHandler_InvalidBody(t *testing.T) {
	s := NewStore(nil, nil)
	rec := doConnect(t, s, authedContext(), "not json")
	assertJSONError(t, rec, http.StatusBadRequest)
}

func TestConnectHandler_InvalidProvider(t *testing.T) {
	s := NewStore(nil, nil)
	rec := doConnect(t, s, authedContext(), `{"provider":"cohere","key":"sk-test"}`)
	assertJSONError(t, rec, http.StatusBadRequest)
}

func TestConnectHandler_MissingKey(t *testing.T) {
	s := NewStore(nil, nil)
	rec := doConnect(t, s, authedContext(), `{"provider":"anthropic","key":""}`)
	assertJSONError(t, rec, http.StatusBadRequest)
}

func TestConnectHandler_EncryptionNotConfigured(t *testing.T) {
	// cipher is nil (encryption unset) — must 500 before ever attempting
	// a live provider call or touching the (also nil) pool.
	s := NewStore(nil, nil)
	rec := doConnect(t, s, authedContext(), `{"provider":"anthropic","key":"sk-test-1234"}`)
	assertJSONError(t, rec, http.StatusInternalServerError)
}

func TestListHandler_Unauthenticated(t *testing.T) {
	s := NewStore(nil, nil)
	h := s.ListHandler()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/credentials", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusUnauthorized)
}

func TestDeleteHandler_Unauthenticated(t *testing.T) {
	s := NewStore(nil, nil)
	rec := doDelete(t, s, context.Background(), "anthropic")
	assertJSONError(t, rec, http.StatusUnauthorized)
}

func TestDeleteHandler_InvalidProvider(t *testing.T) {
	s := NewStore(nil, nil)
	rec := doDelete(t, s, authedContext(), "cohere")
	assertJSONError(t, rec, http.StatusBadRequest)
}

func doConnect(t *testing.T, s *Store, ctx context.Context, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := s.ConnectHandler()

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/credentials", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func doDelete(t *testing.T, s *Store, ctx context.Context, providerParam string) *httptest.ResponseRecorder {
	t.Helper()
	h := s.DeleteHandler()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("provider", providerParam)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)

	req := httptest.NewRequestWithContext(ctx, http.MethodDelete, "/v1/credentials/"+providerParam, nil)
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
	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error == "" {
		t.Fatal("expected non-empty error message")
	}
}
