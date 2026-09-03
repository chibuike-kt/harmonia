package user

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestIntegration_GetByID exercises Store.GetByID against real Postgres.
// There's no Store method to create a user yet — upsert-by-provider is
// steps 2/3's job, not this one's — so this seeds a row directly via SQL,
// a fair stand-in until a real writer exists. Requires a live Postgres —
// run via `make test-integration` after `make up`.
func TestIntegration_GetByID(t *testing.T) {
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

	githubID := "gh-test-" + uuid.New().String()
	var userID uuid.UUID
	err = pool.QueryRow(ctx, `
		INSERT INTO users (github_id, username) VALUES ($1, $2) RETURNING id
	`, githubID, "octocat-test").Scan(&userID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	s := NewStore(pool)

	got, err := s.GetByID(ctx, userID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != userID {
		t.Fatalf("ID = %s, want %s", got.ID, userID)
	}
	if got.Username != "octocat-test" {
		t.Fatalf("Username = %q, want %q", got.Username, "octocat-test")
	}
	if got.GitHubID == nil || *got.GitHubID != githubID {
		t.Fatalf("GitHubID = %v, want %s", got.GitHubID, githubID)
	}
	if got.GoogleID != nil {
		t.Fatalf("GoogleID = %v, want nil", got.GoogleID)
	}

	if _, err := s.GetByID(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByID unknown id: err = %v, want ErrNotFound", err)
	}
}
