// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// SchedulingPolicy is the pluggable policy contract (mvp-build-plan.md §5).
// Filter narrows the fleet; Score ranks the survivors (higher is better).
type SchedulingPolicy interface {
	Name() string
	Filter(j *JobView, t *TargetView, now time.Time) string // "" = feasible
	Score(j *JobView, t *TargetView, now time.Time) float64
}

// registry of policies by name.
var policies = map[string]SchedulingPolicy{}

// Register adds a policy; duplicate names are a programming error.
func Register(p SchedulingPolicy) {
	if _, dup := policies[p.Name()]; dup {
		panic(fmt.Sprintf("scheduler: duplicate policy %q", p.Name()))
	}
	policies[p.Name()] = p
}

// Lookup returns a registered policy.
func Lookup(name string) (SchedulingPolicy, error) {
	p, ok := policies[name]
	if !ok {
		var known []string
		for n := range policies {
			known = append(known, n)
		}
		sort.Strings(known)
		return nil, fmt.Errorf("scheduler: unknown policy %q (registered: %s)",
			name, strings.Join(known, ", "))
	}
	return p, nil
}

// Decision is the outcome of one scheduling attempt for one job. Reason is
// human-readable and lists every filtered target — this is the audit trail
// that makes scheduling arguable.
type Decision struct {
	Policy      string
	Target      string // "" when infeasible
	Score       float64
	SnapshotID  string
	WaitSeconds float64
	// PredictedESP is the estimated success probability, set by policies
	// that compute one (calib-aware/v0, M5). 0 = no prediction.
	PredictedESP float64
	Reason       string
	Rejected     []Rejection
}

// Schedule runs the pipeline for one job over the fleet. Targets are
// evaluated in stable name order; ties break toward the lexicographically
// first name, so decisions are deterministic.
func Schedule(p SchedulingPolicy, j *JobView, fleet []*TargetView, now time.Time) Decision {
	sorted := make([]*TargetView, len(fleet))
	copy(sorted, fleet)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].Name < sorted[b].Name })

	d := Decision{Policy: p.Name()}
	var best *TargetView
	feasible := 0
	for _, t := range sorted {
		if reason := p.Filter(j, t, now); reason != "" {
			d.Rejected = append(d.Rejected, Rejection{Target: t.Name, Reason: reason})
			continue
		}
		feasible++
		score := p.Score(j, t, now)
		if best == nil || score > d.Score {
			best, d.Score = t, score
		}
	}

	var b strings.Builder
	if best != nil {
		d.Target = best.Name
		d.SnapshotID = best.SnapshotID
		d.WaitSeconds = best.WaitSeconds
		fmt.Fprintf(&b, "policy %s selected %s (score %.4f) among %d feasible target(s)",
			p.Name(), best.Name, d.Score, feasible)
	} else {
		fmt.Fprintf(&b, "policy %s found no feasible target among %d", p.Name(), len(sorted))
	}
	if len(d.Rejected) > 0 {
		b.WriteString("; filtered: ")
		for i, r := range d.Rejected {
			if i > 0 {
				b.WriteString("; ")
			}
			fmt.Fprintf(&b, "%s (%s)", r.Target, r.Reason)
		}
	}
	d.Reason = b.String()
	return d
}

// PlacementRecord renders the decision as the status.placement document
// (spec/spec/quantumjob.md). The rejected list rides along so audits never
// need to re-derive it from the prose.
func (d Decision) PlacementRecord() map[string]any {
	rejected := make([]any, 0, len(d.Rejected))
	for _, r := range d.Rejected {
		rejected = append(rejected, map[string]any{"target": r.Target, "reason": r.Reason})
	}
	predicted := map[string]any{"waitSeconds": d.WaitSeconds}
	if d.PredictedESP > 0 {
		predicted["successProbability"] = d.PredictedESP
	}
	return map[string]any{
		"policy":              d.Policy,
		"calibrationSnapshot": d.SnapshotID,
		"predicted":           predicted,
		"reason":              d.Reason,
		"rejected":            rejected,
	}
}
