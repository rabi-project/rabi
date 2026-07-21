// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"testing"
	"time"
)

func absTarget(name string, twoQErr, wait float64, measuredAgo time.Duration) *TargetView {
	return &TargetView{
		Name: name, Online: true, Modality: "gate-model",
		Formats: []string{"openqasm3"}, Qubits: 8, MaxShots: 100000,
		WaitSeconds: wait, MeasuredAt: now.Add(-measuredAgo),
		Metrics: []Metric{{Name: "gate.2q.error", Value: twoQErr}},
	}
}

func absJob() *JobView {
	return &JobView{
		Kind: "gate-model", Qubits: 2,
		Profile: &CircuitProfile{Qubits: 2, OneQubitGates: 2, TwoQubitGates: 3, MeasuredQubits: 2},
	}
}

func argmax(xs []float64) int {
	best := 0
	for i, x := range xs {
		if x > xs[best] {
			best = i
		}
	}
	return best
}

func TestParetoScoreIsZero(t *testing.T) {
	// Selection is via ScoreSet; the per-target Score exists only for the
	// interface and must be inert.
	if got := (paretoPolicy{}).Score(absJob(), absTarget("x", 0.01, 0, 0), now); got != 0 {
		t.Fatalf("pareto Score should be 0, got %v", got)
	}
}

func TestParetoPicksDominatingTarget(t *testing.T) {
	j := absJob()
	// "good" has lower error (higher ESP) AND lower wait — it Pareto-dominates.
	feasible := []*TargetView{
		absTarget("sim/good", 0.005, 5, 0),
		absTarget("sim/bad", 0.05, 100, 0),
	}
	scores := (paretoPolicy{}).ScoreSet(j, feasible, now)
	if argmax(scores) != 0 {
		t.Fatalf("pareto should pick the dominating target; scores=%v", scores)
	}
}

func TestParetoFrontScalarization(t *testing.T) {
	j := absJob()
	// Two non-dominated targets: one faster, one higher-fidelity. Both are on
	// the Pareto front; the balanced scalarization must still pick one, and the
	// result is deterministic.
	feasible := []*TargetView{
		absTarget("sim/fast", 0.05, 1, 0),   // faster, lower fidelity
		absTarget("sim/hifi", 0.005, 50, 0), // slower, higher fidelity
	}
	a := (paretoPolicy{}).ScoreSet(j, feasible, now)
	b := (paretoPolicy{}).ScoreSet(j, feasible, now)
	if a[argmax(a)] != b[argmax(b)] || argmax(a) != argmax(b) {
		t.Fatalf("pareto scalarization must be deterministic: %v vs %v", a, b)
	}
}

func TestParetoAllEqual(t *testing.T) {
	j := absJob()
	// Identical targets exercise the normalize hi==lo branch; every score equal.
	feasible := []*TargetView{
		absTarget("sim/a", 0.01, 10, 0),
		absTarget("sim/b", 0.01, 10, 0),
	}
	scores := (paretoPolicy{}).ScoreSet(j, feasible, now)
	if scores[0] != scores[1] {
		t.Fatalf("identical targets should score equally: %v", scores)
	}
}

func TestAdaptiveDeferralPrefersFreshCalibration(t *testing.T) {
	p := adaptiveDeferralPolicy{}
	j := absJob()
	fresh := absTarget("sim/fresh", 0.01, 0, 1*time.Minute)
	stale := absTarget("sim/stale", 0.01, 0, 10*time.Hour)
	if p.Score(j, fresh, now) <= p.Score(j, stale, now) {
		t.Fatal("fresher calibration should score higher at equal ESP")
	}
	// PredictESP surfaces the raw fidelity proxy (freshness-independent).
	if p.PredictESP(j, fresh) != p.PredictESP(j, stale) {
		t.Error("PredictESP should be the raw ESP, equal for equal calibration metrics")
	}
}

func TestAdaptiveDeferralEdges(t *testing.T) {
	p := adaptiveDeferralPolicy{}
	j := absJob()
	// Unknown calibration age → neutral 0.5 weight, not a hard zero.
	unknown := &TargetView{Name: "sim/u", Metrics: []Metric{{Name: "gate.2q.error", Value: 0.01}}}
	full := absTarget("sim/f", 0.01, 0, 0) // measured "now": age 0 → freshness 1
	if got := p.Score(j, unknown, now); got <= 0 {
		t.Fatalf("unknown-calibration score should be positive (neutral), got %v", got)
	}
	// A future MeasuredAt (negative age) clamps to freshest.
	future := absTarget("sim/future", 0.01, 0, -time.Hour)
	if p.Score(j, future, now) < p.Score(j, full, now)-1e-9 {
		t.Fatal("negative age should clamp to freshest, not penalize")
	}
	if j.CalibrationMaxAge != 0 { // sanity: default-window path used above
		t.Fatal("test job should declare no calibrationMaxAge")
	}
}

// The absorbed policies must honor a declared calibration window (custom-window
// path in adaptive-deferral).
func TestAdaptiveDeferralCustomWindow(t *testing.T) {
	p := adaptiveDeferralPolicy{}
	j := absJob()
	j.CalibrationMaxAge = 2 * time.Hour
	near := absTarget("sim/near", 0.01, 0, 30*time.Minute)
	far := absTarget("sim/far", 0.01, 0, 90*time.Minute)
	if p.Score(j, near, now) <= p.Score(j, far, now) {
		t.Fatal("within a custom window, less-aged calibration still scores higher")
	}
}
