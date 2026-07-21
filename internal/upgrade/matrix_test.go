// SPDX-License-Identifier: Apache-2.0

package upgrade_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rabi-project/rabi/internal/store"
)

// TestMigrationMatrix restores each released tag's golden database at the schema
// version it shipped, migrates forward to the current schema, and asserts the
// data survived — no lost jobs, no corrupted phases, ledger intact. This is the
// "migration matrix green" acceptance for P2.M3.
func TestMigrationMatrix(t *testing.T) {
	for _, g := range loadManifest(t) {
		t.Run(g.Tag, func(t *testing.T) {
			ctx := context.Background()
			dsn := freshDB(t)

			// Restore the golden at the tag's schema version, then seed its data.
			old, err := store.OpenAt(ctx, dsn, g.Version)
			if err != nil {
				t.Fatalf("open at v%d: %v", g.Version, err)
			}
			execSeed(t, dsn, filepath.Join("testdata", g.Seed))

			beforeJobs := countRows(t, ctx, old, "jobs")
			beforeEvents := countRows(t, ctx, old, "job_events")
			beforeUsage := countRows(t, ctx, old, "usage_ledger")
			jobIDs := allJobIDs(t, ctx, old)
			old.Close()
			if beforeJobs == 0 {
				t.Fatalf("golden %s seeded no jobs", g.Tag)
			}

			// The upgrade: migrate forward to the current schema and serve.
			cur, err := store.Open(ctx, dsn)
			if err != nil {
				t.Fatalf("forward migration from v%d failed: %v", g.Version, err)
			}
			defer cur.Close()

			// Every seeded job must still be present, queryable, and in a valid
			// phase — nothing dropped or mangled by the migration.
			if got := countRows(t, ctx, cur, "jobs"); got != beforeJobs {
				t.Errorf("jobs count changed across migration: %d -> %d", beforeJobs, got)
			}
			if got := countRows(t, ctx, cur, "job_events"); got != beforeEvents {
				t.Errorf("job_events count changed: %d -> %d", beforeEvents, got)
			}
			if got := countRows(t, ctx, cur, "usage_ledger"); got != beforeUsage {
				t.Errorf("usage_ledger count changed: %d -> %d", beforeUsage, got)
			}
			for _, id := range jobIDs {
				rec, err := cur.GetJob(ctx, id)
				if err != nil {
					t.Errorf("job %s not queryable after migration: %v", id, err)
					continue
				}
				if !rec.Phase.Valid() {
					t.Errorf("job %s has invalid phase %q after migration", id, rec.Phase)
				}
			}
		})
	}
}

func countRows(t *testing.T, ctx context.Context, s *store.Store, table string) int64 {
	t.Helper()
	var n int64
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func allJobIDs(t *testing.T, ctx context.Context, s *store.Store) []string {
	t.Helper()
	rows, err := s.Pool.Query(ctx, "SELECT job_id FROM jobs ORDER BY created_at")
	if err != nil {
		t.Fatalf("list job ids: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}
