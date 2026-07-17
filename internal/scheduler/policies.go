// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"sync/atomic"
	"time"
)

// standardFilter is shared by every built-in policy: the spec's requirement
// dimensions are not policy-dependent (a policy may only narrow further).
type standardFilter struct{}

func (standardFilter) Filter(j *JobView, t *TargetView, now time.Time) string {
	return FilterTarget(j, t, now)
}

// fifoPolicy (M3): jobs are handled in arrival order; among feasible targets
// every score is equal, so the stable tie-break (first name) decides.
type fifoPolicy struct{ standardFilter }

func (fifoPolicy) Name() string                                   { return "fifo/v0" }
func (fifoPolicy) Score(*JobView, *TargetView, time.Time) float64 { return 0 }

// staticBestPolicy — the benchmark baseline modeling documented current
// practice (Ravi et al.): always the nominally best device, judged by its
// advertised baseline two-qubit error, blind to current calibration and
// queue depth.
type staticBestPolicy struct{ standardFilter }

func (staticBestPolicy) Name() string { return "static-best/v0" }

func (staticBestPolicy) Score(_ *JobView, t *TargetView, _ time.Time) float64 {
	if t.Nominal2QError > 0 {
		return -t.Nominal2QError
	}
	// Unknown nominal quality ranks below any known one.
	return -1
}

// roundRobinPolicy — the second baseline: rotate over the feasible set,
// blind to everything. Rotation state advances once per job (ScoreSet),
// which is deterministic for a deterministic job order.
type roundRobinPolicy struct {
	standardFilter
	counter atomic.Uint64
}

func (*roundRobinPolicy) Name() string { return "round-robin/v0" }

func (p *roundRobinPolicy) Score(*JobView, *TargetView, time.Time) float64 { return 0 }

func (p *roundRobinPolicy) ScoreSet(_ *JobView, feasible []*TargetView, _ time.Time) []float64 {
	scores := make([]float64, len(feasible))
	if len(feasible) > 0 {
		pick := (p.counter.Add(1) - 1) % uint64(len(feasible))
		scores[pick] = 1
	}
	return scores
}

// calibAwarePolicy — §5 of the build plan, exactly:
//
//	score = w_q·ESP − w_t·wait_norm − w_c·cost_norm
//
// ESP is computed per target from the current calibration snapshot
// (esp.go); wait_norm = wait/(wait+60s) ∈ [0,1); cost_norm ≡ 0 in v0
// (pricing is an explicit MVP non-goal — the term stays in the formula for
// post-MVP policies, D-022). Weights come from job intent (D-022):
//
//	                     w_q    w_t    w_c
//	no deadline/floor    0.60   0.25   0.15
//	deadline only        0.45   0.45   0.10
//	quality floor only   0.75   0.15   0.10
//	deadline + floor     0.55   0.35   0.10
type calibAwarePolicy struct{ standardFilter }

func (calibAwarePolicy) Name() string { return "calib-aware/v0" }

// Weights returns (w_q, w_t, w_c) for a job's intent.
func (calibAwarePolicy) Weights(j *JobView) (wq, wt, wc float64) {
	deadline := !j.Deadline.IsZero()
	floor := j.HasQualityFloor()
	switch {
	case deadline && floor:
		return 0.55, 0.35, 0.10
	case deadline:
		return 0.45, 0.45, 0.10
	case floor:
		return 0.75, 0.15, 0.10
	default:
		return 0.60, 0.25, 0.15
	}
}

func (p calibAwarePolicy) Score(j *JobView, t *TargetView, _ time.Time) float64 {
	wq, wt, _ := p.Weights(j)
	esp := ESP(profileFor(j), t)
	waitNorm := t.WaitSeconds / (t.WaitSeconds + 60.0)
	return wq*esp - wt*waitNorm
}

// PredictESP exposes the policy's ESP prediction for the placement record.
func (calibAwarePolicy) PredictESP(j *JobView, t *TargetView) float64 {
	return ESP(profileFor(j), t)
}

// profileFor falls back to a width-only estimate when the program could not
// be profiled: a GHZ-like circuit over the required qubits (documented in
// D-021 — pessimistic enough to still rank by calibration).
func profileFor(j *JobView) CircuitProfile {
	if j.Profile != nil {
		return *j.Profile
	}
	width := int(j.Qubits)
	if width == 0 {
		width = 2
	}
	return CircuitProfile{
		Qubits:         width,
		OneQubitGates:  width,
		TwoQubitGates:  width - 1,
		MeasuredQubits: width,
	}
}

func init() {
	Register(fifoPolicy{})
	Register(staticBestPolicy{})
	Register(&roundRobinPolicy{})
	Register(calibAwarePolicy{})
}
