// SPDX-License-Identifier: Apache-2.0

package dispatch_test

import (
	"context"
	"testing"
	"time"

	"encoding/base64"

	"github.com/google/uuid"

	adapterv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/adapter/v1alpha1"
	"github.com/rabi-project/rabi/internal/adaptertest"
	"github.com/rabi-project/rabi/internal/dispatch"
	"github.com/rabi-project/rabi/internal/job"
	"github.com/rabi-project/rabi/internal/registry"
	"github.com/rabi-project/rabi/internal/scheduler"
)

// TestShadowPlacementsRecorded proves the dispatcher evaluates a candidate
// policy in shadow on every decision and records it — never binding it (P2.M5).
func TestShadowPlacementsRecorded(t *testing.T) {
	fake := adaptertest.New(
		&adaptertest.TargetSpec{ID: "alpha", Qubits: 8, Formats: []string{"openqasm3"},
			Metrics: []*adapterv1alpha1.Metric{{Name: "gate.2q.error", Value: 0.02}}},
		&adaptertest.TargetSpec{ID: "beta", Qubits: 8, Formats: []string{"openqasm3"},
			Metrics: []*adapterv1alpha1.Metric{{Name: "gate.2q.error", Value: 0.005}}},
	)
	addr := fake.Serve(t)
	reg, err := registry.NewFromSpec("sim=" + addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	reg.Start(ctx)

	d, err := dispatch.New(testStore, reg, "", nil) // active policy = fifo/v0
	if err != nil {
		t.Fatal(err)
	}
	cand, err := scheduler.Lookup("calib-aware/v0")
	if err != nil {
		t.Fatal(err)
	}
	d.EnableShadow(cand) // before Run: no race on d.shadow
	go d.Run(ctx)

	// Submit a handful of jobs and let them run to completion (which means each
	// was dispatched, so a shadow row was recorded at decision time). The
	// circuit carries two-qubit gates, so ESP depends on the target's 2q error
	// and calib-aware can distinguish the two targets.
	const n = 6
	for i := 0; i < n; i++ {
		rec := insertJob(t, uuid.NewString(), "acme", cxGateSpec())
		awaitPhase(t, rec.JobID, job.Succeeded, 30*time.Second)
	}

	ps, err := testStore.ShadowPlacementsSince(ctx, "calib-aware/v0", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("query shadow placements: %v", err)
	}
	if len(ps) < n {
		t.Fatalf("expected >= %d shadow placements, got %d", n, len(ps))
	}
	sawDisagreement := false
	for _, p := range ps {
		if p.Policy != "calib-aware/v0" || p.ActivePolicy != "fifo/v0" {
			t.Errorf("wrong policy labels: %+v", p)
		}
		if p.ShadowESP == nil || p.ActiveESP == nil {
			t.Errorf("feasible placement should carry ESP proxies: %+v", p)
		}
		if !p.Agreed {
			sawDisagreement = true
		}
	}
	// calib-aware prefers the lower-error target (beta); fifo takes the first by
	// name (alpha). At least one decision should differ — proving the shadow
	// policy is genuinely being evaluated, not echoing the active one.
	if !sawDisagreement {
		t.Error("expected calib-aware to disagree with fifo on at least one placement")
	}
}

// cxGateSpec is a gate-model job whose circuit has two-qubit gates, so ESP
// (and thus calib-aware's ranking) depends on each target's 2q error.
func cxGateSpec() map[string]any {
	qasm := "OPENQASM 3.0;\nqubit[2] q;\nh q[0];\ncx q[0], q[1];\ncx q[0], q[1];\ncx q[0], q[1];\nmeasure q -> c;\n"
	inline := base64.StdEncoding.EncodeToString([]byte(qasm))
	return map[string]any{
		"workload": map[string]any{
			"kind": "gate-model",
			"gateModel": map[string]any{
				"program": map[string]any{"format": "openqasm3", "inline": inline},
				"shots":   float64(500),
			},
		},
		"requirements": map[string]any{"qubits": float64(2)},
	}
}
