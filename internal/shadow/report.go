// SPDX-License-Identifier: Apache-2.0

// Package shadow turns recorded shadow placements (P2.M5) into a promotion
// decision: it compares a candidate policy against the active one on a
// fidelity proxy (ESP), a quality-SLO proxy, and wait — each with a bootstrap
// confidence interval — and reports whether the candidate is promotable. A
// candidate is promotable only when it is confidently better on fidelity
// without a confident SLO regression; a deliberately-worse policy is not.
package shadow

import (
	"fmt"
	"math/rand"
	"sort"

	"github.com/rabi-project/rabi/internal/store"
)

// DefaultQualityFloor is the ESP below which a placement counts as an
// SLO-proxy violation.
const DefaultQualityFloor = 0.5

// MinSamples is the smallest number of paired observations for which a
// promotion verdict is trustworthy.
const MinSamples = 100

// Interval is a mean with a bootstrap 95% confidence interval over n samples.
type Interval struct {
	Mean float64 `json:"mean"`
	CILo float64 `json:"ci_lo"`
	CIHi float64 `json:"ci_hi"`
	N    int     `json:"n"`
}

// Report compares a candidate policy to the active one.
type Report struct {
	Policy        string   `json:"policy"`
	ActivePolicy  string   `json:"active_policy"`
	Samples       int      `json:"samples"`
	AgreementRate float64  `json:"agreement_rate"`
	QualityFloor  float64  `json:"quality_floor"`
	ESPDelta      Interval `json:"esp_delta"`    // candidate - active fidelity proxy (higher better)
	SLODelta      Interval `json:"slo_delta"`    // candidate - active violation rate (lower better)
	WaitDelta     Interval `json:"wait_delta_s"` // candidate - active wait seconds (context only)
	ActiveESPMean float64  `json:"active_esp_mean"`
	ShadowESPMean float64  `json:"shadow_esp_mean"`
}

// Analyze builds a Report from a candidate's shadow placements.
func Analyze(placements []store.ShadowPlacement, floor float64) Report {
	if floor <= 0 {
		floor = DefaultQualityFloor
	}
	r := Report{QualityFloor: floor, Samples: len(placements)}
	if len(placements) == 0 {
		return r
	}
	r.Policy = placements[0].Policy
	r.ActivePolicy = placements[0].ActivePolicy

	var agreed int
	var espDeltas, waitDeltas, sloDeltas []float64
	var activeESPs, shadowESPs []float64
	for _, p := range placements {
		if p.Agreed {
			agreed++
		}
		if p.ActiveESP != nil && p.ShadowESP != nil {
			espDeltas = append(espDeltas, *p.ShadowESP-*p.ActiveESP)
			activeESPs = append(activeESPs, *p.ActiveESP)
			shadowESPs = append(shadowESPs, *p.ShadowESP)
			// SLO-proxy: 1 if below the quality floor, else 0. The paired delta
			// is shadow_violation - active_violation for the same job.
			sloDeltas = append(sloDeltas, boolf(*p.ShadowESP < floor)-boolf(*p.ActiveESP < floor))
		}
		if p.ActiveWait != nil && p.ShadowWait != nil {
			waitDeltas = append(waitDeltas, *p.ShadowWait-*p.ActiveWait)
		}
	}
	r.AgreementRate = float64(agreed) / float64(len(placements))
	r.ActiveESPMean = mean(activeESPs)
	r.ShadowESPMean = mean(shadowESPs)
	// Deterministic seeds so the same data yields the same intervals.
	r.ESPDelta = bootstrap(espDeltas, 1)
	r.SLODelta = bootstrap(sloDeltas, 2)
	r.WaitDelta = bootstrap(waitDeltas, 3)
	return r
}

// Promotable reports whether the candidate should be promotable from this
// evidence, with human-readable reasons. The bar: enough paired samples, a
// confident fidelity improvement (ESP delta CI entirely above 0), and no
// confident SLO regression (violation-rate delta CI not entirely above 0).
// Wait is reported for context but does not gate — calibration-aware policies
// deliberately trade wait for fidelity.
func (r Report) Promotable() (bool, []string) {
	var reasons []string
	ok := true
	if r.ESPDelta.N < MinSamples {
		ok = false
		reasons = append(reasons, fmt.Sprintf("insufficient evidence: %d paired samples < %d", r.ESPDelta.N, MinSamples))
	}
	if r.ESPDelta.CILo > 0 {
		reasons = append(reasons, fmt.Sprintf("fidelity proxy improved: ESP delta %.4f (95%% CI %.4f..%.4f > 0)", r.ESPDelta.Mean, r.ESPDelta.CILo, r.ESPDelta.CIHi))
	} else {
		ok = false
		reasons = append(reasons, fmt.Sprintf("no confident fidelity gain: ESP delta %.4f (95%% CI %.4f..%.4f includes/below 0)", r.ESPDelta.Mean, r.ESPDelta.CILo, r.ESPDelta.CIHi))
	}
	if r.SLODelta.CILo > 0 {
		ok = false
		reasons = append(reasons, fmt.Sprintf("SLO regression: violation-rate delta %.4f (95%% CI %.4f..%.4f > 0)", r.SLODelta.Mean, r.SLODelta.CILo, r.SLODelta.CIHi))
	} else {
		reasons = append(reasons, fmt.Sprintf("no SLO regression: violation-rate delta %.4f (95%% CI %.4f..%.4f)", r.SLODelta.Mean, r.SLODelta.CILo, r.SLODelta.CIHi))
	}
	return ok, reasons
}

func boolf(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// bootstrap returns the mean and a 95% CI via nonparametric bootstrap
// resampling with a fixed seed (deterministic).
func bootstrap(xs []float64, seed int64) Interval {
	iv := Interval{N: len(xs)}
	if len(xs) == 0 {
		return iv
	}
	iv.Mean = mean(xs)
	const B = 2000
	rng := rand.New(rand.NewSource(seed))
	means := make([]float64, B)
	n := len(xs)
	for b := 0; b < B; b++ {
		s := 0.0
		for i := 0; i < n; i++ {
			s += xs[rng.Intn(n)]
		}
		means[b] = s / float64(n)
	}
	sort.Float64s(means)
	iv.CILo = means[int(0.025*float64(B))]
	iv.CIHi = means[int(0.975*float64(B))]
	return iv
}
