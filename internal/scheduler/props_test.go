// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

// T5.props — policy sanity over ≥1,000 generated cases per property.
//
// Note: the T&V plan names the `rapid` library, but rapid is MPL-2.0 and the
// build plan forbids copyleft dependencies (flagged in docs/decisions.md
// D-023). Generation here is seeded stdlib rand: deterministic, shrink-free.
const propCases = 1200

func genTarget(r *rand.Rand, name string) *TargetView {
	qubits := 2 + r.Intn(24)
	t := &TargetView{
		Name:       name,
		Modality:   "gate-model",
		Technology: "superconducting",
		Qubits:     uint32(qubits),
		Formats:    []string{"openqasm3"},
		MaxShots:   100000,
		Billing:    []string{"shots", "tasks"},
		Online:     r.Float64() > 0.1,
		SnapshotID: fmt.Sprintf("snap-%s", name),
		MeasuredAt: now.Add(-time.Duration(r.Intn(48)) * time.Hour),
	}
	for q := 0; q < qubits; q++ {
		t.Metrics = append(t.Metrics,
			Metric{Name: "gate.1q.error", Value: 0.0001 + r.Float64()*0.002, Qubits: []uint32{uint32(q)}},
			Metric{Name: "readout.error", Value: 0.005 + r.Float64()*0.06, Qubits: []uint32{uint32(q)}},
		)
	}
	for e := 0; e < qubits-1; e++ {
		t.Metrics = append(t.Metrics, Metric{
			Name: "gate.2q.cx.error", Value: 0.003 + r.Float64()*0.03,
			Qubits: []uint32{uint32(e), uint32(e + 1)},
		})
	}
	return t
}

func genJob(r *rand.Rand) *JobView {
	j := &JobView{
		ID: "prop-job", Tenant: "prop", Kind: "gate-model",
		Format: "openqasm3", Shots: uint64(100 + r.Intn(5000)),
		Qubits: uint32(2 + r.Intn(12)),
	}
	if r.Float64() < 0.4 {
		j.TwoQubitErrorMax = 0.004 + r.Float64()*0.04
	}
	if r.Float64() < 0.4 {
		j.ReadoutErrorMax = 0.008 + r.Float64()*0.08
	}
	if r.Float64() < 0.3 {
		j.Deadline = now.Add(time.Hour)
	}
	if r.Float64() < 0.3 {
		j.CalibrationMaxAge = time.Duration(1+r.Intn(72)) * time.Hour
	}
	return j
}

func genFleet(r *rand.Rand) []*TargetView {
	n := 1 + r.Intn(6)
	fleet := make([]*TargetView, n)
	for i := range fleet {
		fleet[i] = genTarget(r, fmt.Sprintf("sim/t%02d", i))
	}
	return fleet
}

// Property: tightening any quality floor never worsens the selection's ESP
// *while the previous winner remains feasible* — and any new selection always
// satisfies the tightened floor. (The unconditional reading of the T&V line
// is unsound: a floor can exclude the previous winner via its best-edge
// metric even though its overall ESP was higher, forcing a legitimately
// lower-ESP reroute. The conditional form is the sound core of the intent.)
func TestPropTighterFloorNeverWorsensESP(t *testing.T) {
	policy, err := Lookup("calib-aware/v0")
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewSource(51))
	for i := 0; i < propCases; i++ {
		j := genJob(r)
		fleet := genFleet(r)
		before := Schedule(policy, j, fleet, now)
		if before.Target == "" {
			continue
		}

		tightened := *j
		switch r.Intn(2) {
		case 0:
			if tightened.TwoQubitErrorMax == 0 {
				tightened.TwoQubitErrorMax = 0.05
			}
			tightened.TwoQubitErrorMax *= 0.5 + r.Float64()*0.4
		case 1:
			if tightened.ReadoutErrorMax == 0 {
				tightened.ReadoutErrorMax = 0.08
			}
			tightened.ReadoutErrorMax *= 0.5 + r.Float64()*0.4
		}
		after := Schedule(policy, &tightened, fleet, now)
		if after.Target == "" {
			continue // floor now infeasible: legal outcome
		}

		// The selection always honors the tightened floor.
		var selected *TargetView
		for _, tv := range fleet {
			if tv.Name == after.Target {
				selected = tv
			}
		}
		if reason := FilterTarget(&tightened, selected, now); reason != "" {
			t.Fatalf("case %d: selected target violates tightened floor: %s", i, reason)
		}

		// While the previous winner stays feasible, ESP never drops.
		var prev *TargetView
		for _, tv := range fleet {
			if tv.Name == before.Target {
				prev = tv
			}
		}
		if FilterTarget(&tightened, prev, now) == "" &&
			after.PredictedESP < before.PredictedESP-1e-12 {
			t.Fatalf("case %d: previous winner still feasible but ESP dropped %.9f -> %.9f (%s -> %s)",
				i, before.PredictedESP, after.PredictedESP, before.Target, after.Target)
		}
	}
}

// Property: adding a target never shrinks the feasible set.
func TestPropAddingTargetNeverShrinksFeasibleSet(t *testing.T) {
	policy, err := Lookup("calib-aware/v0")
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewSource(52))
	for i := 0; i < propCases; i++ {
		j := genJob(r)
		fleet := genFleet(r)
		before := Schedule(policy, j, fleet, now)
		feasibleBefore := len(fleet) - len(before.Rejected)

		extra := genTarget(r, "sim/zz-extra")
		after := Schedule(policy, j, append(fleet, extra), now)
		feasibleAfter := len(fleet) + 1 - len(after.Rejected)

		if feasibleAfter < feasibleBefore {
			t.Fatalf("case %d: feasible set shrank %d -> %d after adding a target",
				i, feasibleBefore, feasibleAfter)
		}
	}
}

// Property: removing the bound target reroutes or leaves the job unplaced —
// never panics, and never re-selects the removed target.
func TestPropRemovingBoundTargetReroutes(t *testing.T) {
	for _, policyName := range []string{"calib-aware/v0", "fifo/v0", "static-best/v0", "round-robin/v0"} {
		policy, err := Lookup(policyName)
		if err != nil {
			t.Fatal(err)
		}
		r := rand.New(rand.NewSource(53))
		for i := 0; i < propCases/4; i++ {
			j := genJob(r)
			fleet := genFleet(r)
			first := Schedule(policy, j, fleet, now)
			if first.Target == "" {
				continue
			}
			var without []*TargetView
			for _, tv := range fleet {
				if tv.Name != first.Target {
					without = append(without, tv)
				}
			}
			second := Schedule(policy, j, without, now)
			if second.Target == first.Target {
				t.Fatalf("[%s] case %d: removed target %q still selected", policyName, i, first.Target)
			}
			if second.Target == "" && len(without) > 0 && len(second.Rejected) != len(without) {
				t.Fatalf("[%s] case %d: unplaced but rejections (%d) don't cover fleet (%d)",
					policyName, i, len(second.Rejected), len(without))
			}
		}
	}
}
