# Tangle Specification — Overview
*Spec v0.1-draft*

## 1. Architecture

Four planes. Arrows are gRPC unless noted.

```
Client plane      qctl · Python SDK · REST · Kubernetes operator · Slurm interop
                                   │
Control plane     API server → Scheduler (policy pipeline) → Binder
                  Tenancy/Quotas · Accounting · Target registry · State store
                                   │  (adapter protocol, outbound-friendly)
Adapter plane     Out-of-process drivers: QRMI · QDMI · vendor clouds · simulators
                  Optionally fronted by a per-site agent ("qlet") for private/air-gapped sites
                                   │
Device plane      QPUs (any modality) · simulator clusters · vendor cloud queues
```

The **site agent is optional**: cloud-only deployments run adapters next to the control plane.
Agents exist for credential isolation, air-gaps, and outbound-only networking — they add no
semantics of their own.

## 2. Terminology

| Term | Meaning |
|---|---|
| **Target** | A schedulable execution resource: a QPU, a simulator, or a vendor cloud backend, exposed by exactly one adapter |
| **Adapter** | An out-of-process gRPC server implementing `tangle.adapter.v1alpha1` for one or more Targets |
| **QuantumJob** | The declarative unit of user intent (see `quantumjob.md`) |
| **Task** | The adapter-level unit: one submission of a payload to one Target (a job may fan out to ≥1 tasks) |
| **Session** | A scheduler-honored affinity window binding successive tasks to one Target (iterative/hybrid loops) |
| **Bundle** | A co-allocation request: quantum + classical resources + interconnect class, granted together or not at all |
| **Tenant** | The accounting/quota boundary (organization → project hierarchy) |
| **Calibration snapshot** | A timestamped, provenance-carrying set of device quality metrics |

## 2a. Technology registry (RFC-0001, normative)

`TargetInfo.technology` and `QuantumJob.requirements.technology` match exactly and
case-sensitively against canonical, lowercase kebab-case strings from this open registry
(extended by spec-repo PR, not by proto release):

`superconducting` · `trapped-ion` · `neutral-atom` · `photonic` · `annealer` ·
`spin-semiconductor` · `nv-center` · `simulator`

Adapters MUST use a registry value when one applies and MAY use a novel string, which
SHOULD be proposed to the registry. `Capabilities.cloud_queue` declares that tasks
traverse a shared vendor cloud queue outside the site's control; it drives
`backendSelector.preferOnPrem` / `allowCloudBurst` filtering. Until spec v0.3, control
planes MAY fall back to `vendor_extensions["technology"]` / `["cloud"]`; from v0.3 the
fallback is removed and those extension keys are reserved.

## 3. QuantumJob lifecycle (normative)

```
PENDING → SCHEDULED → SUBMITTED → RUNNING → SUCCEEDED
   │           │           │          │    ↘ FAILED (terminal, with ErrorDetail)
   │           │           │          │
   └───────────┴───────────┴──────────┴──→ CANCELLED (terminal, user- or policy-initiated)
```

- `PENDING`: accepted, validated, awaiting placement. Placement retries stay here.
- `SCHEDULED`: bound to a Target; `status.boundTarget` and `status.placement` (reason, calibration
  snapshot ID used, predicted quality) MUST be recorded before submission — this is the audit trail.
- `SUBMITTED`/`RUNNING`: task(s) in flight at the adapter. Adapters report transitions; the control
  plane is the source of truth for job-level state.
- Terminal states are immutable. Requeue = new job with `spec.retryOf` set.
- A job whose Target degrades below `requirements.quality` before RUNNING MAY be returned to
  `PENDING` (policy-controlled reschedule); after RUNNING it MUST NOT be silently migrated.

## 4. Error semantics (normative categories)

Adapters map vendor errors into exactly one category (`ErrorDetail.category`):

`INVALID_PROGRAM` · `CAPABILITY_MISMATCH` · `DEVICE_OFFLINE` · `CALIBRATION_STALE` ·
`CAPACITY_EXHAUSTED` · `BUDGET_EXCEEDED` · `SESSION_LOST` · `VENDOR_ERROR` (catch-all, must
preserve `vendorCode`/`vendorMessage`) — each flagged `retriable: true|false`.

Categories, not vendor strings, drive scheduler behavior (retry, reschedule, blacklist-for-window).

## 5. Quality metrics (normative)

Every metric consumed by scheduling carries: `name`, `value`, `unit`, `modality`,
`measuredAt`, `source` (who measured), `methodology` (free-form but required), and optional
`confidence`. The spec defines *transport*, not equivalence: implementations MUST NOT treat
metrics with different `methodology` as directly comparable without an explicit, logged
normalization policy.

## 6. Usage & accounting (normative)

Adapters report consumption in **native units** (`qpu-seconds`, `shots`, `tasks`, `credits`,
vendor-specific) per task. The control plane stores native usage immutably; normalization to
tenant-facing cost is an accounting-layer policy with its own audit record.

## 7. Conformance

A driver is Tangle-conformant when it passes the suite in `tangle-conformance` for the
capabilities it declares. Declaring a capability (e.g. sessions) obligates passing its tests;
not declaring it is always legal. See `conformance/README.md`.
