// SPDX-License-Identifier: Apache-2.0

package dispatch

import (
	"context"
	"sort"

	"github.com/rabi-project/rabi/internal/store"
)

// fairOrder arranges one cycle's pending jobs by weighted deficit
// round-robin over projects (phase1-build-plan.md M2: fair-share weights
// consumed by the scheduler's priority ordering). Each step picks the
// project with the smallest (assigned+1)/weight virtual time — ties break
// by tenant string, then FIFO within a project — so two projects at 3:1
// weights bind in an exact A,A,A,B cadence under contention. The deficit
// resets every cycle: fairness is over the live contention window, not
// all-time history (docs/decisions.md D-036).
func fairOrder(pending []*store.JobRecord, weights map[string]int) []*store.JobRecord {
	if len(pending) <= 1 {
		return pending
	}
	queues := map[string][]*store.JobRecord{}
	var tenants []string
	for _, rec := range pending { // PendingJobs is oldest-first: FIFO per queue
		if _, seen := queues[rec.Tenant]; !seen {
			tenants = append(tenants, rec.Tenant)
		}
		queues[rec.Tenant] = append(queues[rec.Tenant], rec)
	}
	sort.Strings(tenants)

	weightOf := func(tenant string) float64 {
		if w := weights[tenant]; w >= 1 {
			return float64(w)
		}
		return 1
	}

	assigned := map[string]int{}
	out := make([]*store.JobRecord, 0, len(pending))
	for len(out) < len(pending) {
		best := ""
		bestKey := 0.0
		for _, tenant := range tenants {
			if assigned[tenant] == len(queues[tenant]) {
				continue
			}
			key := float64(assigned[tenant]+1) / weightOf(tenant)
			if best == "" || key < bestKey {
				best, bestKey = tenant, key
			}
		}
		out = append(out, queues[best][assigned[best]])
		assigned[best]++
	}
	return out
}

// orderPending applies fair-share ordering using stored project weights.
// On weight-lookup failure the cycle proceeds FIFO — scheduling must not
// stall on tenancy metadata.
func (d *Dispatcher) orderPending(ctx context.Context, pending []*store.JobRecord) []*store.JobRecord {
	if len(pending) <= 1 {
		return pending
	}
	seen := map[string]bool{}
	var tenants []string
	for _, rec := range pending {
		if !seen[rec.Tenant] {
			seen[rec.Tenant] = true
			tenants = append(tenants, rec.Tenant)
		}
	}
	if len(tenants) == 1 {
		return pending
	}
	weights, err := d.store.ProjectWeights(ctx, tenants)
	if err != nil {
		d.logger.Error("loading project weights; falling back to FIFO", "error", err)
		return pending
	}
	return fairOrder(pending, weights)
}
