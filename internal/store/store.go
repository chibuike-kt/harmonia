// Package store wires the shared pgxpool.Pool and redis.Client used by
// every other internal package. Postgres is the source of truth; Redis
// holds only ephemeral state (leases, presence) and is never queried as
// authoritative (design doc section 3).
package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Store struct {
	Pool  *pgxpool.Pool
	Redis *redis.Client
}

// Querier is the subset of *pgxpool.Pool and pgx.Tx that a domain Store
// needs to run queries. Accepting it instead of *pgxpool.Pool lets a Store
// work unmodified whether it's bound to the pool directly or to a
// transaction started on it — a handler wraps a write and its event
// record in one by constructing a Store around a pgx.Tx instead of Pool.
type Querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Tx is the subset of pgx.Tx a handler needs to run its writes and finish
// the transaction: Querier plus Commit/Rollback. Smaller than pgx.Tx so a
// fake can satisfy it in tests without a live Postgres connection.
type Tx interface {
	Querier
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// Beginner starts a transaction. A test can supply its own fake in place
// of PoolBeginner to exercise commit/rollback logic without live Postgres.
type Beginner interface {
	Begin(ctx context.Context) (Tx, error)
}

// PoolBeginner adapts *pgxpool.Pool to Beginner. A plain method value
// wouldn't do — (*pgxpool.Pool).Begin returns pgx.Tx, not the narrower Tx
// here, so its signature doesn't satisfy Beginner directly even though a
// pgx.Tx value always satisfies Tx.
type PoolBeginner struct {
	Pool *pgxpool.Pool
}

func (b PoolBeginner) Begin(ctx context.Context) (Tx, error) {
	tx, err := b.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

func New(ctx context.Context, databaseURL, redisAddr string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	return &Store{Pool: pool, Redis: rdb}, nil
}

func (s *Store) Close() {
	s.Pool.Close()
	_ = s.Redis.Close()
}
