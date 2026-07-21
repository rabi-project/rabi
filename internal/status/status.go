// SPDX-License-Identifier: Apache-2.0

// Package status renders the public status page (phase2-build-plan.md P2.M7): a
// static HTML page the control plane builds from its own database — uptime,
// days-since-a-job-was-lost, probe success rate, estimator calibration error
// (with the probe-circuit caveat printed honestly), and the last game-day
// result. No new infrastructure: it is served like /healthz and /metrics,
// aggregate-only, safe to expose. A stranger should be able to answer "is Rabi
// healthy, and how do you know?" from this page alone.
package status

import (
	"context"
	"html/template"
	"io"
	"sort"
	"time"

	"github.com/rabi-project/rabi/internal/probe"
	"github.com/rabi-project/rabi/internal/store"
)

// probeSuccessFidelity is the Bell-probe fidelity at/above which a probe counts
// as a success (well above the 0.25 a dead device would score).
const probeSuccessFidelity = 0.5

// Data is everything the status page shows.
type Data struct {
	GeneratedAt      time.Time
	UptimeDays       float64
	OperationDays    float64 // since the first job
	JobsLost         int64
	DaysSinceJobLost float64
	NeverLost        bool

	ProbesConsidered  int
	ProbeSuccessRate  float64 // fraction of latest probes with fidelity >= threshold
	MedianCalibErr    *float64
	CalibrationCaveat string

	ReconciliationOK  bool
	ReconciliationAt  time.Time
	ReconciliationMis int

	LastGameDay *store.GameDay

	Healthy bool
}

// Gather assembles the status data from the store. `started` is the process
// start time (for uptime).
func Gather(ctx context.Context, st *store.Store, started time.Time, now time.Time) (Data, error) {
	d := Data{GeneratedAt: now, UptimeDays: clampDays(now.Sub(started))}
	d.CalibrationCaveat = "Calibration error is measured only on a two-qubit Bell probe circuit; it is a health signal, not a guarantee for arbitrary workloads."

	// Operation window and lost jobs. A "lost" job is one still non-terminal well
	// past the policy max — the invariant suite's definition of lost.
	var firstJob *time.Time
	_ = st.Pool.QueryRow(ctx, `SELECT min(created_at) FROM jobs`).Scan(&firstJob)
	if firstJob != nil {
		d.OperationDays = clampDays(now.Sub(*firstJob))
	}
	const lostAfter = 24 * time.Hour
	cutoff := now.Add(-lostAfter)
	_ = st.Pool.QueryRow(ctx, `
		SELECT count(*) FROM jobs
		WHERE phase NOT IN ('SUCCEEDED','FAILED','CANCELLED') AND created_at < $1`, cutoff).Scan(&d.JobsLost)
	if d.JobsLost == 0 {
		d.NeverLost = true
		d.DaysSinceJobLost = d.OperationDays
	} else {
		var lastLost *time.Time
		_ = st.Pool.QueryRow(ctx, `
			SELECT max(created_at) FROM jobs
			WHERE phase NOT IN ('SUCCEEDED','FAILED','CANCELLED') AND created_at < $1`, cutoff).Scan(&lastLost)
		if lastLost != nil {
			d.DaysSinceJobLost = clampDays(now.Sub(*lastLost))
		}
	}

	// Probe health.
	health, err := probe.LatestHealth(ctx, st)
	if err == nil && len(health) > 0 {
		d.ProbesConsidered = len(health)
		var success int
		var calErrs []float64
		for _, h := range health {
			if h.Fidelity >= probeSuccessFidelity {
				success++
			}
			if h.AbsError != nil {
				calErrs = append(calErrs, *h.AbsError)
			}
		}
		d.ProbeSuccessRate = float64(success) / float64(len(health))
		if len(calErrs) > 0 {
			sort.Float64s(calErrs)
			m := calErrs[len(calErrs)/2]
			d.MedianCalibErr = &m
		}
	}

	// Reconciliation.
	checked, mis, at, ok, _ := st.LastReconciliation(ctx)
	_ = checked
	if ok {
		d.ReconciliationAt = at
		d.ReconciliationMis = mis
		d.ReconciliationOK = mis == 0
	} else {
		d.ReconciliationOK = true // no audit yet is not unhealthy
	}

	// Last game-day.
	if gd, ok, err := st.LastGameDay(ctx); err == nil && ok {
		d.LastGameDay = &gd
	}

	// Overall health: no lost jobs, reconciliation clean, probes mostly passing.
	d.Healthy = d.JobsLost == 0 && d.ReconciliationOK &&
		(d.ProbesConsidered == 0 || d.ProbeSuccessRate >= 0.5)
	return d, nil
}

var page = template.Must(template.New("status").Funcs(template.FuncMap{
	"days": func(f float64) string { return trim(f) },
	"pct":  func(f float64) string { return trim(f*100) + "%" },
	"mul100": func(f *float64) float64 {
		if f == nil {
			return 0
		}
		return *f * 100
	},
}).Parse(statusHTML))

// Render writes the status page HTML.
func Render(w io.Writer, d Data) error {
	return page.Execute(w, d)
}

func trim(f float64) string {
	// one decimal, no trailing ".0"
	s := formatFloat(f)
	return s
}

// clampDays converts a duration to days, never negative (clock skew or a job
// timestamped microseconds in the future must not render a negative metric).
func clampDays(d time.Duration) float64 {
	days := d.Hours() / 24
	if days < 0 {
		return 0
	}
	return days
}
