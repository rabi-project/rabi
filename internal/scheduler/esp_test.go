// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"math"
	"testing"
	"time"
)

func targetWithMetrics(e1, e2, ro []float64) *TargetView {
	t := &TargetView{Name: "sim/fixture"}
	for i, v := range e1 {
		t.Metrics = append(t.Metrics, Metric{Name: "gate.1q.error", Value: v, Qubits: []uint32{uint32(i)}})
	}
	for i, v := range e2 {
		t.Metrics = append(t.Metrics, Metric{Name: "gate.2q.cx.error", Value: v, Qubits: []uint32{uint32(i), uint32(i + 1)}})
	}
	for i, v := range ro {
		t.Metrics = append(t.Metrics, Metric{Name: "readout.error", Value: v, Qubits: []uint32{uint32(i)}})
	}
	return t
}

// T5.esp — hand-computed fixtures agree to 1e-9. The simple cases are
// verifiable on paper: 1-gate = 1−ε_best; readout-only = ∏(1−ε_k); missing
// metric classes fall back to the documented defaults (0.01/0.05/0.05).
func TestESPFixtures(t *testing.T) {
	cases := []struct {
		name       string
		profile    CircuitProfile
		e1, e2, ro []float64
		want       float64
	}{
		{"one 1q gate", CircuitProfile{Qubits: 1, OneQubitGates: 1},
			[]float64{0.001, 0.003}, nil, nil, 0.999},
		{"one 2q gate", CircuitProfile{Qubits: 2, TwoQubitGates: 1},
			nil, []float64{0.008, 0.012}, nil, 0.992},
		{"readout only", CircuitProfile{Qubits: 2, MeasuredQubits: 2},
			nil, nil, []float64{0.01, 0.02, 0.05}, 0.970200000000000},
		{"bell", CircuitProfile{Qubits: 2, OneQubitGates: 2, TwoQubitGates: 1, MeasuredQubits: 2},
			[]float64{0.0004, 0.0003}, []float64{0.008}, []float64{0.015, 0.012},
			0.964718902068834},
		{"50 gates", CircuitProfile{Qubits: 5, OneQubitGates: 30, TwoQubitGates: 20, MeasuredQubits: 5},
			[]float64{0.0004, 0.0003, 0.0005, 0.0004, 0.0006},
			[]float64{0.008, 0.010, 0.009, 0.012},
			[]float64{0.015, 0.012, 0.020, 0.018, 0.025},
			0.740795656126013},
		{"no 1q metrics -> default", CircuitProfile{Qubits: 2, OneQubitGates: 3},
			nil, nil, nil, 0.970299000000000},
		{"no 2q metrics -> default", CircuitProfile{Qubits: 3, TwoQubitGates: 2},
			nil, nil, nil, 0.902500000000000},
		{"no readout -> default", CircuitProfile{Qubits: 2, MeasuredQubits: 2},
			nil, nil, nil, 0.902500000000000},
		{"empty profile", CircuitProfile{Qubits: 2},
			[]float64{0.5}, []float64{0.5}, []float64{0.5}, 1.0},
		{"best-K edge subset", CircuitProfile{Qubits: 3, TwoQubitGates: 4},
			nil, []float64{0.02, 0.005, 0.010, 0.001, 0.015}, nil, 0.988053892081000},
		{"fallback fill beyond available", CircuitProfile{Qubits: 4, TwoQubitGates: 2, MeasuredQubits: 3},
			nil, []float64{0.004}, []float64{0.01}, 0.832601158400000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ESP(tc.profile, targetWithMetrics(tc.e1, tc.e2, tc.ro))
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("ESP = %.15f, want %.15f (Δ %.2g)", got, tc.want, math.Abs(got-tc.want))
			}
		})
	}
}

// ESP must reward better calibration, monotonically.
func TestESPMonotoneInErrors(t *testing.T) {
	profile := CircuitProfile{Qubits: 3, OneQubitGates: 5, TwoQubitGates: 4, MeasuredQubits: 3}
	good := targetWithMetrics([]float64{0.001, 0.001, 0.001}, []float64{0.005, 0.005}, []float64{0.01, 0.01, 0.01})
	bad := targetWithMetrics([]float64{0.002, 0.002, 0.002}, []float64{0.010, 0.010}, []float64{0.02, 0.02, 0.02})
	if ESP(profile, good) <= ESP(profile, bad) {
		t.Fatal("better calibration must yield higher ESP")
	}
}

// T5.weights — the documented intent → weights table; deadline present must
// raise the wait weight strictly.
func TestWeightMapping(t *testing.T) {
	p := calibAwarePolicy{}
	deadline := func(j *JobView) { j.Deadline = now.Add(time.Hour) }
	floor := func(j *JobView) { j.TwoQubitErrorMax = 0.01 }

	cases := []struct {
		name       string
		mutate     []func(*JobView)
		wq, wt, wc float64
	}{
		{"plain", nil, 0.60, 0.25, 0.15},
		{"deadline", []func(*JobView){deadline}, 0.45, 0.45, 0.10},
		{"quality floor", []func(*JobView){floor}, 0.75, 0.15, 0.10},
		{"deadline + floor", []func(*JobView){deadline, floor}, 0.55, 0.35, 0.10},
	}
	base := map[string]float64{}
	for _, tc := range cases {
		j := baseJob()
		for _, m := range tc.mutate {
			m(j)
		}
		wq, wt, wc := p.Weights(j)
		if wq != tc.wq || wt != tc.wt || wc != tc.wc {
			t.Errorf("%s: weights = (%.2f, %.2f, %.2f), want (%.2f, %.2f, %.2f)",
				tc.name, wq, wt, wc, tc.wq, tc.wt, tc.wc)
		}
		base[tc.name] = wt
	}
	if !(base["deadline"] > base["plain"] && base["deadline + floor"] > base["quality floor"]) {
		t.Fatal("deadline must strictly raise the wait weight")
	}
}
