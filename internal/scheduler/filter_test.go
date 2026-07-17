// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

func baseTarget() *TargetView {
	return &TargetView{
		Name:       "sim/alpha",
		Modality:   "gate-model",
		Technology: "superconducting",
		Qubits:     5,
		Formats:    []string{"openqasm3"},
		MaxShots:   100000,
		Billing:    []string{"shots", "tasks"},
		Online:     true,
		SnapshotID: "snap-a",
		MeasuredAt: now.Add(-1 * time.Hour),
		Metrics: []Metric{
			{Name: "gate.2q.cx.error", Value: 0.008, Qubits: []uint32{0, 1}},
			{Name: "gate.2q.cx.error", Value: 0.012, Qubits: []uint32{1, 2}},
			{Name: "readout.error", Value: 0.02, Qubits: []uint32{0}},
			{Name: "readout.error", Value: 0.03, Qubits: []uint32{1}},
		},
	}
}

func baseJob() *JobView {
	return &JobView{
		ID: "job-1", Tenant: "acme", Kind: "gate-model",
		Format: "openqasm3", Shots: 1000, Qubits: 2,
	}
}

// T3.filter — every requirement dimension: ≥1 include + 1 exclude case with
// the expected rejection reason.
func TestFilterDimensions(t *testing.T) {
	cases := []struct {
		name   string
		job    func(*JobView)
		target func(*TargetView)
		want   string // "" = feasible; else substring of the rejection reason
	}{
		{"baseline feasible", nil, nil, ""},

		{"offline excluded", nil, func(tv *TargetView) { tv.Online = false }, "not online"},

		{"maintenance excluded", nil, func(tv *TargetView) {
			tv.Maintenance = []Window{{Start: now.Add(-time.Hour), End: now.Add(time.Hour)}}
		}, "maintenance window"},
		{"maintenance passed ok", nil, func(tv *TargetView) {
			tv.Maintenance = []Window{{Start: now.Add(-2 * time.Hour), End: now.Add(-time.Hour)}}
		}, ""},

		{"modality mismatch", func(j *JobView) { j.Kind = "annealing" }, nil,
			"modality gate-model does not match workload kind annealing"},

		{"format unsupported", func(j *JobView) { j.Format = "qir" }, nil,
			"program format qir not supported"},
		{"format listed ok", nil, func(tv *TargetView) {
			tv.Formats = []string{"qir", "openqasm3"}
		}, ""},

		{"qubit count excluded", func(j *JobView) { j.Qubits = 6 }, nil,
			"requires 6 qubits, target has 5"},
		{"qubit count exact ok", func(j *JobView) { j.Qubits = 5 }, nil, ""},

		{"shots above cap", func(j *JobView) { j.Shots = 200000 }, nil,
			"requires 200000 shots, target caps at 100000"},

		{"technology excluded", func(j *JobView) { j.Technology = []string{"trapped-ion"} }, nil,
			"technology superconducting not in required set [trapped-ion]"},
		{"technology in set ok", func(j *JobView) {
			j.Technology = []string{"superconducting", "trapped-ion"}
		}, nil, ""},

		{"two-qubit floor excluded", func(j *JobView) { j.TwoQubitErrorMax = 0.006 }, nil,
			"best two-qubit error 0.008 exceeds floor 0.006"},
		{"two-qubit floor met (best edge)", func(j *JobView) { j.TwoQubitErrorMax = 0.01 }, nil, ""},
		{"two-qubit floor without metric", func(j *JobView) { j.TwoQubitErrorMax = 0.01 },
			func(tv *TargetView) { tv.Metrics = nil }, "no two-qubit error metric"},

		{"readout floor excluded", func(j *JobView) { j.ReadoutErrorMax = 0.01 }, nil,
			"best readout error 0.02 exceeds floor 0.01"},
		{"readout floor met", func(j *JobView) { j.ReadoutErrorMax = 0.025 }, nil, ""},

		{"calibration too old", func(j *JobView) { j.CalibrationMaxAge = 30 * time.Minute }, nil,
			"calibration age 1h0m0s exceeds calibrationMaxAge 30m0s"},
		{"calibration fresh enough", func(j *JobView) { j.CalibrationMaxAge = 2 * time.Hour }, nil, ""},

		{"denyTargets excluded", func(j *JobView) { j.DenyTargets = []string{"sim/alpha"} }, nil,
			"excluded by backendSelector.denyTargets"},
		{"denyTargets other ok", func(j *JobView) { j.DenyTargets = []string{"sim/beta"} }, nil, ""},

		{"requireTargets excluded", func(j *JobView) { j.RequireTargets = []string{"sim/beta"} }, nil,
			"not in backendSelector.requireTargets"},
		{"requireTargets included ok", func(j *JobView) { j.RequireTargets = []string{"sim/alpha"} }, nil, ""},

		{"cloud not allowed", nil, func(tv *TargetView) { tv.Cloud = true },
			"cloud target not in backendSelector.allowCloudBurst"},
		{"cloud allowed when listed", func(j *JobView) { j.AllowCloudBurst = []string{"sim/alpha"} },
			func(tv *TargetView) { tv.Cloud = true }, ""},

		{"budget unit unmetered", func(j *JobView) { j.BudgetUnits = []string{"qpu-seconds"} }, nil,
			"budget limit unit qpu-seconds not metered by target (bills: shots, tasks)"},
		{"budget unit metered ok", func(j *JobView) { j.BudgetUnits = []string{"shots"} }, nil, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j, tv := baseJob(), baseTarget()
			if tc.job != nil {
				tc.job(j)
			}
			if tc.target != nil {
				tc.target(tv)
			}
			got := FilterTarget(j, tv, now)
			if tc.want == "" && got != "" {
				t.Fatalf("expected feasible, got rejection %q", got)
			}
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("rejection %q does not contain %q", got, tc.want)
			}
		})
	}
}
