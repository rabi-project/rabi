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

// factories build policies by name; the process-wide singletons used by
// tangled are cached separately (stateful policies like round-robin need
// fresh instances in benchmark runs).
var (
	factories  = map[string]func() SchedulingPolicy{}
	singletons = map[string]SchedulingPolicy{}
)

// Register adds a policy factory; duplicate names are a programming error.
func Register(name string, factory func() SchedulingPolicy) {
	if _, dup := factories[name]; dup {
		panic(fmt.Sprintf("scheduler: duplicate policy %q", name))
	}
	factories[name] = factory
}

// NewPolicy returns a fresh instance of the named policy.
func NewPolicy(name string) (SchedulingPolicy, error) {
	f, ok := factories[name]
	if !ok {
		var known []string
		for n := range factories {
			known = append(known, n)
		}
		sort.Strings(known)
		return nil, fmt.Errorf("scheduler: unknown policy %q (registered: %s)",
			name, strings.Join(known, ", "))
	}
	return f(), nil
}

// Lookup returns the process-wide singleton of the named policy.
func Lookup(name string) (SchedulingPolicy, error) {
	if p, ok := singletons[name]; ok {
		return p, nil
	}
	p, err := NewPolicy(name)
	if err != nil {
		return nil, err
	}
	singletons[name] = p
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

// SetScorer is an optional policy extension for policies that need the whole
// feasible set at once (e.g. round-robin rotation).
type SetScorer interface {
	ScoreSet(j *JobView, feasible []*TargetView, now time.Time) []float64
}

// ESPPredictor is an optional policy extension: policies that estimate a
// success probability expose it for the placement audit record.
type ESPPredictor interface {
	PredictESP(j *JobView, t *TargetView) float64
}

// Schedule runs the pipeline for one job over the fleet. Targets are
// evaluated in stable name order; ties break toward the lexicographically
// first name, so decisions are deterministic.
func Schedule(p SchedulingPolicy, j *JobView, fleet []*TargetView, now time.Time) Decision {
	sorted := make([]*TargetView, len(fleet))
	copy(sorted, fleet)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].Name < sorted[b].Name })

	d := Decision{Policy: p.Name()}
	var feasibleTargets []*TargetView
	for _, t := range sorted {
		if reason := p.Filter(j, t, now); reason != "" {
			d.Rejected = append(d.Rejected, Rejection{Target: t.Name, Reason: reason})
			continue
		}
		feasibleTargets = append(feasibleTargets, t)
	}

	var scores []float64
	if setScorer, ok := p.(SetScorer); ok {
		scores = setScorer.ScoreSet(j, feasibleTargets, now)
	} else {
		scores = make([]float64, len(feasibleTargets))
		for i, t := range feasibleTargets {
			scores[i] = p.Score(j, t, now)
		}
	}

	var best *TargetView
	feasible := len(feasibleTargets)
	for i, t := range feasibleTargets {
		if best == nil || scores[i] > d.Score {
			best, d.Score = t, scores[i]
		}
	}
	if best != nil {
		if predictor, ok := p.(ESPPredictor); ok {
			d.PredictedESP = predictor.PredictESP(j, best)
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
