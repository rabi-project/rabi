// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mAengo31/rabi/internal/job"
	"github.com/mAengo31/rabi/internal/store"
)

func TestBindJobLifecycle(t *testing.T) {
	ctx := t.Context()
	rec := newRec("88888888-8888-8888-8888-888888888801", "acme", "bind-me")
	if err := testStore.InsertJob(ctx, rec); err != nil {
		t.Fatal(err)
	}

	placement := map[string]any{"policy": "direct/v0", "reason": "test"}
	bound, err := testStore.BindJob(ctx, rec.JobID,
		"88888888-8888-8888-8888-888888888802", "sim/alpha", placement)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if bound.Phase != job.Scheduled || bound.Status["boundTarget"] != "sim/alpha" {
		t.Fatalf("bind state: %+v", bound)
	}

	// A second bind must refuse: no longer PENDING.
	_, err = testStore.BindJob(ctx, rec.JobID, "88888888-8888-8888-8888-888888888803", "sim/alpha", placement)
	if err == nil || !strings.Contains(err.Error(), "not PENDING") {
		t.Fatalf("double bind: %v", err)
	}

	// Unknown job.
	_, err = testStore.BindJob(ctx, "99999999-9999-9999-9999-999999999998",
		"88888888-8888-8888-8888-888888888804", "sim/alpha", placement)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("bind unknown job: %v", err)
	}

	// The bound task is visible as active and via TaskForJob.
	active, err := testStore.ActiveTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tk := range active {
		if tk.JobID == rec.JobID && tk.State == "QUEUED" {
			found = true
		}
	}
	if !found {
		t.Fatalf("bound task not in ActiveTasks: %+v", active)
	}
	tk, err := testStore.TaskForJob(ctx, rec.JobID)
	if err != nil || tk.Target != "sim/alpha" {
		t.Fatalf("TaskForJob: %+v %v", tk, err)
	}

	// UpdateTask progresses adapter state; error/result round-trip.
	if err := testStore.UpdateTask(ctx, tk.TaskID, "adapter-42", "FAILED",
		map[string]any{"category": "VENDOR_ERROR"}, map[string]any{"partial": true}); err != nil {
		t.Fatal(err)
	}
	tk, err = testStore.TaskForJob(ctx, rec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if tk.AdapterTaskID != "adapter-42" || tk.State != "FAILED" ||
		tk.Error["category"] != "VENDOR_ERROR" || tk.Result["partial"] != true {
		t.Fatalf("task round trip: %+v", tk)
	}

	if err := testStore.UpdateTask(ctx, "99999999-9999-9999-9999-999999999997", "", "RUNNING", nil, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update unknown task: %v", err)
	}
	if _, err := testStore.TaskForJob(ctx, "99999999-9999-9999-9999-999999999996"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("task for unknown job: %v", err)
	}
}

func TestRecordUsageIdempotent(t *testing.T) {
	ctx := t.Context()
	rec := newRec("88888888-8888-8888-8888-888888888810", "usage-tenant", "usage-job")
	if err := testStore.InsertJob(ctx, rec); err != nil {
		t.Fatal(err)
	}
	taskID := "88888888-8888-8888-8888-888888888811"
	usage := map[string]float64{"shots": 1000, "tasks": 1}
	for range 3 { // repeated recording must not duplicate
		if err := testStore.RecordUsage(ctx, rec.JobID, taskID, "usage-tenant", "sim/alpha", usage); err != nil {
			t.Fatal(err)
		}
	}
	n, err := testStore.UsageCountForTask(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("usage rows = %d, want 2 (shots+tasks, deduped)", n)
	}

	totals, err := testStore.TenantUsage(ctx, "usage-tenant", time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(totals) != 2 || totals[0].Amount+totals[1].Amount != 1001 {
		t.Fatalf("totals = %+v", totals)
	}

	// Time-window filters (nullableTime non-nil paths).
	past, err := testStore.TenantUsage(ctx, "usage-tenant",
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil || len(past) != 2 {
		t.Fatalf("windowed usage: %+v %v", past, err)
	}
	none, err := testStore.TenantUsage(ctx, "usage-tenant",
		time.Now().Add(time.Hour), time.Now().Add(2*time.Hour))
	if err != nil || len(none) != 0 {
		t.Fatalf("future window must be empty: %+v %v", none, err)
	}
}

func TestNotifyAndWait(t *testing.T) {
	ctx := t.Context()
	// A notify already in flight wakes the waiter.
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = testStore.NotifyJobs(context.Background())
	}()
	start := time.Now()
	if err := testStore.WaitForJobNotify(ctx, 5*time.Second); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("notify did not wake the waiter")
	}
	// Timeout without a notify is a clean nil.
	if err := testStore.WaitForJobNotify(ctx, 200*time.Millisecond); err != nil {
		t.Fatalf("quiet timeout must be nil, got %v", err)
	}
}

func TestTaskMethodsOnClosedPool(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testDSN)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	if _, err := st.BindJob(ctx, "x", "y", "t", nil); err == nil {
		t.Fatal("BindJob on closed pool must fail")
	}
	if _, err := st.PendingJobs(ctx, 5); err == nil {
		t.Fatal("PendingJobs on closed pool must fail")
	}
	if _, err := st.ActiveTasks(ctx); err == nil {
		t.Fatal("ActiveTasks on closed pool must fail")
	}
	if err := st.UpdateTask(ctx, "x", "", "RUNNING", nil, nil); err == nil {
		t.Fatal("UpdateTask on closed pool must fail")
	}
	if err := st.RecordUsage(ctx, "j", "t", "ten", "tar", map[string]float64{"shots": 1}); err == nil {
		t.Fatal("RecordUsage on closed pool must fail")
	}
	if _, err := st.TenantUsage(ctx, "ten", time.Time{}, time.Time{}); err == nil {
		t.Fatal("TenantUsage on closed pool must fail")
	}
	if err := st.NotifyJobs(ctx); err == nil {
		t.Fatal("NotifyJobs on closed pool must fail")
	}
	if err := st.WaitForJobNotify(ctx, time.Second); err == nil {
		t.Fatal("WaitForJobNotify on closed pool must fail")
	}
}

func TestPendingJobsOrder(t *testing.T) {
	ctx := t.Context()
	a := newRec("88888888-8888-8888-8888-888888888820", "order", "first")
	b := newRec("88888888-8888-8888-8888-888888888821", "order", "second")
	if err := testStore.InsertJob(ctx, a); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := testStore.InsertJob(ctx, b); err != nil {
		t.Fatal(err)
	}
	pending, err := testStore.PendingJobs(ctx, 1000)
	if err != nil {
		t.Fatal(err)
	}
	posA, posB := -1, -1
	for i, rec := range pending {
		if rec.JobID == a.JobID {
			posA = i
		}
		if rec.JobID == b.JobID {
			posB = i
		}
	}
	if posA == -1 || posB == -1 || posA > posB {
		t.Fatalf("pending order wrong: a=%d b=%d", posA, posB)
	}
}
