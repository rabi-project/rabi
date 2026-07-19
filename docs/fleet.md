# Operating a fleet

Rabi's native case is **many devices, not one**. This page explains how a
fleet is assembled, how to add or swap devices, how the scheduler reasons
across all of them, and — honestly — where the scaling boundary is today.

New to the model? Read [concepts](concepts.md) first.

## The model: sites, targets, fleet

Two nouns:

- A **target** is one device you can run on — a QPU, a cloud backend, a
  simulator. Targets are named fleet-scoped as **`<site>/<target_id>`**
  (e.g. `ibm/marrakesh`, `sim/ibm-torino-r`).
- A **site** is one **adapter endpoint** — a process speaking the
  `tangle.adapter.v1alpha1` gRPC protocol. Rabi talks only to adapters,
  never to vendors directly.

**One adapter can front one target or many.** The Aer adapter serves three
replay QPUs from a single config; the QRMI adapter can expose several
QRMI-managed resources from one process. So a "site" is a logical grouping,
not necessarily one machine.

The **fleet** is simply the union of every target across every registered
site. The scheduler sees them all as one pool and compares them per job.

```
RABI_ADAPTERS ─▶ site "ibm"  (adapter @ host:50052) ─▶ ibm/marrakesh, ibm/brisbane
                 site "iqm"  (adapter @ host:50055) ─▶ iqm/garnet
                 site "sim"  (adapter @ host:50051) ─▶ sim/a, sim/b, sim/c
                                                        └── the fleet ──┘
```

## Registering devices

The control plane discovers its fleet from one environment variable:

```sh
RABI_ADAPTERS="ibm=ibm-host:50052,iqm=iqm-host:50055,sim=aer-host:50051"
```

Each comma-separated entry is `site=host:port`. On start, the registry dials
every adapter, caches its capabilities, and polls device state (calibration,
queue depth, maintenance) continuously. `qctl targets` shows the live fleet.

**You never edit Rabi's code to add a device.** Adding hardware is one of:

1. **More targets on an existing adapter** — e.g. add a backend to the Aer
   replay config or pass another `--resource` to the QRMI adapter. The new
   target appears under that site automatically.
2. **Another adapter endpoint** — run an adapter for the new device and add
   `newsite=host:port` to `RABI_ADAPTERS`. Restart `rabi`; the site joins.

Swapping or retiring a device is the reverse: drop its entry (or its target
from the adapter's config). In flight jobs finish; new placement stops
considering it.

## The certified adapters, and what each fronts

| Adapter | Fronts | Multiple targets per process |
|---|---|---|
| **Aer** (`rabi-adapter-aer`) | local/GPU simulators, calibration-replay QPUs | yes — one config, many targets |
| **IBM** (`rabi-adapter-ibm`) | IBM Quantum backends (qiskit-ibm-runtime) | yes — `IBM_BACKENDS` list |
| **QRMI** (`rabi-adapter-qrmi`) | QRMI-managed resources (IBM Direct Access, Qiskit Runtime, Pasqal Cloud, …) | yes — repeat `--resource` |
| **QDMI** (`rabi-adapter-qdmi`) | any device exposing the QDMI C interface | one device library per process |
| **IQM** (`rabi-adapter-iqm`) | IQM Resonance quantum computers | per `--server` |

A device covered by one of these is a **config line**. A novel device is
"write an adapter, certify it, register it" — see
[conformance for driver authors](conformance-authors.md). Every adapter must
pass the conformance harness for the capabilities it declares before it is
trusted; you are never your own exception.

## How the scheduler uses the whole fleet

Every job runs the pipeline **filter → score → bind** over *all* targets:

1. **Filter** — drop targets that can't satisfy the job's hard requirements
   (qubits, technology, program format, quality floors against live
   calibration, budget-unit support, `backendSelector`).
2. **Score** — rank the survivors on live calibration quality (the
   `calib-aware/v0` policy predicts success probability).
3. **Bind** — pick the best and record the placement audit, including every
   *rejected* target and why.

Adding a device widens the pool the scheduler chooses from — no other change
is needed. A bigger, more varied fleet means more jobs find a good home.

## Steering placement across a mixed fleet

Constraints on the job narrow the feasible set (they never override
requirements). Useful across a heterogeneous fleet:

```yaml
spec:
  requirements:
    technology: [superconducting]     # only these device technologies
  backendSelector:
    preferOnPrem: true                # bias away from shared cloud queues
    allowCloudBurst: [ibm/marrakesh]  # cloud targets excluded unless listed
    requireTargets: [iqm/garnet]      # place only among these
    denyTargets: [sim/flaky-1]        # never here
```

Mixed fleets work today: the reference deployment routinely schedules across
replay QPUs + a cloud backend + a GPU-backed simulator in one fleet, steering
jobs by `technology` and selector to the right segment. See the
[QuantumJob reference](quantumjob-reference.md) for every selector.

## Provenance stays per target

Each target carries its own calibration snapshot with provenance — measured
when, by what methodology — surfaced in `qctl targets -o json` and the
console's fleet view. A large fleet doesn't blur this; every device's numbers
remain individually traceable, which is what keeps placement auditable.

## Verify the fleet

```sh
qctl targets                     # every target, live status + calibration age
qctl targets -o json | jq '.targets[].name'
```

Submit a job with no selector and read `status.placement.rejected` — it lists
every device the scheduler considered and passed over, so you can see the
whole fleet being weighed.

## The scaling boundary — read this

Rabi scales along two different axes, at different maturity:

- **Breadth — more devices.** *Works now.* Adding QPUs is adding adapters;
  the scheduler already reasons over a heterogeneous fleet, and a single site
  can operate many devices today. This is the dimension this page is about.
- **Depth — more load, higher availability.** *Not yet.* The control plane
  runs as a **single instance** backed by one Postgres. If that process
  stops, scheduling pauses until it restarts — **jobs and history are safe in
  Postgres, nothing is lost**, but there is no automatic failover, no
  clustering, and no horizontal scale-out. That is deliberate for pilot-grade
  alpha and is the work of the next phase.

The architecture is built so depth is a build-out, not a redesign: all state
lives in Postgres, binding is an atomic row-locked operation, and adapters are
stateless and out-of-process. But until that phase lands, plan for **one
control-plane instance operating a fleet of many devices** — which is exactly
right for a friendly pilot site, and honest about what it is not.
