// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"fmt"
	"time"
)

// GameDay is one recorded chaos drill (phase2-build-plan.md P2.M1/M7).
type GameDay struct {
	StartedAt       time.Time
	FinishedAt      time.Time
	Scenario        string
	Target          string
	InvariantsGreen bool
	Violations      int
	Operator        string
	Note            string
}

// RecordGameDay appends one finalized drill record. The row is written once,
// complete — drills are never rewritten after they ran.
func (s *Store) RecordGameDay(ctx context.Context, g GameDay) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO game_days
		  (started_at, finished_at, scenario, target, invariants_green, violations, operator, note)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		g.StartedAt, g.FinishedAt, g.Scenario, g.Target, g.InvariantsGreen, g.Violations, g.Operator, g.Note)
	if err != nil {
		return fmt.Errorf("record game day: %w", err)
	}
	return nil
}

// LastGameDay returns the most recent drill, or ok=false if none has run. The
// status page reads this for "last game-day date and result".
func (s *Store) LastGameDay(ctx context.Context) (g GameDay, ok bool, err error) {
	err = s.Pool.QueryRow(ctx, `
		SELECT started_at, finished_at, scenario, target, invariants_green, violations, operator, note
		FROM game_days ORDER BY started_at DESC LIMIT 1`).Scan(
		&g.StartedAt, &g.FinishedAt, &g.Scenario, &g.Target, &g.InvariantsGreen, &g.Violations, &g.Operator, &g.Note)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return GameDay{}, false, nil
		}
		return GameDay{}, false, fmt.Errorf("last game day: %w", err)
	}
	return g, true, nil
}
