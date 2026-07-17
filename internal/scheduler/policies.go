// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
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
// static-best/v0, round-robin/v0, and calib-aware/v0 join in M5.
type fifoPolicy struct{ standardFilter }

func (fifoPolicy) Name() string                                   { return "fifo/v0" }
func (fifoPolicy) Score(*JobView, *TargetView, time.Time) float64 { return 0 }

func init() {
	Register(fifoPolicy{})
}
