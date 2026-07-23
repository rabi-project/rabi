# Rabi concepts

Start here. This explains what Rabi is, the mental model behind it, and the
handful of ideas you need before submitting real work. No commands — see the
[quickstart](quickstart.md) to run something, the [QuantumJob
reference](quantumjob-reference.md) for every field, and the [rabi
reference](rabi-reference.md) for the CLI.

## What Rabi is

Rabi is a **control plane for quantum compute fleets**. You have more than one
place to run quantum programs — real QPUs from different vendors, cloud
queues, simulators — and they differ in qubit count, error rates, price,
queue time, and *how those error rates drift day to day*. Rabi is the layer
that decides **where each job runs**, and records **why**.

You do not tell Rabi *which device* to use. You tell it *what you need* — the
program, how good the result must be, by when, at what cost — and it places
the job on the device where that job will actually succeed, using each
device's **current calibration**. Then it writes down its reasoning so you
can argue with it instead of trusting it.

If you have used Kubernetes: Rabi is to QPUs what a scheduler is to nodes —
you declare a workload and constraints, the scheduler binds it to a fit. The
difference is that a QPU's "fitness" changes hour to hour as it recalibrates,
so placement is a *live* decision against *live* device data, not a static
match.

## The one idea that matters: calibration-aware placement

A quantum device is not a fixed thing. Its two-qubit gate error, readout
error, and which qubits are usable all **drift** — a device that was the best
choice this morning may be mediocre this afternoon after a recalibration.
Vendors publish a *calibration snapshot* describing the current state.

Most people pick a device by its *advertised* specs and hope. Rabi instead
reads the **live calibration snapshot** at the moment of placement and asks,
for your specific job: *which device, right now, gives this the best chance of
a good result?* That is the whole thesis, and it is measurable — Rabi's
public benchmark shows calibration-aware placement beating static
device-selection on result fidelity with far fewer quality-SLO violations.

Everything else in Rabi exists to make that decision **trustworthy**:
provenance (where did this calibration number come from), audit (why did my
job land there), and conformance (can I believe what an adapter reports).

## The QuantumJob

Everything you submit is a **QuantumJob** — a declarative document (YAML or
JSON), shaped like a Kubernetes resource. Its `spec` has a few parts; you only
fill in what you need:

- **workload** — *what to run*. A program in a format (e.g. OpenQASM 3) under
  a modality (`gate-model`, `analog-hamiltonian`, `annealing`, `pulse`,
  `logical`), plus shots.
- **requirements** — *what the device must provide*: qubit count, technology
  (`superconducting`, `trapped-ion`, …), and **quality floors** — the maximum
  error you will tolerate (e.g. `twoQubitErrorMax: 0.006`).
- **deadline / budget** — *by when* and *at what cost* (native-unit caps like
  shots or qpu-seconds).
- **scheduling / session / backendSelector** — how to resolve conflicts,
  whether to pin successive jobs to one device, and soft steering
  (prefer on-prem, allow cloud burst, deny specific targets).

Full field-by-field detail is in the [QuantumJob
reference](quantumjob-reference.md). The point of the shape: you describe
*intent and constraints*, never *commands*. Rabi turns intent into a
placement.

## Quality floors, and the aggregate

A quality floor like "two-qubit error ≤ 0.006" needs a rule for *which* error
value to check — a 156-qubit device has hundreds of them. Rabi's default is
**best**: a device is feasible if **at least one region** of it meets the
floor, because transpilation steers your circuit toward that good region. You
can override per job to `median` (typical quality) or `worst` (every region
must pass — strict, for conservative sites):

```yaml
requirements:
  quality:
    gateModel:
      twoQubitErrorMax: 0.006
      aggregate: best      # best (default) | median | worst
```

The placement audit always tells you the aggregate used and the winning
value, so a rejection reads like *"best two-qubit error 0.0071 exceeds floor
0.006"* — arguable, not magic.

## Deadlines vs. quality: onConflict

A floor and a deadline can become unsatisfiable together — no device meets the
quality bar before your deadline. That is a real decision, and Rabi makes it
**yours** rather than deciding silently:

- `prefer-quality` (default) — wait past the deadline for a device that meets
  the floor. Your result stays good; it may be late.
- `prefer-deadline` — at the last moment that still meets the deadline, bind
  to the best available device *even if it violates the floor*, and record the
  violation explicitly (`floorsRelaxed`, with the actual values).
- `reject` — fail the job rather than compromise either constraint.

```yaml
spec:
  scheduling:
    onConflict: prefer-quality
```

Whichever you choose, the placement audit records it. Rabi never quietly
trades one of your constraints away.

## Sessions

Iterative and hybrid algorithms (VQE, QAOA loops) need successive jobs on the
**same** device to avoid re-queuing and to keep calibration consistent. A
**session** is an affinity window: open one, and every job that joins it lands
on the same target. If the session expires or closes, joining jobs fail
explicitly with `SESSION_LOST` — never a silent reschedule onto a different
device, which would corrupt an iterative loop.

## The job lifecycle

A job moves through a small, strict state machine:

```
PENDING → SCHEDULED → SUBMITTED → RUNNING → SUCCEEDED
   │                                       ↘ FAILED
   └──────────────────────────────────────→ CANCELLED
```

- **PENDING** — accepted and validated, waiting for a feasible placement. A
  job with no feasible device *stays here* with a condition explaining which
  constraint failed for how many devices. It is not an error; conditions may
  improve (a device recalibrates, a queue drains).
- **SCHEDULED** — bound to a device; `status.placement` records the reason,
  the calibration snapshot used, and predicted quality **before** anything
  runs. This is the audit trail.
- **SUBMITTED / RUNNING** — the task is in flight at the adapter.
- **SUCCEEDED / FAILED / CANCELLED** — terminal and immutable. To retry, you
  submit a new job.

Watch it live with `rabi watch <id>`; every transition is delivered in order
from an append-only history, so you never miss one.

## Provenance: why you can trust the numbers

Every calibration metric Rabi schedules on carries its **origin**: which
snapshot, measured when, by what methodology (`RB`, `vendor-reported`,
`qrmi-relayed`, …). The console renders the calibration *age* and methodology,
not just the value. This is deliberate — a scheduler that decides on numbers
you cannot trace is not better than guessing. Adapters that misreport are
caught by the conformance harness before they ever join a fleet.

## Tenancy: orgs, projects, quotas

Work is organized as **org/project** (the "tenant"). Projects have **quotas**
in native units (shots, qpu-seconds) enforced at submission, and **fair-share
weights** so competing projects get proportional access under contention.
Accounting is an append-only ledger — usage is metered per task and can be
normalized to cost under a versioned, replayable policy. You act within your
project; what you can see and submit is scoped to your credential.

## Adapters and the fleet

Rabi does not talk to vendors directly. Each device or cloud is fronted by an
**adapter** — a small out-of-process service implementing a standard gRPC
protocol. The control plane speaks only that protocol, so any vendor can be
added without touching Rabi. Every adapter must pass a **conformance harness**
for the capabilities it declares before it is trusted — Rabi ships five
certified adapters (Aer simulator, IBM, QRMI, QDMI, IQM) and a documented
recipe for writing your own ([conformance-authors.md](conformance-authors.md)).
See [operating a fleet](fleet.md) for how to register many devices and how
fleet breadth scales.

## Where to go next

- **Run something:** [quickstart.md](quickstart.md)
- **Write a job:** [quantumjob-reference.md](quantumjob-reference.md)
- **Use the CLI:** [rabi-reference.md](rabi-reference.md)
- **Call the API:** [api-guide.md](api-guide.md)
- **Operate a deployment:** [site-install-guide.md](site-install-guide.md),
  [security-checklist.md](security-checklist.md)
