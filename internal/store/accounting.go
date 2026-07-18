// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// LedgerEntry is one immutable usage row, in ledger order.
type LedgerEntry struct {
	ID     int64
	JobID  string
	TaskID string
	Tenant string
	Target string
	Unit   string
	Amount float64
}

// LedgerEntries reads ledger rows in id (append) order — the deterministic
// input to normalization ("" tenant = all).
func (s *Store) LedgerEntries(ctx context.Context, tenant string) ([]LedgerEntry, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, job_id, task_id, tenant, target, unit, amount
		FROM usage_ledger WHERE $1 = '' OR tenant = $1 ORDER BY id`, tenant)
	if err != nil {
		return nil, fmt.Errorf("ledger entries: %w", err)
	}
	defer rows.Close()
	var out []LedgerEntry
	for rows.Next() {
		var e LedgerEntry
		if err := rows.Scan(&e.ID, &e.JobID, &e.TaskID, &e.Tenant, &e.Target, &e.Unit, &e.Amount); err != nil {
			return nil, fmt.Errorf("scan ledger: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// UsageMismatch is one job×unit where the job's status usage disagrees with
// the ledger.
type UsageMismatch struct {
	JobID  string  `json:"jobId"`
	Unit   string  `json:"unit"`
	Status float64 `json:"status"`
	Ledger float64 `json:"ledger"`
}

// ReconcileUsage checks Σ ledger == per-job status usage for every
// SUCCEEDED job and records the run. Zero mismatches is the healthy state
// (test-and-verification-plan.md §3 accounting row).
func (s *Store) ReconcileUsage(ctx context.Context) (checked int64, mismatches []UsageMismatch, err error) {
	if err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM jobs WHERE phase = 'SUCCEEDED'`).Scan(&checked); err != nil {
		return 0, nil, fmt.Errorf("reconcile count: %w", err)
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT j.job_id, u.unit, u.amount, COALESCE(l.total, 0)
		FROM jobs j
		CROSS JOIN LATERAL jsonb_to_recordset(j.status->'usage')
		     AS u(unit text, amount double precision)
		LEFT JOIN LATERAL (
		    SELECT SUM(amount) AS total FROM usage_ledger
		    WHERE job_id = j.job_id AND unit = u.unit
		) l ON true
		WHERE j.phase = 'SUCCEEDED' AND u.amount IS DISTINCT FROM COALESCE(l.total, 0)
		ORDER BY j.job_id, u.unit`)
	if err != nil {
		return 0, nil, fmt.Errorf("reconcile scan: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var m UsageMismatch
		if err := rows.Scan(&m.JobID, &m.Unit, &m.Status, &m.Ledger); err != nil {
			return 0, nil, fmt.Errorf("scan mismatch: %w", err)
		}
		mismatches = append(mismatches, m)
	}
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}

	raw, err := json.Marshal(mismatches)
	if err != nil {
		return 0, nil, fmt.Errorf("marshal mismatches: %w", err)
	}
	if raw == nil || string(raw) == "null" {
		raw = []byte("[]")
	}
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO reconciliation_runs (checked, mismatches) VALUES ($1, $2)`,
		checked, raw); err != nil {
		return 0, nil, fmt.Errorf("record reconciliation: %w", err)
	}
	return checked, mismatches, nil
}

// LastReconciliation returns the most recent run (checked, mismatch count,
// time), or ok=false when none has run yet.
func (s *Store) LastReconciliation(ctx context.Context) (checked int64, mismatchCount int, at time.Time, ok bool, err error) {
	err = s.Pool.QueryRow(ctx, `
		SELECT checked, jsonb_array_length(mismatches), at
		FROM reconciliation_runs ORDER BY id DESC LIMIT 1`).Scan(&checked, &mismatchCount, &at)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return 0, 0, time.Time{}, false, nil
		}
		return 0, 0, time.Time{}, false, fmt.Errorf("last reconciliation: %w", err)
	}
	return checked, mismatchCount, at, true, nil
}
