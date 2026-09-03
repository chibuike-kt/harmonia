// Package store wires the shared pgxpool.Pool and redis.Client used by
// every other internal package. Postgres is the source of truth; Redis
// holds only ephemeral state (leases, presence) and is never queried as
// authoritative (design doc section 3).
package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Store struct {
	Pool  *pgxpool.Pool
	Redis *redis.Client
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
