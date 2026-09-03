package user

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// TestIntegration_GitHubLoginAndCallback drives GitHubLoginHandler and
// GitHubCallbackHandler against real Postgres and real Redis, with a
// fake httptest.Server standing in for GitHub's token and userinfo
// endpoints — same technique as the Anthropic client's structural test in
// Milestone 1. This proves the request/response shapes, CSRF state
// handling (including that a replayed callback fails), and the
// upsert-by-github_id + session-issuance logic are right.
//
// It does NOT prove the real GitHub OAuth integration works — that needs
// a registered OAuth App's real client ID/secret/callback URL (see the
// Phase 2 build brief's prerequisite) and stays unverified until then,
// the same way the Anthropic client sat structurally-verified-only until
// a real key existed in Milestone 1.
func TestIntegration_GitHubLoginAndCallback(t *testing.T) {
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

	const fakeGitHubID = 999999001
	var gotTokenRequest url.Values

	fakeGitHub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse token request form: %v", err)
			}
			gotTokenRequest = r.Form
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "fake-access-token"})
		case "/user":
			if got := r.Header.Get("Authorization"); got != "Bearer fake-access-token" {
				t.Fatalf("Authorization = %q, want %q", got, "Bearer fake-access-token")
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         fakeGitHubID,
				"login":      "octocat-test",
				"name":       "The Octocat",
				"avatar_url": "https://example.com/avatar.png",
				"email":      "octocat@example.com",
			})
		default:
			t.Fatalf("unexpected fake GitHub request: %s", r.URL.Path)
		}
	}))
	defer fakeGitHub.Close()

	cfg := GitHubConfig{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		RedirectURI:  "http://localhost:8080/v1/auth/github/callback",
		AuthorizeURL: fakeGitHub.URL + "/login/oauth/authorize",
		TokenURL:     fakeGitHub.URL + "/login/oauth/access_token",
		UserURL:      fakeGitHub.URL + "/user",
		HTTPClient:   fakeGitHub.Client(),
	}

	s := NewStore(pool)
	loginHandler := s.GitHubLoginHandler(cfg, rdb)
	callbackHandler := s.GitHubCallbackHandler(cfg, rdb)

	// Login: expect a redirect to the (fake) authorize URL with the
	// right client_id/redirect_uri/scope and a fresh state parameter.
	loginReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/auth/github/login", nil)
	loginRec := httptest.NewRecorder()
	loginHandler.ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusFound {
		t.Fatalf("login status = %d, want %d", loginRec.Code, http.StatusFound)
	}
	loc, err := url.Parse(loginRec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect location: %v", err)
	}
	if loc.Path != "/login/oauth/authorize" {
		t.Fatalf("redirect path = %q, want %q", loc.Path, "/login/oauth/authorize")
	}
	q := loc.Query()
	if q.Get("client_id") != cfg.ClientID {
		t.Fatalf("client_id = %q, want %q", q.Get("client_id"), cfg.ClientID)
	}
	if q.Get("redirect_uri") != cfg.RedirectURI {
		t.Fatalf("redirect_uri = %q, want %q", q.Get("redirect_uri"), cfg.RedirectURI)
	}
	if q.Get("scope") != githubOAuthScope {
		t.Fatalf("scope = %q, want %q", q.Get("scope"), githubOAuthScope)
	}
	state := q.Get("state")
	if state == "" {
		t.Fatal("expected a non-empty state parameter")
	}

	// Confirm the state actually landed in Redis, consistent with the
	// "store it server-side" choice documented on GitHubLoginHandler.
	if exists, err := rdb.Exists(ctx, githubOAuthStateKeyPrefix+state).Result(); err != nil || exists == 0 {
		t.Fatalf("state not found in redis: exists=%d err=%v", exists, err)
	}

	// Callback: exchange the (fake) code, fetch the (fake) user, upsert,
	// issue a session, set the cookie.
	callbackReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/auth/github/callback?code=fake-code&state="+state, nil)
	callbackRec := httptest.NewRecorder()
	callbackHandler.ServeHTTP(callbackRec, callbackReq)

	if callbackRec.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want %d, body = %s", callbackRec.Code, http.StatusFound, callbackRec.Body.String())
	}
	if gotTokenRequest.Get("code") != "fake-code" {
		t.Fatalf("token request code = %q, want %q", gotTokenRequest.Get("code"), "fake-code")
	}
	if gotTokenRequest.Get("client_secret") != cfg.ClientSecret {
		t.Fatalf("token request client_secret = %q, want %q", gotTokenRequest.Get("client_secret"), cfg.ClientSecret)
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
	replayReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/auth/github/callback?code=fake-code&state="+state, nil)
	replayRec := httptest.NewRecorder()
	callbackHandler.ServeHTTP(replayRec, replayReq)
	if replayRec.Code != http.StatusBadRequest {
		t.Fatalf("replayed state status = %d, want %d", replayRec.Code, http.StatusBadRequest)
	}

	// The session actually resolves via Store.Authenticate, proving the
	// upsert + session issuance produced something real, not just a 302
	// with an empty cookie.
	authenticated, err := s.Authenticate(ctx, HashSessionToken(sessionCookie.Value))
	if err != nil {
		t.Fatalf("Authenticate with issued session: %v", err)
	}
	if authenticated.Username != "octocat-test" {
		t.Fatalf("authenticated Username = %q, want %q", authenticated.Username, "octocat-test")
	}
	if authenticated.GitHubID == nil || *authenticated.GitHubID != "999999001" {
		t.Fatalf("authenticated GitHubID = %v, want %q", authenticated.GitHubID, "999999001")
	}
	if authenticated.DisplayName == nil || *authenticated.DisplayName != "The Octocat" {
		t.Fatalf("authenticated DisplayName = %v, want %q", authenticated.DisplayName, "The Octocat")
	}
}
