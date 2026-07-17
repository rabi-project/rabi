# The QuantumJob Object
*Spec v0.1-draft. Machine-readable schema: `schemas/quantumjob.schema.json` (authoritative on conflict).*

## Example (complete)

```yaml
apiVersion: tangle.dev/v1alpha1
kind: QuantumJob
metadata:
  name: vqe-fe2s2-run14
  tenant: yonsei-chem-lab          # org or org/project
  labels: { experiment: fe2s2 }
spec:
  workload:
    kind: gate-model               # gate-model | analog-hamiltonian | annealing | pulse | logical
    gateModel:
      program:
        format: openqasm3          # openqasm3 | qir | vendor-native (by capability)
        source: s3://bucket/ansatz.qasm   # or inline: <base64>
      shots: 20000
  requirements:
    qubits: 24
    technology: [superconducting, trapped-ion]    # acceptable set; empty = any
    quality:                        # typed per modality; provenance rules in overview.md §5
      gateModel:
        twoQubitErrorMax: 0.006
        readoutErrorMax: 0.02
        calibrationMaxAge: 6h       # placement-time constraint on snapshot freshness
  coupling: co-located              # loose | co-located | real-time
  session:
    join: null                      # or an existing sessionId
    maxDuration: 2h
  bundle:
    classical: { gpus: 0 }
    interconnect: none              # none | datacenter | realtime-qpu-gpu
  deadline: "2026-07-15T09:00:00+09:00"
  budget:
    maxCost: { amount: 25, currency: USD }
    limits: { qpu-seconds: 120, shots: 20000 }    # native-unit caps, adapter-enforced
  backendSelector:                  # constraints, not commands — all optional
    preferOnPrem: true
    allowCloudBurst: [ibm_torino]
    denyTargets: []
  retryOf: null                     # jobId, when requeuing a terminal job
status:                             # written by the control plane only
  phase: SCHEDULED
  boundTarget: lrz/iqm-q20-a
  placement:
    policy: calib-aware/v0
    calibrationSnapshot: snap-2026-07-14T21:04Z-9f3a
    predicted: { successProbability: 0.91, waitSeconds: 340 }
    reason: "best ESP among 3 feasible targets; 2 filtered (calibration age, qubit count)"
  tasks: [{ id: t-01, target: lrz/iqm-q20-a, state: QUEUED }]
  usage: []                         # native-unit records, appended per task
  conditions: []
```

## Field semantics (selected, normative)

**`spec.workload`** — exactly one modality payload, matching `kind`. Unknown `kind` values MUST be
rejected at admission (not at placement). New modalities are additive spec changes via RFC.

**`spec.requirements.quality`** — constraints are *floors/ceilings evaluated against a specific
calibration snapshot at placement time*; the snapshot ID MUST be recorded in `status.placement`.
A job with no feasible target stays `PENDING` with a condition explaining which constraint failed
for how many targets. Quality keys are namespaced per modality; provider-specific extensions live
under `quality.extensions.<vendor>` and never participate in cross-vendor comparison.

**`spec.coupling`** — declares the latency class the workload needs; `real-time` jobs can only bind
to Targets whose adapters declare a real-time capability (e.g., NVQLink-class integration). Tangle
allocates such bundles; it never sits in their latency path.

**`spec.budget.limits`** — native-unit caps enforced by the adapter where the vendor supports it,
and by the control plane as accounting cutoffs otherwise. `maxCost` is evaluated by the accounting
layer's normalization policy and is advisory at placement, binding at settlement.

**`spec.backendSelector`** — narrows the feasible set; it cannot widen quality or budget
constraints. `denyTargets`/`allowCloudBurst` take Target names; globbing is implementation-defined.

**`status.placement`** — the audit trail. Every binding records policy id/version, snapshot id,
predictions, and a human-readable reason. This field is what makes scheduling *arguable* — sites
can reconstruct why any job landed where it did.

## Validation (admission-time, normative)

1. `workload.kind` known; payload present and matching.
2. `program.format` ∈ declared formats of at least one registered adapter (warning, not rejection,
   if fleet currently lacks it — the fleet may change).
3. `deadline`, if set, is in the future; `session.maxDuration ≤ tenant policy max`.
4. `budget.limits` keys are known native units.
5. Tenant exists and has quota headroom for admission (execution-time quota is re-checked at bind).
