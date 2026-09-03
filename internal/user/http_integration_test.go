package user

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestIntegration_SessionsListRevokeLogout exercises GET /v1/sessions,
// DELETE /v1/sessions/{id}, and POST /v1/auth/logout against real
// Postgres: a user's own sessions list with the request's own session
// flagged current, revoking one's own session removes it and stops it
// authenticating, revoking another user's session (or an unknown id)
// 404s, and logout revokes the current session and clears the cookie.
// Requires a live Postgres — run via `make test-integration` after `make up`.
func TestIntegration_SessionsListRevokeLogout(t *testing.T) {
	dbURL := os.Getenv("HARMONIA_DATABASE_URL")
	if dbURL == "" {
		t.Skip("HARMONIA_DATABASE_URL not set; skipping integration test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	s := NewStore(pool)

	user1 := seedTestUser(t, ctx, pool, "sessions-http-user1-")
	user2 := seedTestUser(t, ctx, pool, "sessions-http-user2-")

	plaintext1, hash1, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken: %v", err)
	}
	sess1, err := s.CreateSession(ctx, user1.ID, hash1, nil, nil)
	if err != nil {
		t.Fatalf("CreateSession sess1: %v", err)
	}

	_, hash2, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken: %v", err)
	}
	sess2, err := s.CreateSession(ctx, user1.ID, hash2, nil, nil)
	if err != nil {
		t.Fatalf("CreateSession sess2: %v", err)
	}

	_, otherHash, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken: %v", err)
	}
	otherSess, err := s.CreateSession(ctx, user2.ID, otherHash, nil, nil)
	if err != nil {
		t.Fatalf("CreateSession otherSess: %v", err)
	}

	// List: both of user1's sessions come back, sess1 (the one whose
	// cookie made the request) flagged current, sess2 not.
	listRec := doSessionsRequest(t, s.SessionsHandler(), user1, plaintext1)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d, body = %s", listRec.Code, http.StatusOK, listRec.Body.String())
	}
	var listed []SessionWithCurrent
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode sessions list: %v", err)
	}
	byID := make(map[uuid.UUID]SessionWithCurrent, len(listed))
	for _, sess := range listed {
		byID[sess.ID] = sess
	}
	if got, ok := byID[sess1.ID]; !ok || !got.Current {
		t.Fatalf("sess1 in list = %+v, ok = %v, want present and current", got, ok)
	}
	if got, ok := byID[sess2.ID]; !ok || got.Current {
		t.Fatalf("sess2 in list = %+v, ok = %v, want present and not current", got, ok)
	}
	if _, ok := byID[otherSess.ID]; ok {
		t.Fatal("user2's session must not appear in user1's list")
	}

	// Revoke sess2 (owned by user1) while sess1 is the request's current
	// cookie: succeeds, sess2 stops authenticating, but sess2 isn't the
	// current session, so the caller's cookie must be left untouched.
	revokeRec := doRevokeSessionRequest(t, s.RevokeSessionHandler(), user1, sess2.ID.String(), plaintext1)
	if revokeRec.Code != http.StatusNoContent {
		t.Fatalf("revoke own session status = %d, want %d", revokeRec.Code, http.StatusNoContent)
	}
	assertNoCookieSet(t, revokeRec)
	if _, err := s.Authenticate(ctx, hash2); err == nil {
		t.Fatal("sess2 must not authenticate after being revoked")
	}
	if reRevoked, _, err := s.RevokeSession(ctx, sess2.ID, user1.ID, ""); err != nil || reRevoked {
		t.Fatalf("re-revoking sess2 = (%v, %v), want (false, nil)", reRevoked, err)
	}

	// Revoke sess3 while its own cookie is the request's current one:
	// self-revoking the live session — this is the one case that must
	// also clear the caller's cookie, same as logout would.
	plaintext3, hash3, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken: %v", err)
	}
	sess3, err := s.CreateSession(ctx, user1.ID, hash3, nil, nil)
	if err != nil {
		t.Fatalf("CreateSession sess3: %v", err)
	}
	selfRevokeRec := doRevokeSessionRequest(t, s.RevokeSessionHandler(), user1, sess3.ID.String(), plaintext3)
	if selfRevokeRec.Code != http.StatusNoContent {
		t.Fatalf("self-revoke current session status = %d, want %d", selfRevokeRec.Code, http.StatusNoContent)
	}
	assertCookieCleared(t, selfRevokeRec)
	if _, err := s.Authenticate(ctx, hash3); err == nil {
		t.Fatal("sess3 must not authenticate after self-revoking its own current session")
	}

	// Revoke otherSess (owned by user2) as user1, presenting sess1's
	// cookie: not found, not forbidden — the response can't be used to
	// confirm the id exists — and, being neither owned nor current,
	// leaves the cookie untouched.
	crossRevokeRec := doRevokeSessionRequest(t, s.RevokeSessionHandler(), user1, otherSess.ID.String(), plaintext1)
	if crossRevokeRec.Code != http.StatusNotFound {
		t.Fatalf("revoke other user's session status = %d, want %d", crossRevokeRec.Code, http.StatusNotFound)
	}
	assertNoCookieSet(t, crossRevokeRec)
	if _, err := s.Authenticate(ctx, otherHash); err != nil {
		t.Fatalf("otherSess must still authenticate after a rejected cross-user revoke: %v", err)
	}

	// Revoke an unknown id: also not found.
	unknownRevokeRec := doRevokeSessionRequest(t, s.RevokeSessionHandler(), user1, uuid.New().String(), plaintext1)
	if unknownRevokeRec.Code != http.StatusNotFound {
		t.Fatalf("revoke unknown session status = %d, want %d", unknownRevokeRec.Code, http.StatusNotFound)
	}

	// Logout: revokes sess1 (the current one) and clears the cookie.
	logoutReq := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/auth/logout", nil)
	logoutReq.AddCookie(&http.Cookie{Name: SessionCookieName, Value: plaintext1})
	logoutRec := httptest.NewRecorder()
	s.LogoutHandler().ServeHTTP(logoutRec, logoutReq)

	if logoutRec.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want %d", logoutRec.Code, http.StatusNoContent)
	}
	assertCookieCleared(t, logoutRec)
	if _, err := s.Authenticate(ctx, hash1); err == nil {
		t.Fatal("sess1 must not authenticate after logout")
	}

	// Logout again with the same (now-stale) cookie: still a clean no-op.
	replayRec := httptest.NewRecorder()
	s.LogoutHandler().ServeHTTP(replayRec, logoutReq)
	if replayRec.Code != http.StatusNoContent {
		t.Fatalf("replayed logout status = %d, want %d", replayRec.Code, http.StatusNoContent)
	}
}

func seedTestUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, githubIDPrefix string) User {
	t.Helper()
	var u User
	githubID := githubIDPrefix + uuid.New().String()
	err := pool.QueryRow(ctx, `
		INSERT INTO users (github_id, username) VALUES ($1, $2)
		RETURNING id, github_id, google_id, username, display_name, avatar_url, email, created_at
	`, githubID, githubIDPrefix+"user").Scan(
		&u.ID, &u.GitHubID, &u.GoogleID, &u.Username, &u.DisplayName, &u.AvatarURL, &u.Email, &u.CreatedAt,
	)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u
}

func doSessionsRequest(t *testing.T, h http.HandlerFunc, u User, cookieValue string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(NewContext(context.Background(), u), http.MethodGet, "/v1/sessions", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: cookieValue})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// doRevokeSessionRequest drives RevokeSessionHandler as user u revoking
// sessionIDParam. cookieValue, when non-empty, is set as the request's
// own session cookie — pass the plaintext of whichever session (if any)
// is meant to be "current" for that call; omit it to simulate no cookie
// at all.
func doRevokeSessionRequest(t *testing.T, h http.HandlerFunc, u User, sessionIDParam, cookieValue string) *httptest.ResponseRecorder {
	t.Helper()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", sessionIDParam)
	ctx := context.WithValue(NewContext(context.Background(), u), chi.RouteCtxKey, rctx)

	req := httptest.NewRequestWithContext(ctx, http.MethodDelete, "/v1/sessions/"+sessionIDParam, nil)
	if cookieValue != "" {
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: cookieValue})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// assertNoCookieSet fails the test if the response set a session cookie
// at all — the negative counterpart to assertCookieCleared, for the
// cases that must leave the caller's cookie untouched.
func assertNoCookieSet(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName {
			t.Fatalf("expected no session cookie in response, got %+v", c)
		}
	}
}
