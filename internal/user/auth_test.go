package user

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGenerateSessionToken(t *testing.T) {
	plaintext, hash, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken: %v", err)
	}
	if plaintext == "" || hash == "" {
		t.Fatal("expected non-empty plaintext and hash")
	}
	if plaintext == hash {
		t.Fatal("hash must not equal plaintext")
	}
	if got := HashSessionToken(plaintext); got != hash {
		t.Fatalf("HashSessionToken(plaintext) = %q, want %q", got, hash)
	}

	plaintext2, hash2, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken: %v", err)
	}
	if plaintext == plaintext2 || hash == hash2 {
		t.Fatal("expected distinct tokens across calls")
	}
}

func TestAuthenticate_MissingCookie(t *testing.T) {
	h := Authenticate(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must not run without a session cookie")
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", ct, "application/json")
	}
}

func TestAuthenticate_EmptyCookie(t *testing.T) {
	h := Authenticate(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must not run with an empty session cookie")
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: ""})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
