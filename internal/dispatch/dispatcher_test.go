// SPDX-License-Identifier: Apache-2.0

// Component tests for the dispatcher: real Postgres (testcontainers), fake
// in-process adapter, real registry.
package dispatch_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	adapterv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/adapter/v1alpha1"
	"github.com/rabi-project/rabi/internal/adaptertest"
	"github.com/rabi-project/rabi/internal/dispatch"
	"github.com/rabi-project/rabi/internal/job"
	"github.com/rabi-project/rabi/internal/registry"
	"github.com/rabi-project/rabi/internal/store"
)

var testStore *store.Store

func TestMain(m *testing.M) {
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:15-alpine",
		tcpostgres.WithDatabase("rabi"), tcpostgres.WithUsername("rabi"),
		tcpostgres.WithPassword("rabi"), tcpostgres.BasicWaitStrategies())
	if err != nil {
		log.Fatalf("postgres container: %v", err)
	}
	defer func() { _ = pg.Terminate(ctx) }()
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	testStore, err = store.Open(ctx, dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer testStore.Close()
	os.Exit(m.Run())
}

func newFleet(t *testing.T, specs ...*adaptertest.TargetSpec) (*registry.Registry, *dispatch.Dispatcher) {
	t.Helper()
	fake := adaptertest.New(specs...)
	addr := fake.Serve(t)
	reg, err := registry.NewFromSpec("sim=" + addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	reg.Start(ctx)
	d, err := dispatch.New(testStore, reg, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	go d.Run(ctx)
	return reg, d
}

func insertJob(t *testing.T, id, tenant string, spec map[string]any) *store.JobRecord {
	t.Helper()
	rec := &store.JobRecord{
		JobID:  id,
		Tenant: tenant,
		Name:   "test-job",
		Phase:  job.Pending,
		Doc: map[string]any{
			"apiVersion": "tangle.dev/v1alpha1", "kind": "QuantumJob",
			"metadata": map[string]any{"name": "test-job", "tenant": tenant},
			"spec":     spec,
		},
		Status: map[string]any{"phase": "PENDING", "conditions": []any{}},
	}
	if err := testStore.InsertJob(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func gateModelSpec(qubits int, shots int) map[string]any {
	inline := base64.StdEncoding.EncodeToString([]byte("OPENQASM 3.0;\nqubit[2] q;\n"))
	return map[string]any{
		"workload": map[string]any{
			"kind": "gate-model",
			"gateModel": map[string]any{
				"program": map[string]any{"format": "openqasm3", "inline": inline},
				"shots":   float64(shots),
			},
		},
		"requirements": map[string]any{"qubits": float64(qubits)},
	}
}

func awaitPhase(t *testing.T, jobID string, want job.Phase, within time.Duration) *store.JobRecord {
	t.Helper()
	deadline := time.Now().Add(within)
	var rec *store.JobRecord
	var err error
	for time.Now().Before(deadline) {
		rec, err = testStore.GetJob(context.Background(), jobID)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Phase == want {
			return rec
		}
		if rec.Phase.Terminal() && rec.Phase != want {
			t.Fatalf("job reached terminal %s, want %s (status=%v)", rec.Phase, want, rec.Status)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("job never reached %s (last: %s)", want, rec.Phase)
	return nil
}

func TestJobRunsEndToEnd(t *testing.T) {
	newFleet(t, &adaptertest.TargetSpec{
		ID: "alpha", Qubits: 5, Formats: []string{"openqasm3"}, SnapshotID: "snap-abc"})

	rec := insertJob(t, "10000000-0000-0000-0000-000000000001", "acme", gateModelSpec(2, 500))
	done := awaitPhase(t, rec.JobID, job.Succeeded, 30*time.Second)

	// Placement audit trail (spec: recorded before submission; T3.audit —
	// policy id, snapshot id, prediction, and the rejected list are all
	// present and well-formed).
	placement, _ := done.Status["placement"].(map[string]any)
	if placement["policy"] != "fifo/v0" || placement["calibrationSnapshot"] != "snap-abc" {
		t.Fatalf("placement audit incomplete: %v", placement)
	}
	if done.Status["boundTarget"] != "sim/alpha" {
		t.Fatalf("boundTarget = %v", done.Status["boundTarget"])
	}
	if reason, _ := placement["reason"].(string); reason == "" {
		t.Fatal("placement reason empty")
	}
	predicted, _ := placement["predicted"].(map[string]any)
	if _, ok := predicted["waitSeconds"]; !ok {
		t.Fatalf("placement lacks predicted.waitSeconds: %v", placement)
	}
	if _, ok := placement["rejected"].([]any); !ok {
		t.Fatalf("placement lacks rejected list: %v", placement)
	}

	// Result and usage mirrored into status.
	tasks, _ := done.Status["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("status.tasks = %v", tasks)
	}

	// Ledger has exactly one row per unit for the task.
	task, err := testStore.TaskForJob(context.Background(), rec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	n, err := testStore.UsageCountForTask(context.Background(), task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 { // shots + tasks
		t.Fatalf("usage rows = %d, want 2", n)
	}
	totals, err := testStore.TenantUsage(context.Background(), "acme", time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var shots float64
	for _, u := range totals {
		if u.Unit == "shots" && u.Target == "sim/alpha" {
			shots = u.Amount
		}
	}
	if shots < 500 {
		t.Fatalf("ledger shots = %v, want >= 500", shots)
	}
}

func TestInfeasibleJobStaysPending(t *testing.T) {
	newFleet(t, &adaptertest.TargetSpec{
		ID: "small", Qubits: 3, Formats: []string{"openqasm3"}, SnapshotID: "s"})

	rec := insertJob(t, "10000000-0000-0000-0000-000000000002", "acme", gateModelSpec(50, 100))

	// A job inserted between dispatch cycles is picked up by the 5s fallback
	// tick (the LISTEN window reopens per cycle by design), so poll with a
	// generous deadline instead of a fixed sleep — while asserting the job
	// never leaves PENDING.
	deadline := time.Now().Add(20 * time.Second)
	var conditions string
	for time.Now().Before(deadline) {
		got, err := testStore.GetJob(context.Background(), rec.JobID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Phase != job.Pending {
			t.Fatalf("infeasible job moved to %s", got.Phase)
		}
		conditions = fmt.Sprintf("%v", got.Status["conditions"])
		if containsStr(conditions, "NoFeasibleTarget") && containsStr(conditions, "requires 50 qubits") {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("missing schedulability condition after 20s: %s", conditions)
}

func TestAdapterFailureMapsToFailedJob(t *testing.T) {
	newFleet(t, &adaptertest.TargetSpec{
		ID: "broken", Qubits: 5, Formats: []string{"openqasm3"}, SnapshotID: "s",
		FailWith: &adapterv1alpha1.ErrorDetail{
			Category:      adapterv1alpha1.ErrorDetail_INVALID_PROGRAM,
			Retriable:     false,
			VendorMessage: "synthetic failure",
		}})

	rec := insertJob(t, "10000000-0000-0000-0000-000000000003", "acme", gateModelSpec(2, 100))
	done := awaitPhase(t, rec.JobID, job.Failed, 30*time.Second)

	tasks, _ := done.Status["tasks"].([]any)
	task0, _ := tasks[0].(map[string]any)
	errDetail, _ := task0["error"].(map[string]any)
	if errDetail["category"] != "INVALID_PROGRAM" {
		t.Fatalf("error category = %v", errDetail["category"])
	}
	// Failed job consumed nothing.
	taskRec, err := testStore.TaskForJob(context.Background(), rec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := testStore.UsageCountForTask(context.Background(), taskRec.TaskID); n != 0 {
		t.Fatalf("failed task recorded %d usage rows", n)
	}
}

func TestSourceURIFailsFast(t *testing.T) {
	newFleet(t, &adaptertest.TargetSpec{
		ID: "alpha2", Qubits: 5, Formats: []string{"openqasm3"}, SnapshotID: "s"})

	spec := gateModelSpec(2, 100)
	program := spec["workload"].(map[string]any)["gateModel"].(map[string]any)["program"].(map[string]any)
	delete(program, "inline")
	program["source"] = "s3://bucket/prog.qasm"

	rec := insertJob(t, "10000000-0000-0000-0000-000000000004", "acme", spec)
	done := awaitPhase(t, rec.JobID, job.Failed, 30*time.Second)
	conditions := fmt.Sprintf("%v", done.Status["conditions"])
	if !containsStr(conditions, "not resolvable") {
		t.Fatalf("expected precise source-uri failure, got %s", conditions)
	}
}

func TestDispatcherCancelPropagates(t *testing.T) {
	_, d := newFleet(t, &adaptertest.TargetSpec{
		ID: "slowpoke", Qubits: 5, Formats: []string{"openqasm3"}, SnapshotID: "s",
		StepDelay: 2 * time.Second})

	rec := insertJob(t, "10000000-0000-0000-0000-000000000005", "acme", gateModelSpec(2, 100))
	awaitPhase(t, rec.JobID, job.Submitted, 30*time.Second)

	if err := d.CancelJob(context.Background(), rec.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := testStore.TransitionJob(context.Background(), rec.JobID, job.Cancelled, nil); err != nil {
		t.Fatal(err)
	}
	// The adapter task converges to CANCELLED and the job stays CANCELLED.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		taskRec, err := testStore.TaskForJob(context.Background(), rec.JobID)
		if err == nil && taskRec.State == "CANCELLED" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	got, err := testStore.GetJob(context.Background(), rec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != job.Cancelled {
		t.Fatalf("job phase = %s, want CANCELLED", got.Phase)
	}
	taskRec, err := testStore.TaskForJob(context.Background(), rec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := testStore.UsageCountForTask(context.Background(), taskRec.TaskID); n != 0 {
		t.Fatalf("cancelled-before-run task recorded %d usage rows, want 0", n)
	}
}

func containsStr(haystack, needle string) bool { return strings.Contains(haystack, needle) }
