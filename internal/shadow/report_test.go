// SPDX-License-Identifier: Apache-2.0

package shadow

import (
	"testing"

	"github.com/rabi-project/rabi/internal/store"
)

// mkPlacements builds n paired shadow records; espFn/waitFn produce the active
// and shadow proxies per index so tests can shape the distribution.
func mkPlacements(policy string, n int, espFn func(i int) (active, shadow float64)) []store.ShadowPlacement {
	ps := make([]store.ShadowPlacement, n)
	for i := range ps {
		a, s := espFn(i)
		aw, sw := 0.0, 5.0
		ps[i] = store.ShadowPlacement{
			Policy: policy, ActivePolicy: "fifo/v0",
			ActiveTarget: "sim/a", ShadowTarget: "sim/b", Agreed: false,
			ActiveESP: &a, ShadowESP: &s, ActiveWait: &aw, ShadowWait: &sw,
		}
	}
	return ps
}

func TestPromotable_BetterCandidate(t *testing.T) {
	// Candidate consistently places at higher ESP, both above the floor.
	ps := mkPlacements("calib-aware/v0", 500, func(i int) (float64, float64) {
		jitter := float64(i%10) * 0.001
		return 0.80 + jitter, 0.90 + jitter // +0.10 fidelity, no SLO change
	})
	r := Analyze(ps, DefaultQualityFloor)
	if r.ESPDelta.CILo <= 0 {
		t.Fatalf("expected positive ESP-delta CI, got %+v", r.ESPDelta)
	}
	ok, reasons := r.Promotable()
	if !ok {
		t.Fatalf("better candidate should be promotable; reasons: %v", reasons)
	}
}

func TestPromotable_WorseCandidateNotPromotable(t *testing.T) {
	// The acceptance case: a deliberately-worse policy places at LOWER ESP.
	ps := mkPlacements("bad/v0", 500, func(i int) (float64, float64) {
		jitter := float64(i%10) * 0.001
		return 0.80 + jitter, 0.65 + jitter // -0.15 fidelity
	})
	r := Analyze(ps, DefaultQualityFloor)
	ok, reasons := r.Promotable()
	if ok {
		t.Fatalf("a worse candidate must NOT be promotable; report %+v", r)
	}
	if r.ESPDelta.CIHi >= 0 {
		t.Errorf("worse candidate should have negative ESP-delta CI, got %+v", r.ESPDelta)
	}
	t.Logf("correctly rejected: %v", reasons)
}

func TestPromotable_SLORegressionBlocks(t *testing.T) {
	// Candidate has a hair-higher ESP on average but pushes many placements
	// below the quality floor — an SLO regression must block promotion.
	ps := mkPlacements("risky/v0", 500, func(i int) (float64, float64) {
		if i%2 == 0 {
			return 0.60, 0.95 // big win half the time
		}
		return 0.60, 0.30 // below floor the other half (violation)
	})
	r := Analyze(ps, DefaultQualityFloor)
	ok, _ := r.Promotable()
	if ok && r.SLODelta.CILo > 0 {
		t.Fatalf("SLO regression should block promotion; report %+v", r)
	}
	if r.SLODelta.Mean <= 0 {
		t.Errorf("expected a positive SLO-violation delta (more violations), got %v", r.SLODelta.Mean)
	}
}

func TestPromotable_InsufficientSamples(t *testing.T) {
	ps := mkPlacements("calib-aware/v0", 20, func(i int) (float64, float64) {
		return 0.80, 0.90
	})
	r := Analyze(ps, DefaultQualityFloor)
	if ok, _ := r.Promotable(); ok {
		t.Fatal("20 samples is below MinSamples; must not be promotable")
	}
}

func TestAnalyze_Empty(t *testing.T) {
	r := Analyze(nil, 0)
	if r.Samples != 0 || r.QualityFloor != DefaultQualityFloor {
		t.Fatalf("empty analyze unexpected: %+v", r)
	}
	if ok, _ := r.Promotable(); ok {
		t.Fatal("empty report must not be promotable")
	}
}
