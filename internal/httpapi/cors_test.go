package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFrontendOrigin(t *testing.T) {
	tests := []struct {
		name   string
		appURL string
		want   string
	}{
		{"unset", "", ""},
		{"relative path default", "/", ""},
		{"malformed, no scheme or host", "not a url", ""},
		{"valid origin, no path", "http://localhost:3000", "http://localhost:3000"},
		{"valid origin, with path", "http://localhost:3000/dashboard", "http://localhost:3000"},
		{"valid https origin", "https://app.harmonia.example", "https://app.harmonia.example"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HARMONIA_APP_URL", tt.appURL)
			if got := frontendOrigin(); got != tt.want {
				t.Fatalf("frontendOrigin() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewCORSMiddleware_UnconfiguredIsNil(t *testing.T) {
	t.Setenv("HARMONIA_APP_URL", "")
	if mw := newCORSMiddleware(); mw != nil {
		t.Fatal("expected a nil middleware when HARMONIA_APP_URL is unset — no CORS at all, not an open policy")
	}
}

// TestNewCORSMiddleware_AllowsConfiguredOriginOnly proves the fail-closed
// behavior end to end: a request from the configured frontend origin
// gets the headers that let the browser expose the response to it, and
// a request claiming any other origin gets none — the browser's own
// same-origin policy then keeps that response from the calling script.
func TestNewCORSMiddleware_AllowsConfiguredOriginOnly(t *testing.T) {
	t.Setenv("HARMONIA_APP_URL", "http://localhost:3000")
	mw := newCORSMiddleware()
	if mw == nil {
		t.Fatal("expected a non-nil middleware when HARMONIA_APP_URL is set")
	}

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/credentials", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, "http://localhost:3000")
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("Access-Control-Allow-Credentials = %q, want %q", got, "true")
	}

	otherReq := httptest.NewRequest(http.MethodGet, "/v1/credentials", nil)
	otherReq.Header.Set("Origin", "http://evil.example")
	otherRec := httptest.NewRecorder()
	handler.ServeHTTP(otherRec, otherReq)
	if got := otherRec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin for a disallowed origin = %q, want empty", got)
	}
}

// TestNewCORSMiddleware_Preflight confirms a browser's preflight OPTIONS
// request is answered by the CORS middleware itself — never forwarded to
// the actual route handler, which for most of this API has no OPTIONS
// method registered at all.
func TestNewCORSMiddleware_Preflight(t *testing.T) {
	t.Setenv("HARMONIA_APP_URL", "http://localhost:3000")
	mw := newCORSMiddleware()

	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodOptions, "/v1/credentials", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "Content-Type")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if called {
		t.Fatal("a preflight OPTIONS request must not reach the wrapped handler")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("preflight status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatal("expected a non-empty Access-Control-Allow-Methods on the preflight response")
	}
}
