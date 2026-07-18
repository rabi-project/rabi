// SPDX-License-Identifier: Apache-2.0

// Package probe runs known-output circuits per target on a schedule under
// the first-class system tenant (phase1-build-plan.md M12). Each probe is a
// Bell pair: measured counts against the ideal 50/50 give a fidelity
// (1 − total-variation distance) that feeds target health, and the
// placement's predicted success probability gives the estimator error the
// pilot SLO tracks (median |predicted − measured| ≤ 0.10).
package probe

import (
	"context"
	"encoding/base64"
	"log/slog"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/rabi-project/rabi/internal/job"
	"github.com/rabi-project/rabi/internal/store"
)

// Tenant is the system probe project (auto-created, fair-share weight 1).
const Tenant = "system/probes"

const bellQASM = `
OPENQASM 3.0;
include "stdgates.inc";
qubit[2] q;
bit[2] c;
h q[0];
cx q[0], q[1];
c = measure q;
`

const probeShots = 400.0

// TargetLister names the online gate-model targets to probe.
type TargetLister interface {
	OnlineGateModelTargets(ctx context.Context) []string
}

// Runner schedules probes and harvests results.
type Runner struct {
	store   *store.Store
	targets TargetLister
	logger  *slog.Logger
	every   time.Duration
}

// New builds a runner; every <= 0 disables scheduling (harvest still works).
func New(st *store.Store, targets TargetLister, every time.Duration, logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{store: st, targets: targets, logger: logger, every: every}
}

// Run blocks until ctx is done, probing every interval and harvesting
// finished probes each tick.
func (r *Runner) Run(ctx context.Context) {
	if r.every <= 0 {
		return
	}
	ticker := time.NewTicker(r.every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		r.probeAll(ctx)
		if err := r.Harvest(ctx); err != nil && ctx.Err() == nil {
			r.logger.Error("harvesting probes", "error", err)
		}
	}
}

func (r *Runner) probeAll(ctx context.Context) {
	if _, err := r.store.EnsureProject(ctx, Tenant); err != nil {
		r.logger.Error("probe project", "error", err)
		return
	}
	for _, target := range r.targets.OnlineGateModelTargets(ctx) {
		if err := r.submitProbe(ctx, target); err != nil && ctx.Err() == nil {
			r.logger.Error("submitting probe", "target", target, "error", err)
		}
	}
}

// submitProbe enqueues one pinned Bell job for a target.
func (r *Runner) submitProbe(ctx context.Context, target string) error {
	rec := &store.JobRecord{
		JobID:  uuid.NewString(),
		Tenant: Tenant,
		Name:   "probe-" + target,
		Phase:  job.Pending,
		Doc: map[string]any{
			"apiVersion": "tangle.dev/v1alpha1", "kind": "QuantumJob",
			"metadata": map[string]any{"name": "probe", "tenant": Tenant,
				"labels": map[string]any{"rabi.dev/probe": "true"}},
			"spec": map[string]any{
				"workload": map[string]any{
					"kind": "gate-model",
					"gateModel": map[string]any{
						"program": map[string]any{
							"format": "openqasm3",
							"inline": base64.StdEncoding.EncodeToString([]byte(bellQASM)),
						},
						"shots": probeShots,
					},
				},
				"backendSelector": map[string]any{
					"requireTargets":  []any{target},
					"allowCloudBurst": []any{target},
				},
			},
		},
		Status: map[string]any{"phase": "PENDING", "conditions": []any{}},
	}
	return r.store.InsertJob(ctx, rec)
}

// Harvest converts finished, unrecorded probe jobs into probe_results rows.
func (r *Runner) Harvest(ctx context.Context) error {
	rows, err := r.store.Pool.Query(ctx, `
		SELECT j.job_id, j.status
		FROM jobs j
		WHERE j.tenant = $1 AND j.phase = 'SUCCEEDED'
		  AND NOT EXISTS (SELECT 1 FROM probe_results p WHERE p.job_id = j.job_id)
		LIMIT 100`, Tenant)
	if err != nil {
		return err
	}
	type finished struct {
		id     string
		status map[string]any
	}
	var done []finished
	for rows.Next() {
		var f finished
		if err := rows.Scan(&f.id, &f.status); err != nil {
			rows.Close()
			return err
		}
		done = append(done, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, f := range done {
		target, fidelity, predicted := analyze(f.status)
		if target == "" {
			continue // no counts yet (shouldn't happen for SUCCEEDED)
		}
		var absErr *float64
		var pred *float64
		if predicted > 0 {
			e := math.Abs(predicted - fidelity)
			absErr, pred = &e, &predicted
		}
		if _, err := r.store.Pool.Exec(ctx, `
			INSERT INTO probe_results (target, job_id, fidelity, predicted_esp, abs_error)
			VALUES ($1, $2, $3, $4, $5)`,
			target, f.id, fidelity, pred, absErr); err != nil {
			return err
		}
		r.logger.Info("probe recorded", "target", target,
			"fidelity", fidelity, "predicted", predicted)
	}
	return nil
}

// analyze extracts (target, fidelity vs ideal Bell, predictedESP) from a
// finished probe job's status document.
func analyze(status map[string]any) (string, float64, float64) {
	target, _ := status["boundTarget"].(string)
	var predicted float64
	if placement, ok := status["placement"].(map[string]any); ok {
		if p, ok := placement["predicted"].(map[string]any); ok {
			predicted, _ = p["successProbability"].(float64)
		}
	}
	tasks, _ := status["tasks"].([]any)
	if len(tasks) == 0 {
		return "", 0, 0
	}
	task, _ := tasks[0].(map[string]any)
	result, _ := task["result"].(map[string]any)
	data, _ := result["data"].(map[string]any)
	counts, _ := data["counts"].(map[string]any)
	if len(counts) == 0 {
		return "", 0, 0
	}
	var total float64
	for _, v := range counts {
		n, _ := v.(float64)
		total += n
	}
	if total == 0 {
		return "", 0, 0
	}
	// Fidelity = 1 − TVD against the ideal Bell distribution {00: ½, 11: ½}.
	ideal := map[string]float64{"00": 0.5, "11": 0.5}
	seen := map[string]bool{}
	tvd := 0.0
	for k, v := range counts {
		n, _ := v.(float64)
		tvd += math.Abs(n/total-ideal[k]) / 2
		seen[k] = true
	}
	for k, p := range ideal {
		if !seen[k] {
			tvd += p / 2
		}
	}
	return target, 1 - tvd, predicted
}

// Health summarizes the latest probe per target (for /metrics + console).
type Health struct {
	Target    string
	Fidelity  float64
	AbsError  *float64
	Predicted *float64
	At        time.Time
}

// LatestHealth returns each target's most recent probe result.
func LatestHealth(ctx context.Context, st *store.Store) ([]Health, error) {
	rows, err := st.Pool.Query(ctx, `
		SELECT DISTINCT ON (target) target, fidelity, abs_error, predicted_esp, at
		FROM probe_results ORDER BY target, at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Health
	for rows.Next() {
		var h Health
		if err := rows.Scan(&h.Target, &h.Fidelity, &h.AbsError, &h.Predicted, &h.At); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
