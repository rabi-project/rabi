// SPDX-License-Identifier: Apache-2.0

// Package scheduler implements the policy pipeline: filter → score → bind
// (mvp-build-plan.md §5). Policies implement SchedulingPolicy and register by
// name so reference policies and calib-aware/v0 compare like with like
// inside the same machinery.
package scheduler

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"time"
)

// TargetView is the scheduler's snapshot of one fleet target. It is a plain
// value (no registry dependency) so filters and policies are unit-testable
// and golden scenarios can construct fleets directly.
type TargetView struct {
	Name       string // fleet-scoped "<site>/<target_id>"
	Modality   string
	Technology string // from Capabilities.vendor_extensions["technology"] (D-016)
	Qubits     uint32
	Formats    []string
	MaxShots   uint64
	Billing    []string
	Online     bool
	Cloud      bool // vendor_extensions["cloud"] == "true"

	SnapshotID  string
	MeasuredAt  time.Time
	Metrics     []Metric
	QueueDepth  uint32
	WaitSeconds float64
	Maintenance []Window

	// Nominal2QError is the device's advertised (baseline) median two-qubit
	// error from vendor_extensions["nominal-2q-error-median"] — static per
	// device, used by static-best/v0 (what users pick today). 0 = unknown.
	Nominal2QError float64
}

// Metric is one calibration metric relevant to scheduling.
type Metric struct {
	Name   string
	Value  float64
	Qubits []uint32
}

// Window is a maintenance window.
type Window struct {
	Start, End time.Time
}

// aggregateValues collapses selected error-metric values per RFC-0002:
// "best" = minimum (most favorable for error-type metrics), "worst" =
// maximum, "median" = middle value (lower of the two middles for even
// counts — deterministic). Unknown aggregates fall back to best, which the
// admission schema makes unreachable.
func aggregateValues(values []float64, aggregate string) (float64, bool) {
	if len(values) == 0 {
		return 0, false
	}
	sort.Float64s(values)
	switch aggregate {
	case "worst":
		return values[len(values)-1], true
	case "median":
		return values[(len(values)-1)/2], true
	default: // "best"
		return values[0], true
	}
}

// MetricAggregate returns the aggregate of all values for an exact metric
// name (RFC-0002 evaluation step 1–2).
func (t *TargetView) MetricAggregate(name, aggregate string) (float64, bool) {
	var vals []float64
	for _, m := range t.Metrics {
		if m.Name == name {
			vals = append(vals, m.Value)
		}
	}
	return aggregateValues(vals, aggregate)
}

// TwoQubitErrorAggregate aggregates across any "gate.2q.<gate>.error"
// metric (cx, cz, ecr, …) per D-020.
func (t *TargetView) TwoQubitErrorAggregate(aggregate string) (float64, bool) {
	var vals []float64
	for _, m := range t.Metrics {
		if strings.HasPrefix(m.Name, "gate.2q.") && strings.HasSuffix(m.Name, ".error") {
			vals = append(vals, m.Value)
		}
	}
	return aggregateValues(vals, aggregate)
}

// MinMetric returns the device-best (minimum) value for a metric name — the
// RFC-0002 "best" aggregate (D-016).
func (t *TargetView) MinMetric(name string) (float64, bool) {
	return t.MetricAggregate(name, "best")
}

// MinTwoQubitError is the device-best two-qubit gate error (D-020).
func (t *TargetView) MinTwoQubitError() (float64, bool) {
	return t.TwoQubitErrorAggregate("best")
}

// InMaintenance reports whether now falls inside a maintenance window.
func (t *TargetView) InMaintenance(now time.Time) bool {
	for _, w := range t.Maintenance {
		if !now.Before(w.Start) && now.Before(w.End) {
			return true
		}
	}
	return false
}

// JobView is the scheduler's parsed view of a QuantumJob document.
type JobView struct {
	ID     string
	Tenant string
	Kind   string
	Format string
	Shots  uint64

	Qubits            uint32
	Technology        []string
	TwoQubitErrorMax  float64 // 0 = unset
	ReadoutErrorMax   float64 // 0 = unset
	CalibrationMaxAge time.Duration
	// Aggregate is the RFC-0002 floor-evaluation aggregate: how the many
	// per-qubit/edge metric values collapse to one number before the floor
	// comparison. "best" (normative default) | "median" | "worst".
	Aggregate string
	// OnConflict is the RFC-0003 deadline/floor conflict policy:
	// "prefer-quality" (default) | "prefer-deadline" | "reject".
	OnConflict string

	Deadline    time.Time
	BudgetUnits []string

	// SessionJoin is an existing session id to join (spec.session.join);
	// SessionMaxDuration opens a new session at bind time when set and no
	// join id is given (M6).
	SessionJoin        string
	SessionMaxDuration time.Duration

	PreferOnPrem    bool
	AllowCloudBurst []string
	DenyTargets     []string
	RequireTargets  []string

	// Profile is the deterministic gate-count estimate of the inline program
	// (nil when unavailable — non-gate-model workloads, source URIs, or
	// unprofilable QASM). Policies fall back to a width-only estimate.
	Profile *CircuitProfile
}

// HasQualityFloor reports whether the job sets any quality constraint.
func (j *JobView) HasQualityFloor() bool {
	return j.TwoQubitErrorMax > 0 || j.ReadoutErrorMax > 0
}

// ParseJob extracts the scheduling-relevant fields from a validated document.
func ParseJob(id, tenant string, doc map[string]any) (*JobView, error) {
	spec, _ := doc["spec"].(map[string]any)
	if spec == nil {
		return nil, fmt.Errorf("scheduler: document has no spec")
	}
	workload, _ := spec["workload"].(map[string]any)
	kind, _ := workload["kind"].(string)

	j := &JobView{ID: id, Tenant: tenant, Kind: kind}

	payloadField := map[string]string{
		"gate-model": "gateModel", "analog-hamiltonian": "analogHamiltonian",
		"annealing": "annealing", "pulse": "pulse", "logical": "logical",
	}[kind]
	if payload, ok := workload[payloadField].(map[string]any); ok {
		if program, ok := payload["program"].(map[string]any); ok {
			j.Format, _ = program["format"].(string)
			if inline, ok := program["inline"].(string); ok && kind == "gate-model" {
				if raw, err := base64.StdEncoding.DecodeString(inline); err == nil {
					if profile, err := ProfileQASM(string(raw)); err == nil {
						j.Profile = &profile
					}
				}
			}
		}
		if s, ok := payload["shots"].(float64); ok {
			j.Shots = uint64(s)
		}
	}

	if req, ok := spec["requirements"].(map[string]any); ok {
		if q, ok := req["qubits"].(float64); ok {
			j.Qubits = uint32(q)
		}
		if techs, ok := req["technology"].([]any); ok {
			for _, tech := range techs {
				if s, ok := tech.(string); ok {
					j.Technology = append(j.Technology, s)
				}
			}
		}
		if quality, ok := req["quality"].(map[string]any); ok {
			if gm, ok := quality["gateModel"].(map[string]any); ok {
				if v, ok := gm["twoQubitErrorMax"].(float64); ok {
					j.TwoQubitErrorMax = v
				}
				if v, ok := gm["readoutErrorMax"].(float64); ok {
					j.ReadoutErrorMax = v
				}
				if raw, ok := gm["calibrationMaxAge"].(string); ok {
					d, err := time.ParseDuration(raw)
					if err != nil {
						return nil, fmt.Errorf("scheduler: calibrationMaxAge %q: %w", raw, err)
					}
					j.CalibrationMaxAge = d
				}
				if agg, ok := gm["aggregate"].(string); ok {
					j.Aggregate = agg
				}
			}
		}
	}
	if j.Aggregate == "" {
		j.Aggregate = "best" // RFC-0002 normative default
	}
	if sess, ok := spec["session"].(map[string]any); ok {
		if join, ok := sess["join"].(string); ok {
			j.SessionJoin = join
		}
		if raw, ok := sess["maxDuration"].(string); ok {
			d, err := time.ParseDuration(raw)
			if err != nil {
				return nil, fmt.Errorf("scheduler: session.maxDuration %q: %w", raw, err)
			}
			j.SessionMaxDuration = d
		}
	}
	if sched, ok := spec["scheduling"].(map[string]any); ok {
		if oc, ok := sched["onConflict"].(string); ok {
			j.OnConflict = oc
		}
	}
	if j.OnConflict == "" {
		j.OnConflict = "prefer-quality" // RFC-0003 default = v0 behavior
	}

	if raw, ok := spec["deadline"].(string); ok {
		d, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, fmt.Errorf("scheduler: deadline %q: %w", raw, err)
		}
		j.Deadline = d
	}

	if budget, ok := spec["budget"].(map[string]any); ok {
		if limits, ok := budget["limits"].(map[string]any); ok {
			for unit := range limits {
				j.BudgetUnits = append(j.BudgetUnits, unit)
			}
			sort.Strings(j.BudgetUnits) // deterministic reason strings
		}
	}

	if sel, ok := spec["backendSelector"].(map[string]any); ok {
		j.PreferOnPrem, _ = sel["preferOnPrem"].(bool)
		j.AllowCloudBurst = stringList(sel["allowCloudBurst"])
		j.DenyTargets = stringList(sel["denyTargets"])
		j.RequireTargets = stringList(sel["requireTargets"])
	}
	return j, nil
}

func stringList(v any) []string {
	items, _ := v.([]any)
	var out []string
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
