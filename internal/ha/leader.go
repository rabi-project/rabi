// SPDX-License-Identifier: Apache-2.0

// Package ha holds the pilot-INDEPENDENT high-availability mechanics
// (phase2-build-plan.md P2.M8+): advisory-lock leader election. It is a
// performance optimization — the row-locked binder already makes double-binding
// impossible, so multiple schedulers are safe but wasteful; leader election
// lets a standby stay idle until the leader dies. No new infrastructure: the
// lock is a Postgres session-level advisory lock. Deployment topology, managed-
// vs-local Postgres posture, and site-shaped failover expectations are
// deliberately OUT of scope here — those consume pilot-gate fields (Wave B).
package ha

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// DefaultLockKey is the advisory-lock key for the scheduler leadership. A single
// process across the deployment holds it at a time.
const DefaultLockKey int64 = 0x7261626921 // "rabi!"

// Elector campaigns for scheduler leadership on a dedicated Postgres session. It
// holds a session-level advisory lock while leader; if its process dies, the
// session ends and Postgres releases the lock automatically, so a standby can
// take over — that is the failover path.
type Elector struct {
	key      int64
	interval time.Duration
	logger   *slog.Logger

	conn *pgx.Conn
	mu   sync.RWMutex
	is   bool
}

// NewElector opens the dedicated leadership connection. Close is via cancelling
// Campaign's context.
func NewElector(ctx context.Context, dsn string, key int64, interval time.Duration, logger *slog.Logger) (*Elector, error) {
	if key == 0 {
		key = DefaultLockKey
	}
	if interval <= 0 {
		interval = time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("ha: leadership connection: %w", err)
	}
	return &Elector{key: key, interval: interval, logger: logger, conn: conn}, nil
}

// IsLeader reports whether this process currently holds leadership.
func (e *Elector) IsLeader() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.is
}

func (e *Elector) set(v bool) {
	e.mu.Lock()
	changed := e.is != v
	e.is = v
	e.mu.Unlock()
	if changed {
		if v {
			e.logger.Info("acquired scheduler leadership")
		} else {
			e.logger.Warn("lost scheduler leadership")
		}
	}
}

// Campaign runs until ctx is cancelled: it tries to acquire leadership when it
// doesn't hold it, and verifies the connection while it does. On exit it
// releases the lock and closes the connection.
func (e *Elector) Campaign(ctx context.Context) {
	defer e.release()
	e.tryAcquire(ctx)
	t := time.NewTicker(e.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if e.IsLeader() {
				// Detect a dropped session (lost lock) proactively.
				if err := e.conn.Ping(ctx); err != nil {
					e.set(false)
				}
			} else {
				e.tryAcquire(ctx)
			}
		}
	}
}

func (e *Elector) tryAcquire(ctx context.Context) {
	var ok bool
	if err := e.conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", e.key).Scan(&ok); err != nil {
		e.set(false)
		return
	}
	e.set(ok)
}

func (e *Elector) release() {
	if e.IsLeader() {
		_, _ = e.conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", e.key)
	}
	_ = e.conn.Close(context.Background())
	e.set(false)
}
