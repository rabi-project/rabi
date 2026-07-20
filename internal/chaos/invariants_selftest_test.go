// SPDX-License-Identifier: Apache-2.0

// Self-test: the invariant suite must CATCH planted violations, not just pass
// on healthy stacks. A chaos harness whose invariants can't fail is theater.
package chaos_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/rabi-project/rabi/internal/chaos"
	"github.com/rabi-project/rabi/internal/job"
	"github.com/rabi-project/rabi/internal/store"
)

func seedJob(t *testing.T, tenant string, phase job.Phase) string {
	t.Helper()
	id := uuid.NewString()
	rec := &store.JobRecord{
		JobID: id, Tenant: tenant, Name: "selftest", Phase: phase,
		Doc:    map[string]any{"spec": map[string]any{}},
		Status: map[string]any{"phase": string(phase)},
	}
	if err := testStore.InsertJob(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	return id
}

func hasInvariant(vs []chaos.Violation, name string) bool {
	for _, v := range vs {
		if v.Invariant == name {
			return true
		}
	}
	return false
}

func TestSelfTest_InvariantsCatchPlantedViolations(t *testing.T) {
	ctx := t.Context()

	// Control: a clean freshly-inserted job trips nothing.
	clean := seedJob(t, "self/clean", job.Pending)
	if vs := chaos.CheckAll(ctx, testStore, []string{clean}, nil); len(vs) != 0 {
		t.Fatalf("clean job produced violations: %v", vs)
	}

	// Plant 1: usage recorded far above the declared cap → usage-within-caps
	// must fire. (A true (task_id, unit) duplicate is structurally blocked by the
	// ledger's UNIQUE constraint, so the detectable leak is over-cap usage.)
	owner := testStore.Pool
	dup := seedJob(t, "self/dup", job.Succeeded)
	if _, err := owner.Exec(ctx, `
		INSERT INTO usage_ledger (job_id, task_id, tenant, target, unit, amount)
		VALUES ($1, $2, 'self/dup', 'sim/t', 'shots', 999999)`, dup, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	vs := chaos.CheckAll(ctx, testStore, []string{dup}, map[string]float64{dup: 100})
	if !hasInvariant(vs, "usage-within-caps") {
		t.Errorf("over-cap usage not caught: %v", vs)
	}

	// Plant 2: an event AFTER a terminal transition → terminal-immutable fires.
	term := seedJob(t, "self/term", job.Pending)
	// PENDING event already exists from InsertJob; add SUCCEEDED then a bogus later event.
	for _, ph := range []string{"SUCCEEDED", "RUNNING"} {
		if _, err := owner.Exec(ctx, `
			INSERT INTO job_events (job_id, phase, status) VALUES ($1, $2, '{}')`, term, ph); err != nil {
			t.Fatal(err)
		}
	}
	vs = chaos.CheckAll(ctx, testStore, []string{term}, nil)
	if !hasInvariant(vs, "terminal-immutable") {
		t.Errorf("post-terminal event not caught: %v", vs)
	}
	// the same planted chain (SUCCEEDED then RUNNING) is also an illegal
	// transition → audit-gapless must fire.
	if !hasInvariant(vs, "audit-gapless") {
		t.Errorf("illegal transition not caught: %v", vs)
	}

	// Plant 3: a "lost" job — reference an id that was never stored.
	vs = chaos.CheckAll(ctx, testStore, []string{"never-existed-" + uuid.NewString()}, nil)
	if !hasInvariant(vs, "no-job-lost") {
		t.Errorf("missing job not caught: %v", vs)
	}
	if !strings.Contains(vs[0].String(), "no-job-lost") {
		t.Errorf("violation string malformed: %s", vs[0])
	}
	_ = context.Background
}
