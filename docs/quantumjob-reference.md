# QuantumJob reference

Every field of a QuantumJob, keyed to `spec/schemas/quantumjob.schema.json`
(the schema is normative — this page explains it). New to the model? Read
[concepts.md](concepts.md) first.

A QuantumJob is a YAML or JSON document. Minimal example:

```yaml
apiVersion: tangle.dev/v1alpha1
kind: QuantumJob
metadata:
  name: bell
  tenant: acme/qa
spec:
  workload:
    kind: gate-model
    gateModel:
      program:
        format: openqasm3
        inline: <base64 OpenQASM 3>     # or: uri: s3://...
      shots: 1000
```

`apiVersion` and `kind` are fixed. `metadata.name` is yours; `metadata.tenant`
is the org/project the job belongs to (must match your credential's scope for
scoped tokens).

---

## spec.workload — what to run (required)

`kind` selects the modality; fill the matching block.

| kind | block | key fields |
|---|---|---|
| `gate-model` | `gateModel` | `program`, `shots` |
| `analog-hamiltonian` | `analogHamiltonian` | `program`, `repetitions` |
| `annealing` | `annealing` | `program`, `reads`, `schedule` |
| `pulse` | `pulse` | `program`, `shots` |
| `logical` | `logical` | `program`, `logicalQubits`, `targetLogicalErrorRate` |

**program** carries the circuit:

```yaml
program:
  format: openqasm3        # a format the target adapter declares
  inline: <base64>         # exactly one of inline | uri
  # uri: s3://bucket/circuit.qasm
```

`shots` / `repetitions` / `reads` are the sampling count for that modality.
Shots also feed quota accounting (see `budget`).

---

## spec.requirements — what the device must provide (optional)

```yaml
requirements:
  qubits: 20                       # minimum qubit count
  technology: [superconducting]    # any-of; must match TargetInfo.technology
  quality:
    gateModel:
      twoQubitErrorMax: 0.006      # floor: best/median/worst error ≤ this
      readoutErrorMax: 0.02
      calibrationMaxAge: 2h        # snapshot must be newer than this
      aggregate: best              # best (default) | median | worst
    extensions: {}                 # provider-specific quality keys
```

- **qubits** — devices with fewer are filtered out.
- **technology** — array; a device qualifies if its technology is in the set.
  Registry values: `superconducting`, `trapped-ion`, `neutral-atom`,
  `photonic`, `annealer`, `spin-semiconductor`, `nv-center`, `simulator`.
- **quality floors** — see [concepts § quality floors](concepts.md#quality-floors-and-the-aggregate).
  `aggregate` chooses how the snapshot's many metric values collapse to one
  before comparison. Rejections name the aggregate and the winning value.
- **calibrationMaxAge** — a stale snapshot excludes the device before any
  floor is evaluated. Duration string (`30m`, `2h`).

A job whose requirements no device currently meets stays **PENDING** with a
condition listing which constraint failed for how many devices — it is not
rejected, because conditions can improve.

---

## spec.deadline — by when (optional)

```yaml
spec:
  deadline: "2026-07-20T09:00:00+09:00"     # RFC 3339
```

Interacts with quality floors via `scheduling.onConflict` (below).

---

## spec.scheduling — conflict resolution (optional)

```yaml
spec:
  scheduling:
    onConflict: prefer-quality    # prefer-quality (default) | prefer-deadline | reject
```

When a floor and a deadline cannot both be met (see
[concepts § onConflict](concepts.md#deadlines-vs-quality-onconflict)):

- **prefer-quality** — may run late to meet the floor; adds condition
  `DeadlineExceededWaitingForQuality` when the deadline passes.
- **prefer-deadline** — binds by the deadline even if it violates the floor;
  records `status.placement.floorsRelaxed: true` with the violated floors and
  actual values.
- **reject** — fails with `CAPABILITY_MISMATCH` (retriable) and condition
  `UnsatisfiableBeforeDeadline`.

---

## spec.budget — at what cost (optional)

```yaml
spec:
  budget:
    maxCost: { amount: 25, currency: USD }   # advisory ceiling
    limits: { shots: 20000, qpu-seconds: 120 }   # native-unit caps
```

`limits` keys are **native units** the target meters (a cap in a unit the
device cannot bill is rejected at admission). These are checked against your
project quota at submission. `maxCost` is a soft ceiling in a currency; native
units are the enforceable contract.

---

## spec.session — device affinity for iterative loops (optional)

```yaml
spec:
  session:
    join: null            # null = open a new session; or an existing sessionId
    maxDuration: 2h
```

- **open** (`join: null`, `maxDuration` set) — the job opens a session on its
  bound device; its `status.sessionId` is what later jobs join.
- **join** (`join: <sessionId>`) — the job is pinned to that session's device.
  If the session is closed/expired/foreign, the job fails `SESSION_LOST` — never
  a silent reschedule. See [concepts § sessions](concepts.md#sessions).

---

## spec.backendSelector — soft steering (optional)

Constraints that **narrow** the feasible set; they never widen it or override
requirements.

```yaml
spec:
  backendSelector:
    preferOnPrem: true                 # bias away from cloud-queue targets
    allowCloudBurst: [ibm/torino]      # cloud targets are excluded unless listed here
    denyTargets: [sim/flaky-1]         # never place here
    requireTargets: [ibm/brisbane]     # place only among these
```

`cloud_queue` targets (tasks traverse a shared vendor queue) are excluded
unless named in `allowCloudBurst`. `requireTargets` is the strongest — the
feasible set is intersected with it (this is also how session affinity pins a
job).

---

## spec.bundle — classical resources (optional, advisory)

```yaml
spec:
  bundle:
    classical: { gpus: 0, minimumGpuMemoryGB: 0 }
    interconnect: none        # none | datacenter | realtime-qpu-gpu
```

Declares co-located classical needs for hybrid workloads. Honored where the
fleet supports it; a placeholder for QPU+GPU bundle execution otherwise.

---

## Reading the result: status

You do not write `status` — the control plane does. Key fields:

```yaml
status:
  phase: SUCCEEDED
  boundTarget: sim/ibm-sherbrooke-r
  sessionId: <uuid>                      # if the job opened a session
  placement:                             # the audit trail — "why here"
    policy: calib-aware/v0
    calibrationSnapshot: <id>
    predicted: { waitSeconds: 2, successProbability: 0.58 }
    reason: "policy calib-aware/v0 selected ... among 3 feasible target(s)"
    rejected: [ { target: ..., reason: "best two-qubit error 0.0071 exceeds floor 0.006" } ]
    onConflict: prefer-deadline          # if a conflict was resolved
    floorsRelaxed: true                  # prefer-deadline only
  conditions: [ { type: ..., status: ..., message: ... } ]
  tasks: [ { id, target, state, result } ]
  usage: [ { unit: shots, amount: 1000 } ]
```

`status.placement` is the whole promise of Rabi made concrete: the policy that
chose, the snapshot it used, what it predicted, and **every rejected device
with its reason**. Render it human-first in the console's placement-audit page,
or read it with `rabi get <id>`.
