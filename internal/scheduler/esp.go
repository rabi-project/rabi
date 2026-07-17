// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"math"
	"sort"
	"strings"
)

// ESP — Estimated Success Probability — the standard proxy from the
// literature (Ravi et al.):
//
//	ESP = ∏ over gates (1 − ε_gate) × ∏ over measured qubits (1 − ε_readout)
//
// v0 evaluates it on the un-routed circuit profile with a best-region
// mapping assumption (the transpiler steers toward good qubits), documented
// in D-021:
//
//   - ε_1q: mean of the best min(Qubits, available) one-qubit gate errors,
//     applied to every 1q gate.
//   - ε_2q: mean of the best min(max(Qubits−1, 1), available) two-qubit edge
//     errors (any gate.2q.<gate>.error), applied to every 2q gate — a
//     Qubits-node connected region uses ≈ Qubits−1 distinct edges.
//   - readout: exact product over the best MeasuredQubits readout errors.
//
// Missing metric classes fall back to conservative defaults so an opaque
// target never outranks a measured one.
const (
	defaultOneQubitError = 0.01
	defaultTwoQubitError = 0.05
	defaultReadoutError  = 0.05
)

// ESP computes the estimated success probability of profile p on target t.
func ESP(p CircuitProfile, t *TargetView) float64 {
	esp := 1.0

	if p.OneQubitGates > 0 {
		e1 := meanBestK(metricValues(t, func(name string) bool { return name == "gate.1q.error" }),
			maxInt(p.Qubits, 1), defaultOneQubitError)
		esp *= math.Pow(1-e1, float64(p.OneQubitGates))
	}
	if p.TwoQubitGates > 0 {
		edges := maxInt(p.Qubits-1, 1)
		e2 := meanBestK(metricValues(t, func(name string) bool {
			return strings.HasPrefix(name, "gate.2q.") && strings.HasSuffix(name, ".error")
		}), edges, defaultTwoQubitError)
		esp *= math.Pow(1-e2, float64(p.TwoQubitGates))
	}
	if p.MeasuredQubits > 0 {
		ro := metricValues(t, func(name string) bool { return name == "readout.error" })
		sort.Float64s(ro)
		for k := 0; k < p.MeasuredQubits; k++ {
			if k < len(ro) {
				esp *= 1 - ro[k]
			} else {
				esp *= 1 - defaultReadoutError
			}
		}
	}
	return esp
}

func metricValues(t *TargetView, match func(string) bool) []float64 {
	var out []float64
	for _, m := range t.Metrics {
		if match(m.Name) {
			out = append(out, m.Value)
		}
	}
	return out
}

// meanBestK is the mean of the k smallest values; missing values are filled
// with the conservative default.
func meanBestK(values []float64, k int, fallback float64) float64 {
	sort.Float64s(values)
	sum := 0.0
	for i := 0; i < k; i++ {
		if i < len(values) {
			sum += values[i]
		} else {
			sum += fallback
		}
	}
	return sum / float64(k)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
