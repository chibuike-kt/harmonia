package credentials

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/provider"
	"github.com/chibuike-kt/harmonia/internal/user"
)

// fakeProviderAgent stands in for a real anthropic/openai client, the
// same seam internal/provider's Agent interface exists for — this proves
// Connect's verify-then-encrypt-then-store logic without ever calling a
// real, keyed provider API (none is available in this environment; see
// internal/provider/anthropic's own integration test).
type fakeProviderAgent struct {
	err error
}

func (f fakeProviderAgent) Generate(ctx context.Context, req provider.GenerateRequest) (provider.GenerateResponse, error) {
	if f.err != nil {
		return provider.GenerateResponse{}, f.err
	}
	return provider.GenerateResponse{Content: "OK"}, nil
}

// TestIntegration_ConnectListDelete exercises POST/GET/DELETE
// /v1/credentials against real Postgres, with a fake provider.Agent
// standing in for the live verification call: a failed verification
// stores nothing, a successful one encrypts and upserts (reconnecting
// replaces the same row), listing returns hints only for the caller's
// own user, and deleting is scoped to the caller's own user with no id
// to enumerate. Requires a live Postgres — run via `make test-integration`
// after `make up`.
func TestIntegration_ConnectListDelete(t *testing.T) {
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

	keyBuf := make([]byte, keyBytes)
	if _, err := rand.Read(keyBuf); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	cipher, err := NewCipher(base64.StdEncoding.EncodeToString(keyBuf))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}

	s := NewStore(pool, cipher)
	user1 := seedCredentialsTestUser(t, ctx, pool, "credentials-http-user1-")
	user2 := seedCredentialsTestUser(t, ctx, pool, "credentials-http-user2-")

	// A rejected key: verification fails, nothing is stored.
	s.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return fakeProviderAgent{err: errors.New("invalid api key")}, nil
	}
	failRec := doConnectRequest(t, s.ConnectHandler(), user1, `{"provider":"anthropic","key":"sk-bad-key"}`)
	if failRec.Code != http.StatusBadRequest {
		t.Fatalf("connect with rejected key status = %d, want %d, body = %s", failRec.Code, http.StatusBadRequest, failRec.Body.String())
	}
	assertCredentialCount(t, ctx, pool, user1.ID, "anthropic", 0)

	// A working key: verified, encrypted, and stored.
	s.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return fakeProviderAgent{}, nil
	}
	connectRec := doConnectRequest(t, s.ConnectHandler(), user1, `{"provider":"anthropic","key":"sk-live-key-1234"}`)
	if connectRec.Code != http.StatusCreated {
		t.Fatalf("connect status = %d, want %d, body = %s", connectRec.Code, http.StatusCreated, connectRec.Body.String())
	}
	var created Credential
	if err := json.Unmarshal(connectRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode connect response: %v", err)
	}
	if created.Provider != "anthropic" {
		t.Fatalf("Provider = %q, want %q", created.Provider, "anthropic")
	}
	if created.KeyHint != "1234" {
		t.Fatalf("KeyHint = %q, want %q", created.KeyHint, "1234")
	}
	if created.VerifiedAt == nil {
		t.Fatal("expected VerifiedAt to be set")
	}
	if respBody := connectRec.Body.String(); containsAny(respBody, "sk-live-key-1234", "encrypted_key", "nonce") {
		t.Fatalf("connect response leaked the plaintext key or storage internals: %s", respBody)
	}

	// The plaintext round-trips through the actual stored ciphertext —
	// proving Connect really encrypted it, not just accepted it.
	assertStoredKeyDecryptsTo(t, ctx, pool, cipher, user1.ID, "anthropic", "sk-live-key-1234")

	// Reconnecting the same provider replaces the row in place (same id,
	// new hint), per UNIQUE(user_id, provider).
	reconnectRec := doConnectRequest(t, s.ConnectHandler(), user1, `{"provider":"anthropic","key":"sk-live-key-5678"}`)
	if reconnectRec.Code != http.StatusCreated {
		t.Fatalf("reconnect status = %d, want %d, body = %s", reconnectRec.Code, http.StatusCreated, reconnectRec.Body.String())
	}
	var reconnected Credential
	if err := json.Unmarshal(reconnectRec.Body.Bytes(), &reconnected); err != nil {
		t.Fatalf("decode reconnect response: %v", err)
	}
	if reconnected.ID != created.ID {
		t.Fatalf("reconnect ID = %s, want same row %s", reconnected.ID, created.ID)
	}
	if reconnected.KeyHint != "5678" {
		t.Fatalf("reconnect KeyHint = %q, want %q", reconnected.KeyHint, "5678")
	}
	assertStoredKeyDecryptsTo(t, ctx, pool, cipher, user1.ID, "anthropic", "sk-live-key-5678")

	// user2 connects their own credential — must not appear in user1's list.
	connectUser2Rec := doConnectRequest(t, s.ConnectHandler(), user2, `{"provider":"openai","key":"sk-user2-key-9999"}`)
	if connectUser2Rec.Code != http.StatusCreated {
		t.Fatalf("connect user2 status = %d, want %d, body = %s", connectUser2Rec.Code, http.StatusCreated, connectUser2Rec.Body.String())
	}

	listRec := doListRequest(t, s.ListHandler(), user1)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d, body = %s", listRec.Code, http.StatusOK, listRec.Body.String())
	}
	var listed []Credential
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listed) != 1 || listed[0].Provider != "anthropic" {
		t.Fatalf("user1's list = %+v, want exactly one anthropic credential", listed)
	}

	// Deleting a provider user1 never connected: not found.
	missingDeleteRec := doDeleteRequest(t, s.DeleteHandler(), user1, "openai")
	if missingDeleteRec.Code != http.StatusNotFound {
		t.Fatalf("delete unconnected provider status = %d, want %d", missingDeleteRec.Code, http.StatusNotFound)
	}
	assertCredentialCount(t, ctx, pool, user2.ID, "openai", 1) // untouched

	// Deleting user1's own anthropic credential: succeeds, and it's gone.
	deleteRec := doDeleteRequest(t, s.DeleteHandler(), user1, "anthropic")
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d, body = %s", deleteRec.Code, http.StatusNoContent, deleteRec.Body.String())
	}
	assertCredentialCount(t, ctx, pool, user1.ID, "anthropic", 0)

	// Deleting it again: not found, not a 500 on a second call.
	redeleteRec := doDeleteRequest(t, s.DeleteHandler(), user1, "anthropic")
	if redeleteRec.Code != http.StatusNotFound {
		t.Fatalf("re-delete status = %d, want %d", redeleteRec.Code, http.StatusNotFound)
	}
}

func seedCredentialsTestUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, githubIDPrefix string) user.User {
	t.Helper()
	var u user.User
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

func doConnectRequest(t *testing.T, h http.HandlerFunc, u user.User, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(user.NewContext(context.Background(), u), http.MethodPost, "/v1/credentials", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func doListRequest(t *testing.T, h http.HandlerFunc, u user.User) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(user.NewContext(context.Background(), u), http.MethodGet, "/v1/credentials", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func doDeleteRequest(t *testing.T, h http.HandlerFunc, u user.User, providerParam string) *httptest.ResponseRecorder {
	t.Helper()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("provider", providerParam)
	ctx := context.WithValue(user.NewContext(context.Background(), u), chi.RouteCtxKey, rctx)

	req := httptest.NewRequestWithContext(ctx, http.MethodDelete, "/v1/credentials/"+providerParam, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func assertCredentialCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, providerName string, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM provider_credentials WHERE user_id = $1 AND provider = $2
	`, userID, providerName).Scan(&count); err != nil {
		t.Fatalf("count provider_credentials: %v", err)
	}
	if count != want {
		t.Fatalf("provider_credentials count for %s/%s = %d, want %d", userID, providerName, count, want)
	}
}

func assertStoredKeyDecryptsTo(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cipher *Cipher, userID uuid.UUID, providerName, want string) {
	t.Helper()
	var encryptedKey, nonce []byte
	err := pool.QueryRow(ctx, `
		SELECT encrypted_key, nonce FROM provider_credentials WHERE user_id = $1 AND provider = $2
	`, userID, providerName).Scan(&encryptedKey, &nonce)
	if err != nil {
		t.Fatalf("fetch stored credential: %v", err)
	}
	if string(encryptedKey) == want {
		t.Fatal("stored encrypted_key must not equal the plaintext key")
	}
	got, err := cipher.Decrypt(encryptedKey, nonce)
	if err != nil {
		t.Fatalf("decrypt stored credential: %v", err)
	}
	if got != want {
		t.Fatalf("decrypted stored credential = %q, want %q", got, want)
	}
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if sub != "" && strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
