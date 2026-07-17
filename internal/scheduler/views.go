// SPDX-License-Identifier: Apache-2.0

// Package scheduler implements the policy pipeline: filter → score → bind
// (mvp-build-plan.md §5). Policies implement SchedulingPolicy and register by
// name so reference policies and calib-aware/v0 compare like with like
// inside the same machinery.
package scheduler

import (
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

// MinMetric returns the device-best (minimum) value for a metric name, and
// whether any value exists. Floors are evaluated against the device's best
// value: a device is feasible if some qubit/edge meets the floor (D-016).
func (t *TargetView) MinMetric(name string) (float64, bool) {
	best, found := 0.0, false
	for _, m := range t.Metrics {
		if m.Name != name {
			continue
		}
		if !found || m.Value < best {
			best, found = m.Value, true
		}
	}
	return best, found
}

// MinTwoQubitError is the device-best two-qubit gate error regardless of the
// native gate: it matches any "gate.2q.<gate>.error" metric (cx, cz, ecr, …)
// per D-020.
func (t *TargetView) MinTwoQubitError() (float64, bool) {
	best, found := 0.0, false
	for _, m := range t.Metrics {
		if !strings.HasPrefix(m.Name, "gate.2q.") || !strings.HasSuffix(m.Name, ".error") {
			continue
		}
		if !found || m.Value < best {
			best, found = m.Value, true
		}
	}
	return best, found
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

	Deadline    time.Time
	BudgetUnits []string

	PreferOnPrem    bool
	AllowCloudBurst []string
	DenyTargets     []string
	RequireTargets  []string
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
			}
		}
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
