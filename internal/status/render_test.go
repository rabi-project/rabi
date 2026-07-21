// SPDX-License-Identifier: Apache-2.0

package status

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/rabi-project/rabi/internal/store"
)

func render(t *testing.T, d Data) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Render(&buf, d); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

func TestRender_HealthyPage(t *testing.T) {
	calErr := 0.07
	d := Data{
		GeneratedAt: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC),
		UptimeDays:  3.5, OperationDays: 40, JobsLost: 0, NeverLost: true, DaysSinceJobLost: 40,
		ProbesConsidered: 4, ProbeSuccessRate: 1.0, MedianCalibErr: &calErr,
		CalibrationCaveat: "probe-circuit caveat", ReconciliationOK: true,
		LastGameDay: &store.GameDay{
			Scenario: "invariant-sweep", Target: "fleet0", InvariantsGreen: true,
			FinishedAt: time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC),
		},
		Healthy: true,
	}
	html := render(t, d)
	for _, want := range []string{
		"Rabi status", "Healthy", "Days since a job was lost", "40",
		"0 jobs lost in 40 days", "Probe success rate", "100%",
		"probe-circuit caveat", "invariant-sweep", "invariants green",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("status page missing %q", want)
		}
	}
	if strings.Contains(html, "Degraded") {
		t.Error("healthy page should not say Degraded")
	}
}

func TestRender_DegradedPage(t *testing.T) {
	d := Data{
		GeneratedAt: time.Now(), JobsLost: 2, NeverLost: false, DaysSinceJobLost: 0.5,
		ReconciliationOK: false, ReconciliationMis: 3, Healthy: false,
		CalibrationCaveat: "caveat",
	}
	html := render(t, d)
	if !strings.Contains(html, "Degraded") {
		t.Error("unhealthy page should show Degraded")
	}
	if !strings.Contains(html, "2 job(s) currently unaccounted") {
		t.Error("should surface lost jobs")
	}
	if !strings.Contains(html, "mismatch") {
		t.Error("should surface reconciliation mismatches")
	}
}

func TestRender_NoGameDay(t *testing.T) {
	html := render(t, Data{GeneratedAt: time.Now(), NeverLost: true, Healthy: true, CalibrationCaveat: "c"})
	if !strings.Contains(html, "No game-day recorded yet") {
		t.Error("should handle absent game-day")
	}
}

func TestFormatFloat(t *testing.T) {
	cases := map[float64]string{40.0: "40", 3.5: "3.5", 0.0: "0", 99.94: "99.9"}
	for in, want := range cases {
		if got := formatFloat(in); got != want {
			t.Errorf("formatFloat(%v) = %q, want %q", in, got, want)
		}
	}
}
