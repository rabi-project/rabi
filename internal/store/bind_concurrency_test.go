// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/rabi-project/rabi/internal/job"
	"github.com/rabi-project/rabi/internal/store"
)

// TestBindJob_NoDoubleBindUnderConcurrency proves replica-safe binding
// (phase2-build-plan.md P2.M8+): even with 100 schedulers racing to bind the
// SAME job, exactly one wins — the row-locked binder (FOR UPDATE SKIP LOCKED +
// PENDING check) makes double-binding impossible. This is the correctness
// property HA leader election is a performance optimization over, not a
// prerequisite for.
func TestBindJob_NoDoubleBindUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	id := uuid.NewString()
	rec := &store.JobRecord{
		JobID: id, Tenant: "acme/qa", Name: "race", Phase: job.Pending,
		Doc:    map[string]any{"spec": map[string]any{}},
		Status: map[string]any{"phase": "PENDING", "conditions": []any{}},
	}
	if err := testStore.InsertJob(ctx, rec); err != nil {
		t.Fatal(err)
	}

	const N = 100
	var wg sync.WaitGroup
	var successes atomic.Int64
	start := make(chan struct{})
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // all fire at once
			if _, err := testStore.BindJob(ctx, id, uuid.NewString(), "sim/t", map[string]any{"policy": "test"}); err == nil {
				successes.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("expected exactly 1 successful bind under %d-way concurrency, got %d", N, got)
	}
	var tasks int64
	if err := testStore.Pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE job_id = $1`, id).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if tasks != 1 {
		t.Fatalf("expected exactly 1 task row, got %d — double bind!", tasks)
	}
	got, err := testStore.GetJob(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != job.Scheduled {
		t.Fatalf("bound job phase = %s, want SCHEDULED", got.Phase)
	}
}
