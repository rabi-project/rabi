// SPDX-License-Identifier: Apache-2.0

// Package benchgate is the benchmark-as-regression gate (phase2-build-plan.md
// P2.M5): it compares a benchmark's headline metrics for the value-carrying
// policy against a pinned baseline and reports any regression beyond a
// threshold. A regression blocks a release tag unless the release carries an
// RFC-referenced justification (the baseline then moves in a policy-promotion
// PR). It reads the benchmark's own summary.csv, so it gates the real Artifact B
// numbers, not a re-derivation.
package benchgate

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

// LoadBaselineJSON reads a pinned baseline Metrics from JSON.
func LoadBaselineJSON(path string) (Metrics, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return Metrics{}, err
	}
	var m Metrics
	if err := json.Unmarshal(blob, &m); err != nil {
		return Metrics{}, fmt.Errorf("parse baseline %s: %w", path, err)
	}
	return m, nil
}

// Metrics are the headline benchmark numbers for one policy.
type Metrics struct {
	Policy           string  `json:"policy"`
	MeanFidelity     float64 `json:"mean_fidelity"`      // higher is better
	SLOViolationRate float64 `json:"slo_violation_rate"` // lower is better
	DeadlineMetRate  float64 `json:"deadline_met_rate"`  // higher is better
	MeanWaitS        float64 `json:"mean_wait_s"`        // context only, not gated
}

// Regression is one breached headline metric.
type Regression struct {
	Metric   string
	Baseline float64
	Current  float64
	Detail   string
}

func (r Regression) String() string {
	return fmt.Sprintf("%s regressed: %.4f -> %.4f (%s)", r.Metric, r.Baseline, r.Current, r.Detail)
}

// Check reports headline-metric regressions of current vs baseline beyond
// threshold (e.g. 0.05 = 5%). Fidelity and deadline-met are "higher is better"
// (relative drop past threshold is a regression); SLO-violation rate is "lower
// is better" (a relative rise past threshold, or crossing an absolute floor
// from a near-zero baseline, is a regression). Wait is not gated — calibration-
// aware policies trade wait for fidelity by design.
func Check(baseline, current Metrics, threshold float64) []Regression {
	var regs []Regression
	if current.MeanFidelity < baseline.MeanFidelity*(1-threshold) {
		regs = append(regs, Regression{"mean_fidelity", baseline.MeanFidelity, current.MeanFidelity,
			fmt.Sprintf("dropped more than %.0f%%", threshold*100)})
	}
	if current.DeadlineMetRate < baseline.DeadlineMetRate*(1-threshold) {
		regs = append(regs, Regression{"deadline_met_rate", baseline.DeadlineMetRate, current.DeadlineMetRate,
			fmt.Sprintf("dropped more than %.0f%%", threshold*100)})
	}
	// SLO violation rate: lower is better. Guard both relative rise and an
	// absolute jump from a ~zero baseline (where relative is meaningless).
	if slorRose(baseline.SLOViolationRate, current.SLOViolationRate, threshold) {
		regs = append(regs, Regression{"slo_violation_rate", baseline.SLOViolationRate, current.SLOViolationRate,
			"more violations"})
	}
	return regs
}

func slorRose(baseline, current, threshold float64) bool {
	if baseline < 0.01 { // near-zero baseline: any rise past 5 points is a regression
		return current > baseline+0.05
	}
	return current > baseline*(1+threshold)
}

// LoadSummaryCSV parses a benchmark summary.csv and returns the metrics for the
// named policy.
func LoadSummaryCSV(path, policy string) (Metrics, error) {
	f, err := os.Open(path)
	if err != nil {
		return Metrics{}, err
	}
	defer func() { _ = f.Close() }()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return Metrics{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(rows) < 2 {
		return Metrics{}, fmt.Errorf("%s: no data rows", path)
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[h] = i
	}
	for _, k := range []string{"policy", "mean_fidelity", "slo_violation_rate", "deadline_met_rate"} {
		if _, ok := col[k]; !ok {
			return Metrics{}, fmt.Errorf("%s: missing column %q", path, k)
		}
	}
	for _, row := range rows[1:] {
		if row[col["policy"]] != policy {
			continue
		}
		return Metrics{
			Policy:           policy,
			MeanFidelity:     atof(row[col["mean_fidelity"]]),
			SLOViolationRate: atof(row[col["slo_violation_rate"]]),
			DeadlineMetRate:  atof(row[col["deadline_met_rate"]]),
			MeanWaitS:        atofOr(row, col, "mean_wait_s"),
		}, nil
	}
	return Metrics{}, fmt.Errorf("%s: policy %q not found", path, policy)
}

func atof(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func atofOr(row []string, col map[string]int, key string) float64 {
	if i, ok := col[key]; ok && i < len(row) {
		return atof(row[i])
	}
	return 0
}
