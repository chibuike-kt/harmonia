package realtime

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

func TestStreamHandler_Unauthenticated(t *testing.T) {
	h := StreamHandler(nil, nil, nil, nil, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/rooms/"+uuid.New().String()+"/stream", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertJSONError(t, rec, http.StatusUnauthorized)
}

func TestStreamHandler_InvalidRoomID(t *testing.T) {
	h := StreamHandler(nil, nil, nil, nil, nil)

	req := httptest.NewRequestWithContext(user.NewContext(context.Background(), user.User{ID: uuid.New()}), http.MethodGet, "/v1/rooms/not-a-uuid/stream", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("room_id", "not-a-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

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
	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error == "" {
		t.Fatal("expected non-empty error message")
	}
}
