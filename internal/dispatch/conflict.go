// SPDX-License-Identifier: Apache-2.0

package dispatch

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rabi-project/rabi/internal/job"
	"github.com/rabi-project/rabi/internal/scheduler"
	"github.com/rabi-project/rabi/internal/store"
)

// resolveConflict implements RFC-0003 for a job the normal pipeline could
// not place. It reports true when it fully handled the job this cycle
// (bound it, failed it, or the conflict machinery owns the wait); false
// hands back to the ordinary infeasibility path.
func (d *Dispatcher) resolveConflict(ctx context.Context, rec *store.JobRecord, j *scheduler.JobView, normal scheduler.Decision) bool {
	if !j.HasQualityFloor() || j.Deadline.IsZero() {
		return false // RFC-0003 only concerns floor+deadline jobs
	}
	now := d.now()
	relaxed, violations := scheduler.ScheduleRelaxed(d.policy, j, d.fleetViews(), now)
	if relaxed.Target == "" || len(violations) == 0 {
		// Floors are not the binding constraint (nothing is feasible even
		// without them, or relaxing changed nothing): a plain infeasibility.
		return false
	}
	horizon := scheduler.DecisionHorizon(j, relaxed)

	switch j.OnConflict {
	case "prefer-deadline":
		if now.Before(horizon) {
			return false // still time for a calibration event to fix the floors
		}
		placement := relaxed.PlacementRecord()
		placement["onConflict"] = "prefer-deadline"
		placement["floorsRelaxed"] = true
		placement["decisionHorizon"] = horizon.UTC().Format(time.RFC3339)
		placement["horizonModel"] = scheduler.HorizonModel
		relaxedFloors := make([]any, 0, len(violations))
		for _, v := range violations {
			relaxedFloors = append(relaxedFloors, map[string]any{
				"floor": v.Floor, "limit": v.Limit, "actual": v.Actual, "aggregate": v.Aggregate,
			})
		}
		placement["relaxedFloors"] = relaxedFloors

		taskID := uuid.NewString()
		bound, err := d.store.BindJob(ctx, rec.JobID, taskID, relaxed.Target, placement)
		if err != nil {
			d.logger.Debug("relaxed bind skipped", "job", rec.JobID, "cause", err)
			return true
		}
		d.logger.Info("job bound with floors relaxed (prefer-deadline)",
			"job", rec.JobID, "target", relaxed.Target, "violations", len(violations))
		d.startExecutor(ctx, bound, taskID, relaxed.Target)
		return true

	case "reject":
		if now.Before(horizon) {
			return false
		}
		names := make([]string, 0, len(violations))
		for _, v := range violations {
			names = append(names, fmt.Sprintf("%s (%s %.4g > %.4g)", v.Floor, v.Aggregate, v.Actual, v.Limit))
		}
		msg := fmt.Sprintf("quality floor and deadline are unsatisfiable together: %s cannot be met before %s",
			strings.Join(names, ", "), j.Deadline.UTC().Format(time.RFC3339))
		_, err := d.store.TransitionJob(ctx, rec.JobID, job.Failed, func(st map[string]any) map[string]any {
			conditions, _ := st["conditions"].([]any)
			st["conditions"] = append(conditions, map[string]any{
				"type": "UnsatisfiableBeforeDeadline", "status": "True",
				"reason": "OnConflictReject", "message": msg,
			})
			st["error"] = map[string]any{
				"category": "CAPABILITY_MISMATCH", "retriable": true, "message": msg,
			}
			return st
		})
		if err != nil {
			d.logger.Error("onConflict reject transition", "job", rec.JobID, "error", err)
		} else {
			d.logger.Info("job rejected at decision horizon (onConflict=reject)", "job", rec.JobID)
		}
		return true

	default: // "prefer-quality" — v0 behavior, plus the explicit condition
		if now.After(j.Deadline) {
			_, err := d.store.SetJobCondition(ctx, rec.JobID, map[string]any{
				"type": "DeadlineExceededWaitingForQuality", "status": "True",
				"reason": "PreferQuality",
				"message": fmt.Sprintf("deadline %s passed while waiting for quality floors; onConflict=prefer-quality keeps waiting",
					j.Deadline.UTC().Format(time.RFC3339)),
			})
			if err != nil {
				d.logger.Error("recording deadline-exceeded condition", "job", rec.JobID, "error", err)
			}
		}
		return false // the ordinary NoFeasibleTarget condition still applies
	}
}
