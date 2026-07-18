// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"strings"
	"testing"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/google/uuid"
	"github.com/rabi-project/rabi/internal/job"
	"github.com/rabi-project/rabi/internal/store"
)

// §3 accounting: append-only is enforced BY THE DATABASE — the serving role
// has no UPDATE/DELETE/TRUNCATE privilege on the ledger or audit tables, so
// even a future code bug cannot rewrite history.
func TestLedgerAppendOnlyEnforcedByGrants(t *testing.T) {
	ctx := t.Context()
	rec := shotsJob("ledger/grant", 10)
	if err := testStore.InsertJob(ctx, rec); err != nil {
		t.Fatal(err)
	}
	taskID := uuid.NewString()
	if err := testStore.RecordUsage(ctx, rec.JobID, taskID, rec.Tenant, "site/t1",
		map[string]float64{"shots": 10}); err != nil {
		t.Fatal(err)
	}

	for _, stmt := range []string{
		`UPDATE usage_ledger SET amount = 0 WHERE task_id = '` + taskID + `'`,
		`DELETE FROM usage_ledger WHERE task_id = '` + taskID + `'`,
		`TRUNCATE usage_ledger`,
		`UPDATE audit_log SET reason = 'rewritten'`,
		`DELETE FROM audit_log`,
		`UPDATE job_events SET phase = 'SUCCEEDED'`,
		`DELETE FROM job_events`,
		`UPDATE reconciliation_runs SET checked = 0`,
		`DELETE FROM reconciliation_runs`,
	} {
		_, err := testStore.Pool.Exec(ctx, stmt)
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%s: want permission denied at the DB layer, got %v", stmt, err)
		}
	}
}

// §3 accounting: reconciliation on a seeded workload finds zero mismatches,
// and a deliberately inconsistent job is caught.
func TestReconciliation(t *testing.T) {
	ctx := t.Context()

	// A consistent SUCCEEDED job: status usage matches its ledger rows.
	good := shotsJob("recon/ok", 100)
	if err := testStore.InsertJob(ctx, good); err != nil {
		t.Fatal(err)
	}
	goodTask := uuid.NewString()
	if _, err := testStore.BindJob(ctx, good.JobID, goodTask, "site/t1", map[string]any{"policy": "test"}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.RecordUsage(ctx, good.JobID, goodTask, good.Tenant, "site/t1",
		map[string]float64{"shots": 100, "seconds": 2.5}); err != nil {
		t.Fatal(err)
	}
	for _, next := range []job.Phase{job.Submitted, job.Running, job.Succeeded} {
		if _, err := testStore.TransitionJob(ctx, good.JobID, next, func(st map[string]any) map[string]any {
			if next == job.Succeeded {
				st["usage"] = []any{
					map[string]any{"unit": "seconds", "amount": 2.5},
					map[string]any{"unit": "shots", "amount": 100.0},
				}
			}
			return st
		}); err != nil {
			t.Fatal(err)
		}
	}

	checked, mismatches, err := testStore.ReconcileUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("reconciliation checked nothing")
	}
	for _, m := range mismatches {
		if m.JobID == good.JobID {
			t.Fatalf("consistent job flagged: %+v", m)
		}
	}
	base := len(mismatches)

	// A job whose status claims usage the ledger never saw must be flagged.
	bad := shotsJob("recon/bad", 50)
	if err := testStore.InsertJob(ctx, bad); err != nil {
		t.Fatal(err)
	}
	if _, err := testStore.BindJob(ctx, bad.JobID, uuid.NewString(), "site/t1", map[string]any{"policy": "test"}); err != nil {
		t.Fatal(err)
	}
	for _, next := range []job.Phase{job.Submitted, job.Running, job.Succeeded} {
		if _, err := testStore.TransitionJob(ctx, bad.JobID, next, func(st map[string]any) map[string]any {
			if next == job.Succeeded {
				st["usage"] = []any{map[string]any{"unit": "shots", "amount": 50.0}}
			}
			return st
		}); err != nil {
			t.Fatal(err)
		}
	}
	_, mismatches, err = testStore.ReconcileUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(mismatches) != base+1 {
		t.Fatalf("mismatches = %d, want %d (+1 for the phantom-usage job)", len(mismatches), base+1)
	}

	// The run history records both runs.
	checked2, mCount, _, ok, err := testStore.LastReconciliation(ctx)
	if err != nil || !ok || checked2 == 0 || mCount != base+1 {
		t.Fatalf("last reconciliation: checked=%d mismatches=%d ok=%v err=%v", checked2, mCount, ok, err)
	}

	// Ledger read order is stable (normalization input).
	entries, err := testStore.LedgerEntries(ctx, good.Tenant)
	if err != nil || len(entries) != 2 {
		t.Fatalf("ledger entries: %v %v", entries, err)
	}
	if entries[0].ID >= entries[1].ID {
		t.Fatal("ledger not in append order")
	}
	if _, err := store.OpenAt(ctx, testDSN, 6); err != nil {
		t.Fatalf("OpenAt current head: %v", err)
	}
}

// §3 deploy: N-1→N upgrade from the v0.1.0 schema (migrations 00001–00003).
// A golden Phase-0 database with jobs, a bound task, and ledger rows
// upgrades to head with data intact, tenants mapped, and the append-only
// grants active.
func TestV010GoldenDatabaseUpgrade(t *testing.T) {
	ctx := t.Context()
	pg, err := tcpostgres.Run(ctx, "postgres:15-alpine",
		tcpostgres.WithDatabase("rabi"), tcpostgres.WithUsername("rabi"),
		tcpostgres.WithPassword("rabi"), tcpostgres.BasicWaitStrategies())
	if err != nil {
		t.Fatalf("starting postgres: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}

	old, err := store.OpenAt(ctx, dsn, 3) // v0.1.0 head
	if err != nil {
		t.Fatal(err)
	}
	rec := shotsJob("golden/v010", 100)
	if err := old.InsertJob(ctx, rec); err != nil {
		t.Fatal(err)
	}
	taskID := uuid.NewString()
	if _, err := old.BindJob(ctx, rec.JobID, taskID, "site/t1", map[string]any{"policy": "static-best/v0"}); err != nil {
		t.Fatal(err)
	}
	if err := old.RecordUsage(ctx, rec.JobID, taskID, rec.Tenant, "site/t1",
		map[string]float64{"shots": 100}); err != nil {
		t.Fatal(err)
	}
	old.Close()

	// The N upgrade: a fresh binary boots with auto-migrate on.
	up, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("v0.1.0 -> head upgrade failed: %v", err)
	}
	defer up.Close()
	got, err := up.GetJob(ctx, rec.JobID)
	if err != nil || got.Tenant != "golden/v010" {
		t.Fatalf("job lost in upgrade: %v %+v", err, got)
	}
	if _, err := up.GetProject(ctx, "golden/v010"); err != nil {
		t.Fatalf("tenant not mapped to a project: %v", err)
	}
	entries, err := up.LedgerEntries(ctx, "golden/v010")
	if err != nil || len(entries) != 1 || entries[0].Amount != 100 {
		t.Fatalf("ledger lost in upgrade: %v %v", entries, err)
	}
	if _, err := up.Pool.Exec(ctx, `DELETE FROM usage_ledger`); err == nil {
		t.Fatal("append-only grants not active after upgrade")
	}

	// The auto-migrate gate: a lagging schema refuses to serve...
	if _, err := store.OpenNoMigrate(ctx, dsn); err != nil {
		t.Fatalf("current schema must serve with auto-migrate off: %v", err)
	}
	pg2, err := tcpostgres.Run(ctx, "postgres:15-alpine",
		tcpostgres.WithDatabase("rabi"), tcpostgres.WithUsername("rabi"),
		tcpostgres.WithPassword("rabi"), tcpostgres.BasicWaitStrategies())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg2.Terminate(context.Background()) })
	dsn2, err := pg2.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if lagged, err := store.OpenAt(ctx, dsn2, 3); err != nil {
		t.Fatal(err)
	} else {
		lagged.Close()
	}
	if _, err := store.OpenNoMigrate(ctx, dsn2); err == nil {
		t.Fatal("lagging schema must refuse to serve with auto-migrate off")
	}
}
