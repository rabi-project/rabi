// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"math"
	"time"
)

// This file holds ABSORBED policies: implementations of published scheduling
// ideas from the literature, adapted to Rabi's per-job placement interface.
// They are candidates only — shipped shadow-only, never the default without the
// M5 promotion pipeline's evidence (phase2-build-plan.md P2.M6). Each carries
// its attribution here, and docs/scheduling-policies.md records the fuller
// citation and a "differences from the paper" section.

// paretoPolicy — pareto/v0.
//
// Lineage: Qonductor (Giortamis et al., "Qonductor: A Cloud Orchestrator for
// Quantum Computing", 2024), which selects executions by multi-objective
// optimization (NSGA-II) trading execution time against fidelity.
//
// Differences from the paper (see docs/scheduling-policies.md): Qonductor
// evolves a POPULATION of whole-schedule candidates with NSGA-II's genetic
// operators. Rabi places one job at a time, so this policy keeps NSGA-II's
// non-dominated *sorting* over the feasible targets — objectives (fidelity ↑,
// wait/completion-time ↓) — and selects deterministically from the first
// Pareto front by a balanced scalarization, rather than running a genetic
// search. No crossover/mutation, no population; the multi-objective ranking is
// faithful, the evolutionary machinery is not (and would be nondeterministic).
type paretoPolicy struct{ standardFilter }

func (paretoPolicy) Name() string { return "pareto/v0" }

// Score is unused for selection (ScoreSet dominates) but must exist to satisfy
// the interface; return 0 so a lone target is trivially chosen.
func (paretoPolicy) Score(*JobView, *TargetView, time.Time) float64 { return 0 }

func (p paretoPolicy) ScoreSet(j *JobView, feasible []*TargetView, _ time.Time) []float64 {
	n := len(feasible)
	scores := make([]float64, n)
	if n == 0 {
		return scores
	}
	// Two objectives, both maximized: fidelity proxy, and speed (= −wait).
	fid := make([]float64, n)
	speed := make([]float64, n)
	for i, t := range feasible {
		fid[i] = ESP(profileFor(j), t)
		speed[i] = -t.WaitSeconds
	}
	front := nonDominatedFronts(fid, speed) // front index per target (0 = best)
	fidN := normalize(fid)
	speedN := normalize(speed)
	for i := range scores {
		// Front rank dominates; the balanced scalarization (in [0,0.5]) only
		// breaks ties within a front, never crosses fronts.
		scores[i] = -float64(front[i]) + (fidN[i]+speedN[i])/4.0
	}
	return scores
}

// dominates reports whether (f1,s1) Pareto-dominates (f2,s2): at least as good
// on both objectives and strictly better on one.
func dominates(f1, s1, f2, s2 float64) bool {
	return f1 >= f2 && s1 >= s2 && (f1 > f2 || s1 > s2)
}

// nonDominatedFronts assigns each point its NSGA-II front index (0 = Pareto
// front). Deterministic O(n²) fast-non-dominated-sort.
func nonDominatedFronts(f, s []float64) []int {
	n := len(f)
	front := make([]int, n)
	dominatedCount := make([]int, n)
	dominatesList := make([][]int, n)
	for i := 0; i < n; i++ {
		for k := 0; k < n; k++ {
			if k == i {
				continue
			}
			if dominates(f[i], s[i], f[k], s[k]) {
				dominatesList[i] = append(dominatesList[i], k)
			} else if dominates(f[k], s[k], f[i], s[i]) {
				dominatedCount[i]++
			}
		}
	}
	current := []int{}
	for i := 0; i < n; i++ {
		if dominatedCount[i] == 0 {
			front[i] = 0
			current = append(current, i)
		}
	}
	rank := 0
	for len(current) > 0 {
		var next []int
		for _, i := range current {
			for _, k := range dominatesList[i] {
				dominatedCount[k]--
				if dominatedCount[k] == 0 {
					front[k] = rank + 1
					next = append(next, k)
				}
			}
		}
		rank++
		current = next
	}
	return front
}

func normalize(xs []float64) []float64 {
	out := make([]float64, len(xs))
	if len(xs) == 0 {
		return out
	}
	lo, hi := xs[0], xs[0]
	for _, x := range xs {
		lo = math.Min(lo, x)
		hi = math.Max(hi, x)
	}
	if hi == lo {
		for i := range out {
			out[i] = 1
		}
		return out
	}
	for i, x := range xs {
		out[i] = (x - lo) / (hi - lo)
	}
	return out
}

// adaptiveDeferralPolicy — adaptive-deferral/v0.
//
// Lineage: Ravi, Smith, Gokhale, Chong et al. ("Adaptive Job and Resource
// Management for the Quantum Cloud" / calibration-aware scheduling, 2021),
// whose central observation is that device error rates drift between
// recalibrations, so scheduling should be aware of the calibration window and
// prefer freshly-calibrated devices (deferring onto a better window when it
// helps a quality-sensitive job).
//
// Differences from the paper (see docs/scheduling-policies.md): the paper
// operates a full adaptive manager that can DEFER a job in wall-clock time to
// await a recalibration. Rabi's placement interface chooses among currently-
// feasible targets; the existing calibrationMaxAge filter already defers a
// floor'd job (it stays PENDING when every target is too stale), so this policy
// contributes the *ranking*: weight the fidelity proxy by calibration
// freshness (exponential age decay over the job's tolerance window), strongly
// preferring fresh calibration. It does not itself schedule a future wake-up;
// that remains the dispatcher's re-cycle behavior.
type adaptiveDeferralPolicy struct{ standardFilter }

func (adaptiveDeferralPolicy) Name() string { return "adaptive-deferral/v0" }

// defaultCalibrationWindow is the freshness half-life when a job declares no
// calibration tolerance of its own.
const defaultCalibrationWindow = 30 * time.Minute

func (adaptiveDeferralPolicy) Score(j *JobView, t *TargetView, now time.Time) float64 {
	esp := ESP(profileFor(j), t)
	window := defaultCalibrationWindow
	if j.CalibrationMaxAge > 0 {
		window = j.CalibrationMaxAge
	}
	fresh := 0.5 // unknown calibration age: neutral, neither preferred nor punished
	if !t.MeasuredAt.IsZero() {
		age := now.Sub(t.MeasuredAt)
		if age < 0 {
			age = 0
		}
		fresh = math.Exp(-float64(age) / float64(window))
	}
	return esp * fresh
}

// PredictESP surfaces the fidelity proxy for the placement record, like
// calib-aware/v0 does.
func (adaptiveDeferralPolicy) PredictESP(j *JobView, t *TargetView) float64 {
	return ESP(profileFor(j), t)
}

func init() {
	Register("pareto/v0", func() SchedulingPolicy { return paretoPolicy{} })
	Register("adaptive-deferral/v0", func() SchedulingPolicy { return adaptiveDeferralPolicy{} })
}
