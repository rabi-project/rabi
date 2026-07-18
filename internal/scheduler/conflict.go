// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"time"
)

// RFC-0003: when a quality floor and a deadline cannot both be satisfied,
// the resolution is the user's declared choice (spec.scheduling.onConflict).
// This file provides the floor-relaxed scheduling pass and the decision
// horizon; the dispatcher owns the mode semantics.

// FloorViolation names one floor a prefer-deadline placement relaxes,
// with the actual aggregate value on the chosen target.
type FloorViolation struct {
	Floor     string  // "twoQubitErrorMax" | "readoutErrorMax"
	Limit     float64 // the declared floor
	Actual    float64 // the aggregate value on the bound target
	Aggregate string  // RFC-0002 aggregate in effect
}

// ScheduleRelaxed runs the policy pipeline with quality floors cleared
// (everything else — capability, selector, calibration age — still binds)
// and reports which of the job's floors the chosen target violates.
// No violations means floors were not the binding constraint.
func ScheduleRelaxed(p SchedulingPolicy, j *JobView, fleet []*TargetView, now time.Time) (Decision, []FloorViolation) {
	relaxed := *j
	relaxed.TwoQubitErrorMax, relaxed.ReadoutErrorMax = 0, 0
	d := Schedule(p, &relaxed, fleet, now)
	if d.Target == "" {
		return d, nil
	}
	var chosen *TargetView
	for _, t := range fleet {
		if t.Name == d.Target {
			chosen = t
			break
		}
	}
	if chosen == nil {
		return d, nil
	}
	agg := j.Aggregate
	if agg == "" {
		agg = "best"
	}
	var violations []FloorViolation
	if j.TwoQubitErrorMax > 0 {
		if v, ok := chosen.TwoQubitErrorAggregate(agg); ok && v > j.TwoQubitErrorMax {
			violations = append(violations, FloorViolation{
				Floor: "twoQubitErrorMax", Limit: j.TwoQubitErrorMax, Actual: v, Aggregate: agg,
			})
		}
	}
	if j.ReadoutErrorMax > 0 {
		if v, ok := chosen.MetricAggregate("readout.error", agg); ok && v > j.ReadoutErrorMax {
			violations = append(violations, FloorViolation{
				Floor: "readoutErrorMax", Limit: j.ReadoutErrorMax, Actual: v, Aggregate: agg,
			})
		}
	}
	return d, violations
}

// DecisionHorizon is the latest placement time that still meets the
// deadline. The model is implementation-defined but MUST be recorded
// (RFC-0003 unresolved question): here it is deadline − predicted wait on
// the relaxed-feasible choice, with a zero execution estimate.
const HorizonModel = "deadline minus predicted wait; execution estimate 0"

// DecisionHorizon computes the horizon for a relaxed decision.
func DecisionHorizon(j *JobView, relaxed Decision) time.Time {
	wait := time.Duration(relaxed.WaitSeconds * float64(time.Second))
	return j.Deadline.Add(-wait)
}
