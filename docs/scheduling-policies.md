<!-- SPDX-License-Identifier: Apache-2.0 -->
# Scheduling policies

Rabi's scheduler is pluggable: a **policy** filters the feasible targets for a
job and scores the survivors; the highest score wins (ties broken by target
name). Policies are registered by name and selected with `RABI_POLICY`; the
default is `fifo/v0`.

Changing the **default** policy is a *promotion* and requires evidence from the
shadow-scheduling pipeline and a human-approved PR labeled `policy-promotion`
(enforced by `.github/workflows/policy-guard.yml`). Any policy can run as a
**shadow** candidate via `RABI_SHADOW_POLICIES` — it computes the placement it
*would* make on every decision, recorded but never executed, and
`rabi-shadow report` compares it against the active policy.

## Built-in policies

| policy | role |
|---|---|
| `fifo/v0` | arrival order; equal score among feasible targets (default) |
| `static-best/v0` | baseline: always the nominally-best device, calibration-blind (models documented current practice) |
| `round-robin/v0` | baseline: rotate over the feasible set, blind to everything |
| `calib-aware/v0` | `w_q·ESP − w_t·wait_norm − w_c·cost_norm` on the live calibration snapshot (the value-carrying policy in Artifact B) |

## Absorbed policies (attributed, shadow-only)

These are implementations of published scheduling ideas, adapted to Rabi's
per-job placement interface. **They ship shadow-only** — candidates evaluated in
shadow, never promotable to default without the M5 promotion pipeline's
evidence. Attribution is carried in the code headers
(`internal/scheduler/absorbed.go`), here, and in the release notes.

### `pareto/v0` — Pareto multi-objective (Qonductor lineage)

**Citation.** Giortamis, Romão, Tsoutsfrom, Bhatotia et al., *Qonductor: A Cloud
Orchestrator for Quantum Computing* (2024). Qonductor selects executions by
multi-objective optimization (NSGA-II), trading execution time against fidelity.

**What it does here.** For each feasible target it computes two objectives —
fidelity proxy (ESP, maximized) and speed (`−wait`, maximized) — performs
NSGA-II non-dominated sorting into Pareto fronts, and selects deterministically
from the first front by a balanced scalarization of the two normalized
objectives.

**Differences from the paper.**
- Qonductor evolves a **population of whole-schedule candidates** with NSGA-II's
  genetic operators (crossover, mutation) over generations. Rabi places **one
  job at a time**, so this policy keeps NSGA-II's *non-dominated sorting* over
  the feasible targets but **not** the evolutionary search — there is no
  population, no crossover/mutation, no generations.
- Selection from the Pareto front is a **deterministic** balanced scalarization
  (so placements are reproducible and pass the golden regression suite), not a
  stochastic genetic pick.
- Objectives are per-target proxies (ESP, queue wait), not Qonductor's full
  cost/time model.

### `adaptive-deferral/v0` — calibration-window awareness (Ravi et al. lineage)

**Citation.** Ravi, Smith, Gokhale, Chong et al., *Adaptive Job and Resource
Management for the Quantum Cloud* (IEEE QCE, 2021). The central observation:
device error rates drift between recalibrations, so scheduling should be aware of
the calibration window and prefer freshly-calibrated devices, deferring a
quality-sensitive job onto a better window when it helps.

**What it does here.** It weights the fidelity proxy by **calibration
freshness** — an exponential decay of the target's calibration age over the
job's tolerance window (`requirements.quality.gateModel.calibrationMaxAge`, or a
30-minute default) — strongly preferring freshly-calibrated targets.

**Differences from the paper.**
- The paper runs a full adaptive manager that can **defer a job in wall-clock
  time** to await a recalibration. Rabi's placement interface chooses among
  *currently-feasible* targets; the existing `calibrationMaxAge` filter already
  defers a floor'd job (it stays `PENDING` when every target is too stale). This
  policy contributes the **ranking** — freshness-weighted fidelity — and relies
  on the dispatcher's re-cycle behavior for the actual deferral, rather than
  scheduling a future wake-up itself.
- Freshness is a smooth exponential decay rather than the paper's discrete
  calibration-cycle model.

## Adding a policy

Implement `scheduler.SchedulingPolicy` (and optionally `SetScorer` /
`ESPPredictor`), `Register` it, and it is automatically held to the policy
conformance suite (`TestPolicyConformance`): it must only ever select a target
its own filter accepts, be deterministic for a fresh instance, and reroute when
its choice is removed. New policies ship shadow-only.
