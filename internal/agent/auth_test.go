package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func assertUnauthorizedJSON(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
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

func TestGenerateAPIKey(t *testing.T) {
	plaintext, hash, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if plaintext == "" || hash == "" {
		t.Fatal("expected non-empty plaintext and hash")
	}
	if plaintext == hash {
		t.Fatal("hash must not equal plaintext")
	}
	if got := HashAPIKey(plaintext); got != hash {
		t.Fatalf("HashAPIKey(plaintext) = %q, want %q", got, hash)
	}

	plaintext2, hash2, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if plaintext == plaintext2 || hash == hash2 {
		t.Fatal("expected distinct keys across calls")
	}
}

func TestAuthenticate_MissingHeader(t *testing.T) {
	h := Authenticate(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must not run without credentials")
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertUnauthorizedJSON(t, rec)
}

func TestAuthenticate_MalformedHeader(t *testing.T) {
	h := Authenticate(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must not run without credentials")
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic abc123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertUnauthorizedJSON(t, rec)
}

func TestRequireRoom_NoAgentInContext(t *testing.T) {
	h := RequireRoom(RoomIDParam)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must not run without an authenticated agent")
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertUnauthorizedJSON(t, rec)
}

func TestRequireRoom_Mismatch(t *testing.T) {
	agentRoom := uuid.New()
	urlRoom := uuid.New()

	h := RequireRoom(RoomIDParam)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must not run on room mismatch")
	}))

	req := requestWithAgentAndRoomParam(t, Agent{RoomID: agentRoom}, urlRoom)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertUnauthorizedJSON(t, rec)
}

func TestRequireRoom_Match(t *testing.T) {
	room := uuid.New()
	handlerCalled := false

	h := RequireRoom(RoomIDParam)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
	}))

	req := requestWithAgentAndRoomParam(t, Agent{RoomID: room}, room)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !handlerCalled {
		t.Fatal("next handler must run when room matches")
	}
}

func requestWithAgentAndRoomParam(t *testing.T, a Agent, roomParam uuid.UUID) *http.Request {
	t.Helper()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(RoomIDParam, roomParam.String())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, agentCtxKey, a)
	return req.WithContext(ctx)
}
