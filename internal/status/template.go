// SPDX-License-Identifier: Apache-2.0

package status

import "strconv"

// formatFloat renders one decimal place, trimming a trailing ".0".
func formatFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', 1, 64)
	if len(s) > 2 && s[len(s)-2:] == ".0" {
		s = s[:len(s)-2]
	}
	return s
}

// statusHTML is the self-contained status page. No external assets, no scripts —
// a static document rendered by the control plane from its own database.
const statusHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Rabi — status</title>
<style>
 :root { color-scheme: light dark; }
 body { font: 16px/1.5 system-ui, sans-serif; max-width: 46rem; margin: 2rem auto; padding: 0 1rem; }
 h1 { margin-bottom: .25rem; }
 .badge { display: inline-block; padding: .2rem .7rem; border-radius: 1rem; font-weight: 600; color: #fff; }
 .ok { background: #178a3f; } .bad { background: #b3261e; }
 .grid { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; margin: 1.5rem 0; }
 .card { border: 1px solid #8884; border-radius: .5rem; padding: 1rem; }
 .metric { font-size: 1.8rem; font-weight: 700; }
 .label { font-size: .85rem; opacity: .7; text-transform: uppercase; letter-spacing: .03em; }
 .caveat { font-size: .85rem; opacity: .75; font-style: italic; }
 footer { margin-top: 2rem; font-size: .8rem; opacity: .6; }
 @media (max-width: 32rem) { .grid { grid-template-columns: 1fr; } }
</style>
</head>
<body>
<h1>Rabi status</h1>
<p>{{if .Healthy}}<span class="badge ok">Healthy</span>{{else}}<span class="badge bad">Degraded</span>{{end}}
   &nbsp;<span class="caveat">generated {{.GeneratedAt.UTC.Format "2006-01-02 15:04 UTC"}}</span></p>

<div class="grid">
  <div class="card">
    <div class="label">Days since a job was lost</div>
    <div class="metric">{{days .DaysSinceJobLost}}</div>
    <div class="caveat">{{if .NeverLost}}0 jobs lost in {{days .OperationDays}} days of operation{{else}}{{.JobsLost}} job(s) currently unaccounted{{end}}</div>
  </div>
  <div class="card">
    <div class="label">Uptime (this process)</div>
    <div class="metric">{{days .UptimeDays}} d</div>
    <div class="caveat">operation window: {{days .OperationDays}} days</div>
  </div>
  <div class="card">
    <div class="label">Probe success rate</div>
    <div class="metric">{{if .ProbesConsidered}}{{pct .ProbeSuccessRate}}{{else}}—{{end}}</div>
    <div class="caveat">{{.ProbesConsidered}} target(s) probed</div>
  </div>
  <div class="card">
    <div class="label">Estimator calibration error (median)</div>
    <div class="metric">{{if .MedianCalibErr}}{{days (mul100 .MedianCalibErr)}}%{{else}}—{{end}}</div>
    <div class="caveat">{{.CalibrationCaveat}}</div>
  </div>
</div>

<div class="card">
  <div class="label">Accounting reconciliation</div>
  <p>{{if .ReconciliationOK}}Clean{{else}}<b>{{.ReconciliationMis}} mismatch(es)</b>{{end}}{{if not .ReconciliationAt.IsZero}} — last audit {{.ReconciliationAt.UTC.Format "2006-01-02 15:04 UTC"}}{{end}}. The usage ledger, audit log, and job-event stream are append-only at the database-grant level.</p>
</div>

<div class="card" style="margin-top:1rem">
  <div class="label">Last game-day drill</div>
  {{if .LastGameDay}}
  <p><b>{{.LastGameDay.Scenario}}</b> on <b>{{.LastGameDay.Target}}</b> — {{if .LastGameDay.InvariantsGreen}}invariants green{{else}}<b>invariants RED ({{.LastGameDay.Violations}} violation(s))</b>{{end}}, {{.LastGameDay.FinishedAt.UTC.Format "2006-01-02 15:04 UTC"}}{{if .LastGameDay.Note}} ({{.LastGameDay.Note}}){{end}}.</p>
  {{else}}
  <p>No game-day recorded yet.</p>
  {{end}}
</div>

<footer>
Rabi is an open-source control plane for quantum compute fleets. This page is a
static document rendered by the control plane from its own database — no external
services. Healthy means: zero lost jobs, a clean accounting reconciliation, and
probes passing.
</footer>
</body>
</html>`
