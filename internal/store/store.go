// SPDX-License-Identifier: Apache-2.0

// Package store owns all Postgres access: connection pooling, embedded goose
// migrations, repositories, and (from M2) the FOR UPDATE SKIP LOCKED work
// queue. Postgres is the only stateful dependency in the system
// (mvp-build-plan.md §2).
package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store wraps the pgx pool shared by every control-plane component.
type Store struct {
	Pool *pgxpool.Pool
}

// Open connects to Postgres, retrying until the deadline so rabi tolerates
// compose startup ordering, and runs pending migrations.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store: parse database url: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for {
		err = pool.Ping(pingCtx)
		if err == nil {
			break
		}
		select {
		case <-pingCtx.Done():
			pool.Close()
			return nil, fmt.Errorf("store: postgres unreachable: %w", err)
		case <-time.After(time.Second):
		}
	}

	s := &Store{Pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	pool.Close()

	// Serve under the rabi_app role: it has no UPDATE/DELETE privilege on
	// the ledger/audit tables, so append-only is enforced by Postgres for
	// every query the server can ever run (M3). Migrations above ran with
	// the owner's rights.
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store: parse database url: %w", err)
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE rabi_app")
		return err
	}
	servePool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: open serving pool: %w", err)
	}
	if err := servePool.Ping(ctx); err != nil {
		servePool.Close()
		return nil, fmt.Errorf("store: serving role: %w", err)
	}
	s.Pool = servePool
	return s, nil
}

func (s *Store) migrate(ctx context.Context) error {
	db := stdlib.OpenDBFromPool(s.Pool)
	defer func() { _ = db.Close() }()

	migrations, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("store: scope migrations fs: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations)
	if err != nil {
		return fmt.Errorf("store: init migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("store: apply migrations: %w", err)
	}
	return nil
}

// Close releases the pool.
func (s *Store) Close() {
	s.Pool.Close()
}

// OpenAt connects WITHOUT auto-migrating and applies migrations only up to
// the given version. It exists for upgrade tests (seed a database at an old
// schema version, then migrate forward and assert the data migrations) and
// must not be used by the server, which always runs fully migrated.
func OpenAt(ctx context.Context, databaseURL string, version int64) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store: parse database url: %w", err)
	}
	s := &Store{Pool: pool}
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()
	migrations, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: scope migrations fs: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: init migrations: %w", err)
	}
	if _, err := provider.UpTo(ctx, version); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: apply migrations to %d: %w", version, err)
	}
	return s, nil
}
