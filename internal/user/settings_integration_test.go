package user

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestIntegration_MeAndUpdateMe exercises GET/PATCH /v1/users/me against
// real Postgres: GET returns the authenticated user's own profile,
// PATCH updates only the fields given (username alone, then display
// name alone, then a no-op empty body), and an empty username is
// rejected rather than blanking out the NOT NULL column. Requires a
// live Postgres — run via `make test-integration` after `make up`.
func TestIntegration_MeAndUpdateMe(t *testing.T) {
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
	u := seedSettingsTestUser(t, ctx, pool, "settings-test-")

	getRec := doMeRequest(t, s.MeHandler(), u)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d, body = %s", getRec.Code, http.StatusOK, getRec.Body.String())
	}
	var got User
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if got.ID != u.ID || got.Username != u.Username {
		t.Fatalf("GET /v1/users/me = %+v, want ID=%s Username=%s", got, u.ID, u.Username)
	}

	// Update username only — display_name (currently nil) is untouched.
	patchRec := doUpdateMeRequest(t, s.UpdateMeHandler(), u, `{"username":"renamed-user"}`)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("PATCH username status = %d, want %d, body = %s", patchRec.Code, http.StatusOK, patchRec.Body.String())
	}
	var afterUsername User
	if err := json.Unmarshal(patchRec.Body.Bytes(), &afterUsername); err != nil {
		t.Fatalf("decode PATCH response: %v", err)
	}
	if afterUsername.Username != "renamed-user" {
		t.Fatalf("Username = %q, want %q", afterUsername.Username, "renamed-user")
	}
	if afterUsername.DisplayName != nil {
		t.Fatalf("DisplayName = %v, want nil (untouched)", afterUsername.DisplayName)
	}

	// Update display_name only — username from the previous update sticks.
	patchRec2 := doUpdateMeRequest(t, s.UpdateMeHandler(), u, `{"display_name":"Renamed User"}`)
	if patchRec2.Code != http.StatusOK {
		t.Fatalf("PATCH display_name status = %d, want %d, body = %s", patchRec2.Code, http.StatusOK, patchRec2.Body.String())
	}
	var afterDisplayName User
	if err := json.Unmarshal(patchRec2.Body.Bytes(), &afterDisplayName); err != nil {
		t.Fatalf("decode PATCH response: %v", err)
	}
	if afterDisplayName.Username != "renamed-user" {
		t.Fatalf("Username = %q, want %q (unchanged)", afterDisplayName.Username, "renamed-user")
	}
	if afterDisplayName.DisplayName == nil || *afterDisplayName.DisplayName != "Renamed User" {
		t.Fatalf("DisplayName = %v, want %q", afterDisplayName.DisplayName, "Renamed User")
	}

	// An empty body changes nothing.
	patchRec3 := doUpdateMeRequest(t, s.UpdateMeHandler(), u, `{}`)
	if patchRec3.Code != http.StatusOK {
		t.Fatalf("PATCH empty body status = %d, want %d, body = %s", patchRec3.Code, http.StatusOK, patchRec3.Body.String())
	}
	var afterNoop User
	if err := json.Unmarshal(patchRec3.Body.Bytes(), &afterNoop); err != nil {
		t.Fatalf("decode PATCH response: %v", err)
	}
	if afterNoop.Username != "renamed-user" || afterNoop.DisplayName == nil || *afterNoop.DisplayName != "Renamed User" {
		t.Fatalf("PATCH {} changed state: %+v", afterNoop)
	}

	// An empty username is rejected, not applied.
	rejectRec := doUpdateMeRequest(t, s.UpdateMeHandler(), u, `{"username":""}`)
	if rejectRec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH empty username status = %d, want %d, body = %s", rejectRec.Code, http.StatusBadRequest, rejectRec.Body.String())
	}
	stillThere, err := s.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID after rejected update: %v", err)
	}
	if stillThere.Username != "renamed-user" {
		t.Fatalf("Username after rejected empty update = %q, want %q", stillThere.Username, "renamed-user")
	}
}

func seedSettingsTestUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, githubIDPrefix string) User {
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

func doMeRequest(t *testing.T, h http.HandlerFunc, u User) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(NewContext(context.Background(), u), http.MethodGet, "/v1/users/me", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func doUpdateMeRequest(t *testing.T, h http.HandlerFunc, u User, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(NewContext(context.Background(), u), http.MethodPatch, "/v1/users/me", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
