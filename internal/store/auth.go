// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// TokenRecord is one row of api_tokens. TokenHash never leaves the store
// layer except for verification; the plaintext exists only at mint time.
type TokenRecord struct {
	ID         string
	Name       string
	Project    string
	Role       string
	TokenHash  string
	CreatedBy  string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// ErrTokenNotFound reports an unknown or revoked-and-purged token id.
var ErrTokenNotFound = errors.New("token not found")

// InsertToken stores a freshly minted token's metadata and hash.
func (s *Store) InsertToken(ctx context.Context, t *TokenRecord) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO api_tokens (id, name, project, role, token_hash, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		t.ID, t.Name, t.Project, t.Role, t.TokenHash, t.CreatedBy)
	if err != nil {
		return fmt.Errorf("insert token: %w", err)
	}
	return nil
}

// GetToken fetches a token row by id (revoked rows included — the caller
// decides how to treat revocation so it can audit precisely).
func (s *Store) GetToken(ctx context.Context, id string) (*TokenRecord, error) {
	row := s.Pool.QueryRow(ctx, `
		SELECT id, name, project, role, token_hash, created_by, created_at, last_used_at, revoked_at
		FROM api_tokens WHERE id = $1`, id)
	var t TokenRecord
	err := row.Scan(&t.ID, &t.Name, &t.Project, &t.Role, &t.TokenHash,
		&t.CreatedBy, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTokenNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get token: %w", err)
	}
	return &t, nil
}

// ListTokens returns token metadata, newest first, optionally filtered by
// project. Hashes are cleared: inventory never needs them.
func (s *Store) ListTokens(ctx context.Context, project string) ([]*TokenRecord, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, name, project, role, created_by, created_at, last_used_at, revoked_at
		FROM api_tokens
		WHERE $1 = '' OR project = $1
		ORDER BY created_at DESC`, project)
	if err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	defer rows.Close()
	var out []*TokenRecord
	for rows.Next() {
		var t TokenRecord
		if err := rows.Scan(&t.ID, &t.Name, &t.Project, &t.Role,
			&t.CreatedBy, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt); err != nil {
			return nil, fmt.Errorf("scan token: %w", err)
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

// RevokeToken marks a token revoked. Idempotent; reports whether the token
// existed.
func (s *Store) RevokeToken(ctx context.Context, id string) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE api_tokens SET revoked_at = COALESCE(revoked_at, now())
		WHERE id = $1`, id)
	if err != nil {
		return false, fmt.Errorf("revoke token: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// TouchToken records use for inventory ("last_used_at"), best-effort.
func (s *Store) TouchToken(ctx context.Context, id string) {
	_, _ = s.Pool.Exec(ctx, `UPDATE api_tokens SET last_used_at = now() WHERE id = $1`, id)
}

// AuditEntry is one auth decision worth keeping: every deny, every admin
// action (phase1-build-plan.md M1).
type AuditEntry struct {
	PrincipalType string
	Subject       string
	PrincipalName string
	Role          string
	Method        string
	Decision      string // "allow" | "deny"
	Reason        string
}

// RecordAudit appends to the audit log. There is deliberately no update or
// delete method for audit_log anywhere in this package.
func (s *Store) RecordAudit(ctx context.Context, e AuditEntry) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO audit_log (principal_type, subject, principal_name, role, method, decision, reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		e.PrincipalType, e.Subject, e.PrincipalName, e.Role, e.Method, e.Decision, e.Reason)
	if err != nil {
		return fmt.Errorf("record audit: %w", err)
	}
	return nil
}

// AuditEntries returns recent audit rows (newest first) for tests and the
// eventual console; filter by decision ("" = all).
func (s *Store) AuditEntries(ctx context.Context, decision string, limit int) ([]AuditEntry, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT principal_type, subject, principal_name, role, method, decision, reason
		FROM audit_log
		WHERE $1 = '' OR decision = $1
		ORDER BY id DESC LIMIT $2`, decision, limit)
	if err != nil {
		return nil, fmt.Errorf("audit entries: %w", err)
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.PrincipalType, &e.Subject, &e.PrincipalName,
			&e.Role, &e.Method, &e.Decision, &e.Reason); err != nil {
			return nil, fmt.Errorf("scan audit: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
