// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ProjectRecord is one row of projects. Tenant is the exact wire string the
// spec API speaks; Org/Name are derived display fields.
type ProjectRecord struct {
	Tenant     string
	Org        string
	Name       string
	Weight     int
	CreatedAt  time.Time
	ArchivedAt *time.Time
}

// QuotaRecord is a per-project limit in one native unit.
type QuotaRecord struct {
	Tenant string
	Unit   string
	Limit  float64
}

// ErrProjectNotFound reports an unknown project tenant string.
var ErrProjectNotFound = errors.New("project not found")

// ErrProjectArchived rejects submissions into archived projects.
var ErrProjectArchived = errors.New("project is archived")

// ErrQuotaExceeded rejects a submission that would overrun a quota; the
// message carries the unit and amounts for the client.
type ErrQuotaExceeded struct {
	Unit      string
	Limit     float64
	Committed float64
	Requested float64
}

func (e *ErrQuotaExceeded) Error() string {
	return fmt.Sprintf("quota exceeded for unit %q: limit %.0f, committed %.0f, requested %.0f",
		e.Unit, e.Limit, e.Committed, e.Requested)
}

// splitTenant derives display org/name from a wire tenant string.
func splitTenant(tenant string) (org, name string) {
	if org, name, ok := strings.Cut(tenant, "/"); ok {
		return org, name
	}
	return tenant, "default"
}

// EnsureProject returns the project for a tenant string, creating it on
// first use (Phase 0 accepted arbitrary tenant strings; strictness is a
// deployment policy for later — docs/decisions.md D-036).
func (s *Store) EnsureProject(ctx context.Context, tenant string) (*ProjectRecord, error) {
	if tenant == "" {
		return nil, fmt.Errorf("empty tenant")
	}
	org, name := splitTenant(tenant)
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO projects (tenant, org, name) VALUES ($1, $2, $3)
		ON CONFLICT (tenant) DO NOTHING`, tenant, org, name)
	if err != nil {
		return nil, fmt.Errorf("ensure project: %w", err)
	}
	return s.GetProject(ctx, tenant)
}

// GetProject fetches one project by tenant string.
func (s *Store) GetProject(ctx context.Context, tenant string) (*ProjectRecord, error) {
	row := s.Pool.QueryRow(ctx, `
		SELECT tenant, org, name, weight, created_at, archived_at
		FROM projects WHERE tenant = $1`, tenant)
	var p ProjectRecord
	err := row.Scan(&p.Tenant, &p.Org, &p.Name, &p.Weight, &p.CreatedAt, &p.ArchivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrProjectNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get project: %w", err)
	}
	return &p, nil
}

// ListProjects returns projects ordered by tenant string.
func (s *Store) ListProjects(ctx context.Context, includeArchived bool) ([]*ProjectRecord, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT tenant, org, name, weight, created_at, archived_at
		FROM projects WHERE $1 OR archived_at IS NULL ORDER BY tenant`, includeArchived)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()
	var out []*ProjectRecord
	for rows.Next() {
		var p ProjectRecord
		if err := rows.Scan(&p.Tenant, &p.Org, &p.Name, &p.Weight, &p.CreatedAt, &p.ArchivedAt); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// ArchiveProject marks a project archived (idempotent). Archived projects
// refuse new submissions; existing jobs and history are untouched.
func (s *Store) ArchiveProject(ctx context.Context, tenant string) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE projects SET archived_at = COALESCE(archived_at, now())
		WHERE tenant = $1`, tenant)
	if err != nil {
		return false, fmt.Errorf("archive project: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// SetProjectWeight sets the fair-share weight (>= 1).
func (s *Store) SetProjectWeight(ctx context.Context, tenant string, weight int) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE projects SET weight = $2 WHERE tenant = $1`, tenant, weight)
	if err != nil {
		return fmt.Errorf("set weight: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrProjectNotFound
	}
	return nil
}

// ProjectWeights returns fair-share weights for the given tenants; tenants
// without a project row default to weight 1.
func (s *Store) ProjectWeights(ctx context.Context, tenants []string) (map[string]int, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT tenant, weight FROM projects WHERE tenant = ANY($1)`, tenants)
	if err != nil {
		return nil, fmt.Errorf("project weights: %w", err)
	}
	defer rows.Close()
	out := make(map[string]int, len(tenants))
	for _, t := range tenants {
		out[t] = 1
	}
	for rows.Next() {
		var tenant string
		var weight int
		if err := rows.Scan(&tenant, &weight); err != nil {
			return nil, fmt.Errorf("scan weight: %w", err)
		}
		out[tenant] = weight
	}
	return out, rows.Err()
}

// SetQuota sets (or with limit < 0 removes) a per-unit quota.
func (s *Store) SetQuota(ctx context.Context, tenant, unit string, limit float64) error {
	if limit < 0 {
		_, err := s.Pool.Exec(ctx, `
			DELETE FROM project_quotas WHERE tenant = $1 AND unit = $2`, tenant, unit)
		if err != nil {
			return fmt.Errorf("remove quota: %w", err)
		}
		return nil
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO project_quotas (tenant, unit, limit_amount) VALUES ($1, $2, $3)
		ON CONFLICT (tenant, unit) DO UPDATE SET limit_amount = EXCLUDED.limit_amount`,
		tenant, unit, limit)
	if err != nil {
		return fmt.Errorf("set quota: %w", err)
	}
	return nil
}

// ListQuotas returns the quotas for one project ("" = all projects).
func (s *Store) ListQuotas(ctx context.Context, tenant string) ([]QuotaRecord, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT tenant, unit, limit_amount FROM project_quotas
		WHERE $1 = '' OR tenant = $1 ORDER BY tenant, unit`, tenant)
	if err != nil {
		return nil, fmt.Errorf("list quotas: %w", err)
	}
	defer rows.Close()
	var out []QuotaRecord
	for rows.Next() {
		var q QuotaRecord
		if err := rows.Scan(&q.Tenant, &q.Unit, &q.Limit); err != nil {
			return nil, fmt.Errorf("scan quota: %w", err)
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// InsertJobWithQuota inserts a PENDING job after checking the project's
// quotas, all in ONE transaction with the quota rows locked. The lock
// serializes concurrent submissions of the same project, so N concurrent
// requests against a nearly-exhausted quota admit exactly the affordable
// number (test-and-verification-plan.md §3 race criterion).
//
// costs declares the submission's native-unit demand (e.g. shots). Committed
// usage per unit = recorded ledger usage + declared demand of non-terminal
// jobs (terminal jobs bill via the ledger only, so nothing double-counts).
// Units without a quota row are unlimited.
func (s *Store) InsertJobWithQuota(ctx context.Context, rec *JobRecord, costs map[string]float64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin quota insert: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for unit, requested := range costs {
		var limit float64
		err := tx.QueryRow(ctx, `
			SELECT limit_amount FROM project_quotas
			WHERE tenant = $1 AND unit = $2 FOR UPDATE`, rec.Tenant, unit).Scan(&limit)
		if errors.Is(err, pgx.ErrNoRows) {
			continue // no quota on this unit
		}
		if err != nil {
			return fmt.Errorf("store: lock quota %s/%s: %w", rec.Tenant, unit, err)
		}
		var committed float64
		err = tx.QueryRow(ctx, `
			SELECT COALESCE((SELECT SUM(amount) FROM usage_ledger
			                 WHERE tenant = $1 AND unit = $2), 0)
			     + COALESCE((SELECT SUM(declared_cost(doc, $2)) FROM jobs
			                 WHERE tenant = $1
			                   AND phase NOT IN ('SUCCEEDED','FAILED','CANCELLED')), 0)`,
			rec.Tenant, unit).Scan(&committed)
		if err != nil {
			return fmt.Errorf("store: committed usage %s/%s: %w", rec.Tenant, unit, err)
		}
		if committed+requested > limit {
			return &ErrQuotaExceeded{Unit: unit, Limit: limit, Committed: committed, Requested: requested}
		}
	}

	if err := insertJobTx(ctx, tx, rec); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit quota insert: %w", err)
	}
	return nil
}
