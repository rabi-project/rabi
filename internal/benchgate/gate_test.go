// SPDX-License-Identifier: Apache-2.0

package benchgate

import (
	"path/filepath"
	"testing"
)

var baseline = Metrics{
	Policy: "calib-aware/v0", MeanFidelity: 0.407417,
	SLOViolationRate: 0.0, DeadlineMetRate: 0.970149, MeanWaitS: 718.734,
}

func TestCheck_NoRegressionWhenEqual(t *testing.T) {
	if regs := Check(baseline, baseline, 0.05); len(regs) != 0 {
		t.Fatalf("identical metrics should not regress: %v", regs)
	}
}

func TestCheck_SmallChangeWithinThreshold(t *testing.T) {
	cur := baseline
	cur.MeanFidelity = baseline.MeanFidelity * 0.97 // 3% drop, within 5%
	if regs := Check(baseline, cur, 0.05); len(regs) != 0 {
		t.Fatalf("3%% drop should pass a 5%% gate: %v", regs)
	}
}

func TestCheck_PlantedFidelityRegressionBlocks(t *testing.T) {
	cur := baseline
	cur.MeanFidelity = baseline.MeanFidelity * 0.85 // 15% drop
	regs := Check(baseline, cur, 0.05)
	if len(regs) == 0 {
		t.Fatal("a 15% fidelity drop MUST be caught")
	}
	if regs[0].Metric != "mean_fidelity" {
		t.Errorf("wrong metric flagged: %v", regs)
	}
	t.Logf("blocked: %s", regs[0])
}

func TestCheck_PlantedSLORegressionBlocks(t *testing.T) {
	cur := baseline
	cur.SLOViolationRate = 0.10 // from a clean 0.0 baseline
	regs := Check(baseline, cur, 0.05)
	found := false
	for _, r := range regs {
		if r.Metric == "slo_violation_rate" {
			found = true
		}
	}
	if !found {
		t.Fatalf("an SLO-violation jump from 0 must be caught: %v", regs)
	}
}

func TestCheck_DeadlineRegressionBlocks(t *testing.T) {
	cur := baseline
	cur.DeadlineMetRate = baseline.DeadlineMetRate * 0.80
	if regs := Check(baseline, cur, 0.05); len(regs) == 0 {
		t.Fatal("a 20% deadline-met drop must be caught")
	}
}

// The gate reads the real committed benchmark summary, and it matches the
// pinned baseline (both derive from the same Artifact B run) — so a stock
// release is not blocked.
func TestLoadSummaryCSV_MatchesBaseline(t *testing.T) {
	path := filepath.Join("..", "..", "bench", "results", "summary.csv")
	cur, err := LoadSummaryCSV(path, "calib-aware/v0")
	if err != nil {
		t.Fatalf("load committed summary: %v", err)
	}
	if regs := Check(baseline, cur, 0.05); len(regs) != 0 {
		t.Fatalf("committed bench results should not regress against the baseline: %v", regs)
	}
}

func TestLoadBaselineJSON(t *testing.T) {
	path := filepath.Join("..", "..", "bench", "baseline.json")
	m, err := LoadBaselineJSON(path)
	if err != nil {
		t.Fatalf("load baseline.json: %v", err)
	}
	if m.Policy != "calib-aware/v0" || m.MeanFidelity <= 0 {
		t.Fatalf("baseline.json looks wrong: %+v", m)
	}
	// The committed baseline must match the committed benchmark results.
	cur, err := LoadSummaryCSV(filepath.Join("..", "..", "bench", "results", "summary.csv"), m.Policy)
	if err != nil {
		t.Fatal(err)
	}
	if regs := Check(m, cur, 0.05); len(regs) != 0 {
		t.Fatalf("baseline.json disagrees with committed results: %v", regs)
	}
}
