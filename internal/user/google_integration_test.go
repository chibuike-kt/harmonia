package user

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"maps"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// TestIntegration_GoogleLoginAndCallback drives GoogleLoginHandler and
// GoogleCallbackHandler against real Postgres and real Redis, with a fake
// httptest.Server standing in for Google's token and JWKS endpoints —
// same technique as the GitHub flow and the Anthropic client's structural
// test in Milestone 1. The ID token here is genuinely RS256-signed with a
// throwaway key, and verifyGoogleIDToken genuinely verifies it against
// the fake JWKS — this isn't a shortcut around verification, it's
// verification exercised against a controlled key instead of Google's
// real one.
//
// It does NOT prove the real Google OAuth integration works — that needs
// a registered OAuth Client's real client ID/secret/callback URL (see the
// Phase 2 build brief's prerequisite) and stays unverified until then,
// the same distinction as the GitHub flow and the Anthropic client in
// Milestone 1.
func TestIntegration_GoogleLoginAndCallback(t *testing.T) {
	dbURL := os.Getenv("HARMONIA_DATABASE_URL")
	if dbURL == "" {
		t.Skip("HARMONIA_DATABASE_URL not set; skipping integration test")
	}
	redisAddr := os.Getenv("HARMONIA_REDIS_ADDR")
	if redisAddr == "" {
		t.Skip("HARMONIA_REDIS_ADDR not set; skipping integration test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	defer pool.Close()

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	defer func() { _ = rdb.Close() }()

	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}

	const testIssuer = "https://accounts.google.test"
	const testClientID = "test-client-id"
	const testKid = "test-key-1"
	const testSubject = "1234567890"

	idToken := signTestGoogleIDToken(t, privKey, testKid, map[string]any{
		"iss":            testIssuer,
		"aud":            testClientID,
		"sub":            testSubject,
		"email":          "octocat@example.com",
		"email_verified": true,
		"name":           "The Octocat",
		"picture":        "https://example.com/avatar.png",
		"exp":            time.Now().Add(time.Hour).Unix(),
		"iat":            time.Now().Unix(),
	})

	var gotTokenRequest url.Values

	fakeGoogle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse token request form: %v", err)
			}
			gotTokenRequest = r.Form
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id_token":     idToken,
				"access_token": "fake-access-token",
			})
		case "/certs":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"keys": []map[string]string{jwkFromPublicKey(testKid, &privKey.PublicKey)},
			})
		default:
			t.Fatalf("unexpected fake Google request: %s", r.URL.Path)
		}
	}))
	defer fakeGoogle.Close()

	cfg := GoogleConfig{
		ClientID:     testClientID,
		ClientSecret: "test-client-secret",
		RedirectURI:  "http://localhost:8080/v1/auth/google/callback",
		AuthorizeURL: fakeGoogle.URL + "/authorize",
		TokenURL:     fakeGoogle.URL + "/token",
		JWKSURL:      fakeGoogle.URL + "/certs",
		Issuer:       testIssuer,
		HTTPClient:   fakeGoogle.Client(),
	}

	s := NewStore(pool)
	loginHandler := s.GoogleLoginHandler(cfg, rdb)
	callbackHandler := s.GoogleCallbackHandler(cfg, rdb)

	// Login: expect a redirect to the (fake) authorize URL with the
	// right client_id/redirect_uri/response_type/scope and a fresh state.
	loginReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/auth/google/login", nil)
	loginRec := httptest.NewRecorder()
	loginHandler.ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusFound {
		t.Fatalf("login status = %d, want %d", loginRec.Code, http.StatusFound)
	}
	loc, err := url.Parse(loginRec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect location: %v", err)
	}
	if loc.Path != "/authorize" {
		t.Fatalf("redirect path = %q, want %q", loc.Path, "/authorize")
	}
	q := loc.Query()
	if q.Get("client_id") != cfg.ClientID {
		t.Fatalf("client_id = %q, want %q", q.Get("client_id"), cfg.ClientID)
	}
	if q.Get("redirect_uri") != cfg.RedirectURI {
		t.Fatalf("redirect_uri = %q, want %q", q.Get("redirect_uri"), cfg.RedirectURI)
	}
	if q.Get("response_type") != "code" {
		t.Fatalf("response_type = %q, want %q", q.Get("response_type"), "code")
	}
	if q.Get("scope") != googleOAuthScope {
		t.Fatalf("scope = %q, want %q", q.Get("scope"), googleOAuthScope)
	}
	state := q.Get("state")
	if state == "" {
		t.Fatal("expected a non-empty state parameter")
	}

	if exists, err := rdb.Exists(ctx, googleOAuthStateKeyPrefix+state).Result(); err != nil || exists == 0 {
		t.Fatalf("state not found in redis: exists=%d err=%v", exists, err)
	}

	// Callback: exchange the (fake) code for the signed ID token, verify
	// it, upsert, issue a session, set the cookie.
	callbackReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/auth/google/callback?code=fake-code&state="+state, nil)
	callbackRec := httptest.NewRecorder()
	callbackHandler.ServeHTTP(callbackRec, callbackReq)

	if callbackRec.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want %d, body = %s", callbackRec.Code, http.StatusFound, callbackRec.Body.String())
	}
	if gotTokenRequest.Get("code") != "fake-code" {
		t.Fatalf("token request code = %q, want %q", gotTokenRequest.Get("code"), "fake-code")
	}
	if gotTokenRequest.Get("grant_type") != "authorization_code" {
		t.Fatalf("token request grant_type = %q, want %q", gotTokenRequest.Get("grant_type"), "authorization_code")
	}

	cookies := callbackRec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == SessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected a session cookie to be set")
	}
	if !sessionCookie.HttpOnly || !sessionCookie.Secure || sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie attributes = %+v, want HttpOnly+Secure+SameSite=Lax", sessionCookie)
	}

	// The state is one-time use — a replayed callback must fail.
	replayReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/auth/google/callback?code=fake-code&state="+state, nil)
	replayRec := httptest.NewRecorder()
	callbackHandler.ServeHTTP(replayRec, replayReq)
	if replayRec.Code != http.StatusBadRequest {
		t.Fatalf("replayed state status = %d, want %d", replayRec.Code, http.StatusBadRequest)
	}

	authenticated, err := s.Authenticate(ctx, HashSessionToken(sessionCookie.Value))
	if err != nil {
		t.Fatalf("Authenticate with issued session: %v", err)
	}
	if authenticated.Username != "octocat" {
		t.Fatalf("authenticated Username = %q, want %q", authenticated.Username, "octocat")
	}
	if authenticated.GoogleID == nil || *authenticated.GoogleID != testSubject {
		t.Fatalf("authenticated GoogleID = %v, want %q", authenticated.GoogleID, testSubject)
	}
	if authenticated.DisplayName == nil || *authenticated.DisplayName != "The Octocat" {
		t.Fatalf("authenticated DisplayName = %v, want %q", authenticated.DisplayName, "The Octocat")
	}
	if authenticated.Email == nil || *authenticated.Email != "octocat@example.com" {
		t.Fatalf("authenticated Email = %v, want %q", authenticated.Email, "octocat@example.com")
	}
}

// TestVerifyGoogleIDToken_Failures proves verifyGoogleIDToken actually
// checks signature/issuer/audience/expiry rather than trusting a decoded
// token — no live Postgres/Redis needed for this one.
func TestVerifyGoogleIDToken_Failures(t *testing.T) {
	signingKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}

	const testIssuer = "https://accounts.google.test"
	const testClientID = "test-client-id"
	const testKid = "test-key-1"

	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{jwkFromPublicKey(testKid, &signingKey.PublicKey)},
		})
	}))
	defer jwksServer.Close()

	cfg := GoogleConfig{
		ClientID:   testClientID,
		JWKSURL:    jwksServer.URL,
		Issuer:     testIssuer,
		HTTPClient: jwksServer.Client(),
	}

	validClaims := map[string]any{
		"iss": testIssuer,
		"aud": testClientID,
		"sub": "1234567890",
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}

	tests := []struct {
		name string
		key  *rsa.PrivateKey
		kid  string
		mut  func(map[string]any)
	}{
		{"wrong signing key", otherKey, testKid, nil},
		{"unknown kid", signingKey, "no-such-key", nil},
		{"wrong issuer", signingKey, testKid, func(c map[string]any) { c["iss"] = "https://evil.example" }},
		{"wrong audience", signingKey, testKid, func(c map[string]any) { c["aud"] = "someone-elses-client-id" }},
		{"expired", signingKey, testKid, func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := make(map[string]any, len(validClaims))
			maps.Copy(claims, validClaims)
			if tt.mut != nil {
				tt.mut(claims)
			}

			token := signTestGoogleIDToken(t, tt.key, tt.kid, claims)
			if _, err := verifyGoogleIDToken(context.Background(), cfg, token); err == nil {
				t.Fatal("expected verification to fail")
			}
		})
	}
}

func jwkFromPublicKey(kid string, pub *rsa.PublicKey) map[string]string {
	return map[string]string{
		"kid": kid,
		"kty": "RSA",
		"alg": "RS256",
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

// signTestGoogleIDToken builds and RS256-signs a JWT the same way
// verifyGoogleIDToken expects to check one — a from-scratch stand-in for
// a real Google-issued ID token, signed with a throwaway test key.
func signTestGoogleIDToken(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()

	header := map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerB64 + "." + claimsB64

	hashed := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hashed[:])
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}
