// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// SessionRecord is one control-plane session.
type SessionRecord struct {
	SessionID        string
	Tenant           string
	Target           string
	AdapterSessionID string
	OpenedByJob      string
	ExpiresAt        *time.Time
	ClosedAt         *time.Time
	CreatedAt        time.Time
}

// ErrSessionNotFound reports an unknown session id.
var ErrSessionNotFound = errors.New("session not found")

// Live reports whether the session can still accept tasks at now.
func (s *SessionRecord) Live(now time.Time) bool {
	if s.ClosedAt != nil {
		return false
	}
	return s.ExpiresAt == nil || now.Before(*s.ExpiresAt)
}

// InsertSession stores a freshly opened session.
func (s *Store) InsertSession(ctx context.Context, rec *SessionRecord) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO sessions (session_id, tenant, target, adapter_session_id, opened_by_job, expires_at, closed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		rec.SessionID, rec.Tenant, rec.Target, rec.AdapterSessionID, rec.OpenedByJob, rec.ExpiresAt, rec.ClosedAt)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

// GetSession fetches one session.
func (s *Store) GetSession(ctx context.Context, id string) (*SessionRecord, error) {
	row := s.Pool.QueryRow(ctx, `
		SELECT session_id, tenant, target, adapter_session_id, opened_by_job, expires_at, closed_at, created_at
		FROM sessions WHERE session_id = $1`, id)
	var rec SessionRecord
	err := row.Scan(&rec.SessionID, &rec.Tenant, &rec.Target, &rec.AdapterSessionID,
		&rec.OpenedByJob, &rec.ExpiresAt, &rec.ClosedAt, &rec.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	return &rec, nil
}

// CloseSession marks a session closed (idempotent).
func (s *Store) CloseSession(ctx context.Context, id string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE sessions SET closed_at = COALESCE(closed_at, now()) WHERE session_id = $1`, id)
	if err != nil {
		return fmt.Errorf("close session: %w", err)
	}
	return nil
}
