// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"testing"
)

// round-robin rotates deterministically over the feasible set. Stateful, so
// it is tested on a fresh instance rather than the registered singleton.
func TestRoundRobinRotates(t *testing.T) {
	p := &roundRobinPolicy{}
	fleet := []*TargetView{
		baseTarget(), baseTarget(), baseTarget(),
	}
	fleet[0].Name = "sim/a"
	fleet[1].Name = "sim/b"
	fleet[2].Name = "sim/c"

	var picks []string
	for range 6 {
		d := Schedule(p, baseJob(), fleet, now)
		picks = append(picks, d.Target)
	}
	want := []string{"sim/a", "sim/b", "sim/c", "sim/a", "sim/b", "sim/c"}
	for i := range want {
		if picks[i] != want[i] {
			t.Fatalf("rotation = %v, want %v", picks, want)
		}
	}

	// Rotation skips infeasible targets rather than stalling.
	fleet[1].Online = false
	d := Schedule(p, baseJob(), fleet, now)
	if d.Target != "sim/a" && d.Target != "sim/c" {
		t.Fatalf("rotation picked infeasible target %q", d.Target)
	}
}

// static-best is deterministic and ignores live calibration entirely.
func TestStaticBestIgnoresLiveCalibration(t *testing.T) {
	p, err := Lookup("static-best/v0")
	if err != nil {
		t.Fatal(err)
	}
	flagship := baseTarget()
	flagship.Name = "sim/flagship"
	flagship.Nominal2QError = 0.005
	// Live calibration terrible — static-best must not care.
	for i := range flagship.Metrics {
		flagship.Metrics[i].Value *= 10
	}
	modest := baseTarget()
	modest.Name = "sim/modest"
	modest.Nominal2QError = 0.009

	d := Schedule(p, baseJob(), []*TargetView{modest, flagship}, now)
	if d.Target != "sim/flagship" {
		t.Fatalf("static-best picked %q, want the nominal flagship", d.Target)
	}

	// Unknown nominal ranks below known.
	unknown := baseTarget()
	unknown.Name = "sim/unknown"
	d = Schedule(p, baseJob(), []*TargetView{unknown, modest}, now)
	if d.Target != "sim/modest" {
		t.Fatalf("static-best picked %q over a device with known nominal", d.Target)
	}
}

// calib-aware records its ESP prediction in the decision.
func TestCalibAwarePredictsESP(t *testing.T) {
	p, err := Lookup("calib-aware/v0")
	if err != nil {
		t.Fatal(err)
	}
	d := Schedule(p, baseJob(), []*TargetView{baseTarget()}, now)
	if d.Target == "" || d.PredictedESP <= 0 || d.PredictedESP > 1 {
		t.Fatalf("prediction missing or out of range: %+v", d)
	}
	rec := d.PlacementRecord()
	predicted := rec["predicted"].(map[string]any)
	if _, ok := predicted["successProbability"]; !ok {
		t.Fatalf("placement record lacks successProbability: %v", rec)
	}
}
