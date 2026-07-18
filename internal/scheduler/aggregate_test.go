// SPDX-License-Identifier: Apache-2.0

// RFC-0002 conformance: a crafted snapshot with known best/median/worst
// values per metric class; per-aggregate include/exclude with the normative
// rejection-string format (aggregate name + winning value + floor).
package scheduler

import (
	"strings"
	"testing"
	"time"
)

func craftedTarget() *TargetView {
	return &TargetView{
		Name: "site/crafted", Modality: "gate-model", Online: true,
		Qubits: 5, Formats: []string{"openqasm3"},
		MeasuredAt: time.Date(2026, 7, 19, 11, 0, 0, 0, time.UTC),
		Metrics: []Metric{
			{Name: "gate.2q.cx.error", Value: 0.001, Qubits: []uint32{0, 1}},
			{Name: "gate.2q.cx.error", Value: 0.005, Qubits: []uint32{1, 2}},
			{Name: "gate.2q.cx.error", Value: 0.020, Qubits: []uint32{2, 3}},
			{Name: "readout.error", Value: 0.010, Qubits: []uint32{0}},
			{Name: "readout.error", Value: 0.030, Qubits: []uint32{1}},
		},
	}
}

func TestAggregateFloorSemantics(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		aggregate string
		floor     float64
		feasible  bool
		reason    string // "" for feasible; normative format otherwise
	}{
		// best = 0.001, median = 0.005, worst = 0.020
		{"best", 0.006, true, ""},
		{"median", 0.006, true, ""},
		{"worst", 0.006, false, "worst two-qubit error 0.02 exceeds floor 0.006"},
		{"best", 0.004, true, ""},
		{"median", 0.004, false, "median two-qubit error 0.005 exceeds floor 0.004"},
		{"worst", 0.004, false, "worst two-qubit error 0.02 exceeds floor 0.004"},
		{"best", 0.0005, false, "best two-qubit error 0.001 exceeds floor 0.0005"},
		// Unset defaults to best (normative default).
		{"", 0.006, true, ""},
	}
	for _, c := range cases {
		j := &JobView{
			Kind: "gate-model", Format: "openqasm3",
			TwoQubitErrorMax: c.floor, Aggregate: c.aggregate,
		}
		got := FilterTarget(j, craftedTarget(), now)
		if c.feasible && got != "" {
			t.Errorf("agg=%q floor=%g: want feasible, got %q", c.aggregate, c.floor, got)
		}
		if !c.feasible && got != c.reason {
			t.Errorf("agg=%q floor=%g: reason = %q, want %q", c.aggregate, c.floor, got, c.reason)
		}
	}

	// Readout floors use the same aggregate: median readout = 0.03 (lower
	// middle of two values is 0.010 per deterministic rule — even count
	// takes values[(n-1)/2] = 0.010).
	j := &JobView{Kind: "gate-model", ReadoutErrorMax: 0.02, Aggregate: "median"}
	if got := FilterTarget(j, craftedTarget(), now); got != "" {
		t.Errorf("median readout of [0.01 0.03] is 0.01, must pass floor 0.02: %q", got)
	}
	j.Aggregate = "worst"
	want := "worst readout error 0.03 exceeds floor 0.02"
	if got := FilterTarget(j, craftedTarget(), now); got != want {
		t.Errorf("worst readout: %q, want %q", got, want)
	}
}

func TestParseJobAggregateAndOnConflict(t *testing.T) {
	doc := map[string]any{
		"spec": map[string]any{
			"workload": map[string]any{"kind": "gate-model"},
			"requirements": map[string]any{
				"quality": map[string]any{
					"gateModel": map[string]any{
						"twoQubitErrorMax": 0.006,
						"aggregate":        "median",
					},
				},
			},
			"scheduling": map[string]any{"onConflict": "prefer-deadline"},
		},
	}
	j, err := ParseJob("id", "t", doc)
	if err != nil {
		t.Fatal(err)
	}
	if j.Aggregate != "median" || j.OnConflict != "prefer-deadline" {
		t.Fatalf("parsed aggregate=%q onConflict=%q", j.Aggregate, j.OnConflict)
	}
	// Defaults.
	j2, err := ParseJob("id", "t", map[string]any{
		"spec": map[string]any{"workload": map[string]any{"kind": "gate-model"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if j2.Aggregate != "best" || j2.OnConflict != "prefer-quality" {
		t.Fatalf("defaults: aggregate=%q onConflict=%q", j2.Aggregate, j2.OnConflict)
	}
	if !strings.HasPrefix("best", j2.Aggregate) {
		t.Fatal("unreachable")
	}
}
