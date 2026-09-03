package user

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestSessionsHandler_Unauthenticated(t *testing.T) {
	s := &Store{}
	h := s.SessionsHandler()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/sessions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusUnauthorized)
}

func TestRevokeSessionHandler_Unauthenticated(t *testing.T) {
	s := &Store{}
	h := s.RevokeSessionHandler()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/v1/sessions/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusUnauthorized)
}

func TestRevokeSessionHandler_InvalidID(t *testing.T) {
	s := &Store{}
	h := s.RevokeSessionHandler()

	rec := doRevokeSession(t, h, NewContext(context.Background(), User{ID: uuid.New()}), "not-a-uuid")
	assertJSONError(t, rec, http.StatusBadRequest)
}

func TestLogoutHandler_NoCookie(t *testing.T) {
	s := &Store{}
	h := s.LogoutHandler()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/auth/logout", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	assertCookieCleared(t, rec)
}

func doRevokeSession(t *testing.T, h http.HandlerFunc, ctx context.Context, sessionIDParam string) *httptest.ResponseRecorder {
	t.Helper()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", sessionIDParam)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)

	req := httptest.NewRequestWithContext(ctx, http.MethodDelete, "/v1/sessions/"+sessionIDParam, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func assertCookieCleared(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName {
			if c.MaxAge >= 0 {
				t.Fatalf("cleared cookie MaxAge = %d, want negative", c.MaxAge)
			}
			return
		}
	}
	t.Fatal("expected a session cookie to be set (clearing it)")
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
