// SPDX-License-Identifier: Apache-2.0

package upgrade_test

import (
	"context"
	"sort"
	"testing"

	"github.com/rabi-project/rabi/internal/store"
)

// TestRollbackSafety proves the current schema is a strict superset of the last
// released schema (v0.4.x shipped migration 8): every table and column that
// existed then still exists now, nothing dropped or renamed. That additivity is
// what makes rollback safe on a single-node deployment — the N-1 binary keeps
// working against the N schema, so an operator rolls the binary back without a
// schema downgrade. (A full goose-down chain is deliberately not the rollback
// path: not every migration ships a Down, and re-migrating forward is the
// tested direction.)
func TestRollbackSafety(t *testing.T) {
	ctx := context.Background()

	const lastReleased = int64(8) // v0.4.x
	prevDSN := freshDB(t)
	prev, err := store.OpenAt(ctx, prevDSN, lastReleased)
	if err != nil {
		t.Fatalf("open at v%d: %v", lastReleased, err)
	}
	prevObjs := schemaObjects(t, ctx, prev)
	prev.Close()

	curDSN := freshDB(t)
	cur, err := store.Open(ctx, curDSN)
	if err != nil {
		t.Fatalf("open current: %v", err)
	}
	defer cur.Close()
	curObjs := schemaObjects(t, ctx, cur)

	curSet := make(map[string]bool, len(curObjs))
	for _, o := range curObjs {
		curSet[o] = true
	}
	var missing []string
	for _, o := range prevObjs {
		if !curSet[o] {
			missing = append(missing, o)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("schema is NOT additive — %d object(s) present at v%d were dropped/renamed, breaking rollback: %v",
			len(missing), lastReleased, missing)
	}
	if len(curObjs) <= len(prevObjs) {
		t.Errorf("expected the current schema to add objects over v%d (got %d vs %d)",
			lastReleased, len(curObjs), len(prevObjs))
	}
	t.Logf("additive: v%d had %d table.column objects, current has %d (superset)",
		lastReleased, len(prevObjs), len(curObjs))
}

// schemaObjects returns "table.column" for every user column in the public
// schema, excluding goose's bookkeeping table.
func schemaObjects(t *testing.T, ctx context.Context, s *store.Store) []string {
	t.Helper()
	rows, err := s.Pool.Query(ctx, `
		SELECT table_name, column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name <> 'goose_db_version'
		ORDER BY table_name, column_name`)
	if err != nil {
		t.Fatalf("introspect schema: %v", err)
	}
	defer rows.Close()
	var objs []string
	for rows.Next() {
		var table, col string
		if err := rows.Scan(&table, &col); err != nil {
			t.Fatal(err)
		}
		objs = append(objs, table+"."+col)
	}
	return objs
}
