// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"strings"
	"testing"

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
