// SPDX-License-Identifier: Apache-2.0

// RFC-0003 DES tests: a fleet where the quality floor and the deadline
// provably conflict (the only target's two-qubit error exceeds the floor and
// the deadline is at hand), one test per onConflict mode.
package dispatch_test

import (
	"testing"
	"time"

	adapterv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/adapter/v1alpha1"
	"github.com/rabi-project/rabi/internal/adaptertest"
	"github.com/rabi-project/rabi/internal/job"
)

// conflictSpec: floor 0.006 vs device error 0.02, deadline already at hand.
func conflictSpec(onConflict string) map[string]any {
	spec := gateModelSpec(2, 100)
	spec["requirements"].(map[string]any)["quality"] = map[string]any{
		"gateModel": map[string]any{"twoQubitErrorMax": 0.006},
	}
	spec["deadline"] = time.Now().UTC().Add(1 * time.Second).Format(time.RFC3339)
	if onConflict != "" {
		spec["scheduling"] = map[string]any{"onConflict": onConflict}
	}
	return spec
}

func conflictTarget() *adaptertest.TargetSpec {
	return &adaptertest.TargetSpec{
		ID: "noisy", Qubits: 5, Formats: []string{"openqasm3"}, MaxShots: 100000,
		SnapshotID: "conflict-snap",
		Metrics: []*adapterv1alpha1.Metric{
			{Name: "gate.2q.cx.error", Value: 0.02, Qubits: []uint32{0, 1}},
			{Name: "readout.error", Value: 0.01, Qubits: []uint32{0}},
		},
	}
}

func TestOnConflictPreferDeadline(t *testing.T) {
	newFleet(t, conflictTarget())
	rec := insertJob(t, "00000000-0000-4000-9000-00000000c001", "conflict/pd", conflictSpec("prefer-deadline"))

	got := awaitPhase(t, rec.JobID, job.Succeeded, 30*time.Second)
	placement, _ := got.Status["placement"].(map[string]any)
	if placement == nil {
		t.Fatal("no placement record")
	}
	if placement["floorsRelaxed"] != true || placement["onConflict"] != "prefer-deadline" {
		t.Fatalf("relaxation not recorded: %+v", placement)
	}
	relaxed, _ := placement["relaxedFloors"].([]any)
	if len(relaxed) != 1 {
		t.Fatalf("relaxedFloors = %v", relaxed)
	}
	detail, _ := relaxed[0].(map[string]any)
	if detail["floor"] != "twoQubitErrorMax" || detail["limit"] != 0.006 || detail["actual"] != 0.02 || detail["aggregate"] != "best" {
		t.Fatalf("relaxation detail wrong: %+v", detail)
	}
	if placement["decisionHorizon"] == "" || placement["horizonModel"] == "" {
		t.Fatalf("horizon not recorded: %+v", placement)
	}
}

func TestOnConflictReject(t *testing.T) {
	newFleet(t, conflictTarget())
	rec := insertJob(t, "00000000-0000-4000-9000-00000000c002", "conflict/rj", conflictSpec("reject"))

	got := awaitPhase(t, rec.JobID, job.Failed, 30*time.Second)
	conditions, _ := got.Status["conditions"].([]any)
	found := false
	for _, c := range conditions {
		m, _ := c.(map[string]any)
		if m["type"] == "UnsatisfiableBeforeDeadline" && m["status"] == "True" {
			found = true
		}
	}
	if !found {
		t.Fatalf("UnsatisfiableBeforeDeadline condition missing: %v", conditions)
	}
	errDetail, _ := got.Status["error"].(map[string]any)
	if errDetail["category"] != "CAPABILITY_MISMATCH" || errDetail["retriable"] != true {
		t.Fatalf("error detail wrong: %+v", errDetail)
	}
}

func TestOnConflictPreferQualityWaits(t *testing.T) {
	newFleet(t, conflictTarget())
	// Default mode (no scheduling block): v0 behavior — the job waits past
	// its deadline and gains the explicit condition.
	rec := insertJob(t, "00000000-0000-4000-9000-00000000c003", "conflict/pq", conflictSpec(""))

	deadline := time.Now().Add(20 * time.Second)
	var sawCondition bool
	for time.Now().Before(deadline) {
		got, err := testStore.GetJob(t.Context(), rec.JobID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Phase != job.Pending {
			t.Fatalf("prefer-quality must keep waiting, job went %s", got.Phase)
		}
		conditions, _ := got.Status["conditions"].([]any)
		for _, c := range conditions {
			m, _ := c.(map[string]any)
			if m["type"] == "DeadlineExceededWaitingForQuality" {
				sawCondition = true
			}
		}
		if sawCondition {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !sawCondition {
		t.Fatal("DeadlineExceededWaitingForQuality condition never recorded")
	}
	// And it is still PENDING (never silently relaxed or failed).
	got, _ := testStore.GetJob(t.Context(), rec.JobID)
	if got.Phase != job.Pending {
		t.Fatalf("job left PENDING: %s", got.Phase)
	}
}
