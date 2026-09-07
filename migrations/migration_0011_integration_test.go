// Package migrations holds this repo's schema migrations (the .sql files
// alongside this test) plus the one Go test that exercises them directly.
package migrations

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// connectMigrationTestPool connects with the superuser role migrations
// themselves run as (HARMONIA_MIGRATE_DATABASE_URL) — DDL rights
// harmonia_app deliberately doesn't have (see
// 0005_harmonia_app_role.up.sql). Skips if not configured, same
// convention every other integration test in this repo already follows.
func connectMigrationTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("HARMONIA_MIGRATE_DATABASE_URL")
	if dbURL == "" {
		t.Skip("HARMONIA_MIGRATE_DATABASE_URL not set; skipping integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestIntegration_Migration0011_BackfillsExistingMentions proves
// 0011_multi_agent_dynamics.up.sql's backfill step for real: not that the
// INSERT ... SELECT compiles, but that a real pre-existing single mention
// survives the migration as the equivalent message_mentions row, a
// message with no mention gets no row, and the old column is actually
// gone afterward.
//
// Runs the migration's real, unmodified up.sql against a throwaway
// schema seeded with realistic pre-migration data — isolated from the
// shared dev database's own already-migrated public schema, so this
// never touches (or risks) the schema the running server is connected
// to. The pre-migration tables here are deliberately minimal: only the
// columns 0011's own statements actually read or write (messages.id/
// mentioned_agent_id, the agents/rooms FK targets), not a full schema
// clone — nothing else about the real tables' shape affects this
// migration's logic.
func TestIntegration_Migration0011_BackfillsExistingMentions(t *testing.T) {
	pool := connectMigrationTestPool(t)
	ctx := context.Background()

	schema := "test_mig0011_" + uuid.New().String()[:8]
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`)
	})

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	// Pin this connection's search_path for its whole life: every
	// unqualified table/function reference below — both this test's own
	// setup and the migration file's own SQL — resolves against the test
	// schema first, `public` second (needed only for gen_random_uuid(),
	// which lives in public's pgcrypto extension, not anything belonging
	// to the real application schema).
	if _, err := conn.Exec(ctx, `SET search_path = "`+schema+`", public`); err != nil {
		t.Fatalf("set search_path: %v", err)
	}

	setup := `
		CREATE TABLE agents (id uuid PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE rooms (id uuid PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE messages (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			mentioned_agent_id uuid REFERENCES agents(id)
		);
	`
	if _, err := conn.Exec(ctx, setup); err != nil {
		t.Fatalf("create pre-migration tables: %v", err)
	}

	var mentionedAgentID uuid.UUID
	if err := conn.QueryRow(ctx, `INSERT INTO agents DEFAULT VALUES RETURNING id`).Scan(&mentionedAgentID); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	var mentioningMessageID, plainMessageID uuid.UUID
	if err := conn.QueryRow(ctx,
		`INSERT INTO messages (mentioned_agent_id) VALUES ($1) RETURNING id`, mentionedAgentID,
	).Scan(&mentioningMessageID); err != nil {
		t.Fatalf("seed message with an existing mention: %v", err)
	}
	if err := conn.QueryRow(ctx,
		`INSERT INTO messages (mentioned_agent_id) VALUES (NULL) RETURNING id`,
	).Scan(&plainMessageID); err != nil {
		t.Fatalf("seed message with no mention: %v", err)
	}

	upSQL, err := os.ReadFile("0011_multi_agent_dynamics.up.sql")
	if err != nil {
		t.Fatalf("read migration file: %v", err)
	}

	// The real migration file, unmodified — exec'd via the simple query
	// protocol so its multiple semicolon-separated statements run as one
	// batch, exactly as `migrate up` would apply it.
	if _, err := conn.Conn().PgConn().Exec(ctx, string(upSQL)).ReadAll(); err != nil {
		t.Fatalf("apply 0011 up migration: %v", err)
	}

	rows, err := conn.Query(ctx, `SELECT agent_id FROM message_mentions WHERE message_id = $1`, mentioningMessageID)
	if err != nil {
		t.Fatalf("query message_mentions for the mentioning message: %v", err)
	}
	var gotAgentIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan message_mentions row: %v", err)
		}
		gotAgentIDs = append(gotAgentIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate message_mentions: %v", err)
	}
	if len(gotAgentIDs) != 1 || gotAgentIDs[0] != mentionedAgentID {
		t.Fatalf("message_mentions for the mentioning message = %v, want exactly [%s]", gotAgentIDs, mentionedAgentID)
	}

	var plainCount int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM message_mentions WHERE message_id = $1`, plainMessageID).Scan(&plainCount); err != nil {
		t.Fatalf("count message_mentions for the unmentioned message: %v", err)
	}
	if plainCount != 0 {
		t.Fatalf("message_mentions for the unmentioned message = %d rows, want 0", plainCount)
	}

	var mentionedAgentIDColumnExists bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = $1 AND table_name = 'messages' AND column_name = 'mentioned_agent_id'
		)
	`, schema).Scan(&mentionedAgentIDColumnExists); err != nil {
		t.Fatalf("check mentioned_agent_id column: %v", err)
	}
	if mentionedAgentIDColumnExists {
		t.Fatalf("messages.mentioned_agent_id still exists after migration 0011 — should have been dropped")
	}
}
