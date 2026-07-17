// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Rejection records why one target was filtered — the audit trail lists
// every filtered target with its reason (spec quantumjob.md §status.placement).
type Rejection struct {
	Target string
	Reason string
}

// FilterTarget applies the standard requirement dimensions (mvp-build-plan.md
// §5 step 1) to one target. It returns "" when feasible, or the precise
// rejection reason. Every dimension has a fixed reason format — golden tests
// and T3.filter assert these strings.
func FilterTarget(j *JobView, t *TargetView, now time.Time) string {
	return filterTarget(j, t, now, true)
}

// FilterCapabilityOnly checks the hard capability/selector dimensions but
// skips calibration-derived ones (quality floors, calibrationMaxAge). The
// baseline policies use it: current practice cannot act on calibration
// intent, which is precisely what the benchmark measures (D-024).
func FilterCapabilityOnly(j *JobView, t *TargetView, now time.Time) string {
	return filterTarget(j, t, now, false)
}

func filterTarget(j *JobView, t *TargetView, now time.Time, quality bool) string {
	if !t.Online {
		return "target not online"
	}
	if t.InMaintenance(now) {
		return "in maintenance window"
	}
	if t.Modality != j.Kind {
		return fmt.Sprintf("modality %s does not match workload kind %s", t.Modality, j.Kind)
	}
	if j.Format != "" && !slices.Contains(t.Formats, j.Format) {
		return fmt.Sprintf("program format %s not supported (offers: %s)",
			j.Format, strings.Join(t.Formats, ", "))
	}
	if j.Qubits > 0 && j.Qubits > t.Qubits {
		return fmt.Sprintf("requires %d qubits, target has %d", j.Qubits, t.Qubits)
	}
	if j.Shots > 0 && t.MaxShots > 0 && j.Shots > t.MaxShots {
		return fmt.Sprintf("requires %d shots, target caps at %d", j.Shots, t.MaxShots)
	}
	if len(j.Technology) > 0 && !slices.Contains(j.Technology, t.Technology) {
		return fmt.Sprintf("technology %s not in required set [%s]",
			t.Technology, strings.Join(j.Technology, ", "))
	}

	// Quality floors are evaluated against the current calibration snapshot.
	if quality {
		if reason := qualityFloors(j, t, now); reason != "" {
			return reason
		}
	}

	// backendSelector narrows the feasible set; it can never widen it.
	if slices.Contains(j.DenyTargets, t.Name) {
		return "excluded by backendSelector.denyTargets"
	}
	if len(j.RequireTargets) > 0 && !slices.Contains(j.RequireTargets, t.Name) {
		return "not in backendSelector.requireTargets"
	}
	if t.Cloud && !slices.Contains(j.AllowCloudBurst, t.Name) {
		return "cloud target not in backendSelector.allowCloudBurst"
	}

	// Budget-unit sanity: a native-unit cap the target cannot meter is not
	// enforceable there.
	for _, unit := range j.BudgetUnits {
		if !slices.Contains(t.Billing, unit) {
			return fmt.Sprintf("budget limit unit %s not metered by target (bills: %s)",
				unit, strings.Join(t.Billing, ", "))
		}
	}
	return ""
}

func qualityFloors(j *JobView, t *TargetView, now time.Time) string {
	if j.TwoQubitErrorMax > 0 {
		v, ok := t.MinTwoQubitError()
		if !ok {
			return "no two-qubit error metric in calibration snapshot"
		}
		if v > j.TwoQubitErrorMax {
			return fmt.Sprintf("best two-qubit error %.4g exceeds floor %.4g", v, j.TwoQubitErrorMax)
		}
	}
	if j.ReadoutErrorMax > 0 {
		v, ok := t.MinMetric("readout.error")
		if !ok {
			return "no readout error metric in calibration snapshot"
		}
		if v > j.ReadoutErrorMax {
			return fmt.Sprintf("best readout error %.4g exceeds floor %.4g", v, j.ReadoutErrorMax)
		}
	}
	if j.CalibrationMaxAge > 0 {
		age := now.Sub(t.MeasuredAt)
		if t.MeasuredAt.IsZero() || age > j.CalibrationMaxAge {
			return fmt.Sprintf("calibration age %s exceeds calibrationMaxAge %s",
				age.Truncate(time.Minute), j.CalibrationMaxAge)
		}
	}
	return ""
}
