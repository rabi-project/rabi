// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"fmt"
	"time"
)

// ShadowPlacement is one candidate-policy decision recorded but never executed
// (phase2-build-plan.md P2.M5). ESP/Wait are pointers because a policy may find
// no feasible target, in which case the proxy is unknown (NULL).
type ShadowPlacement struct {
	At           time.Time
	JobID        string
	Tenant       string
	Policy       string // candidate/shadow policy
	ActivePolicy string
	ActiveTarget string
	ShadowTarget string
	Agreed       bool
	ActiveESP    *float64
	ShadowESP    *float64
	ActiveWait   *float64
	ShadowWait   *float64
}

// RecordShadowPlacement appends one shadow decision. Append-only: a record of
// what a candidate policy would have computed, never rewritten.
func (s *Store) RecordShadowPlacement(ctx context.Context, p ShadowPlacement) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO shadow_placements
		  (job_id, tenant, policy, active_policy, active_target, shadow_target,
		   agreed, active_esp, shadow_esp, active_wait, shadow_wait)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		p.JobID, p.Tenant, p.Policy, p.ActivePolicy, p.ActiveTarget, p.ShadowTarget,
		p.Agreed, p.ActiveESP, p.ShadowESP, p.ActiveWait, p.ShadowWait)
	if err != nil {
		return fmt.Errorf("record shadow placement: %w", err)
	}
	return nil
}

// ShadowPlacementsSince returns a candidate policy's shadow records at or after
// `since`, newest first — the input to the comparison report.
func (s *Store) ShadowPlacementsSince(ctx context.Context, policy string, since time.Time) ([]ShadowPlacement, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT at, job_id, tenant, policy, active_policy, active_target, shadow_target,
		       agreed, active_esp, shadow_esp, active_wait, shadow_wait
		FROM shadow_placements
		WHERE policy = $1 AND at >= $2
		ORDER BY at DESC`, policy, since)
	if err != nil {
		return nil, fmt.Errorf("query shadow placements: %w", err)
	}
	defer rows.Close()
	var out []ShadowPlacement
	for rows.Next() {
		var p ShadowPlacement
		if err := rows.Scan(&p.At, &p.JobID, &p.Tenant, &p.Policy, &p.ActivePolicy,
			&p.ActiveTarget, &p.ShadowTarget, &p.Agreed,
			&p.ActiveESP, &p.ShadowESP, &p.ActiveWait, &p.ShadowWait); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ShadowPolicies lists the distinct candidate policies with recorded placements.
func (s *Store) ShadowPolicies(ctx context.Context) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT policy FROM shadow_placements ORDER BY policy`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
