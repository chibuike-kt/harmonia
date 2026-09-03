package room

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateHandler_InvalidBody(t *testing.T) {
	s := &Store{}
	h := s.CreateHandler()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/rooms", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusBadRequest)
}

func TestCreateHandler_MissingName(t *testing.T) {
	s := &Store{}
	h := s.CreateHandler()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/rooms", strings.NewReader(`{}`))
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
