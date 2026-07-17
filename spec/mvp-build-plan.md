# Tangle MVP — Agent-Executable Build Plan
### v1.0 · July 2026 · Hand this document, plus the `tangle-spec` skeleton, to the coding agent.

---

## Kickoff prompt (paste this to the agent, attaching this file + tangle-spec/)

> You are building the MVP of Tangle, an open-source control plane for quantum compute fleets.
> Read `mvp-build-plan.md` completely before writing any code — it defines the mission, hard
> constraints, milestones M0–M8, and explicit non-goals. The `tangle-spec/` directory is the
> source of truth for all protocols and schemas; do not modify it without flagging the change
> as a spec question. Work strictly milestone by milestone: do not start milestone N+1 until
> milestone N's acceptance criteria all pass and are demonstrated by a committed test or script.
> Keep commits small and messages precise. When a decision is not covered by this document,
> choose the boring option and record it in `docs/decisions.md`. Testing requirements and
> quantitative pass bars for every milestone live in `test-and-verification-plan.md` — its
> per-milestone suites (§2) are part of each milestone's acceptance criteria.

---

## 1. Mission

Build the thing that makes Tangle irrefutable. Two artifacts, nothing else matters:

**Artifact A — the five-minute demo.** `docker compose up` starts a working control plane
managing a mixed fleet: three simulated QPUs whose noise and calibration replay *real device
data*, plus (when a token is provided) one real IBM Quantum cloud backend. A seed script submits
~20 `QuantumJob`s with different quality floors, deadlines, and budgets; `qctl` shows them being
filtered, scored, bound, executed, and accounted — each with a recorded, human-readable placement
reason. Runs on a laptop, offline by default, no quantum hardware required.

**Artifact B — the number.** A reproducible benchmark (`make bench`, <30 min on a laptop)
comparing three placement policies over the same replayed-calibration fleet and workload:
`static-best` (always the nominally best device — what users do today), `round-robin`, and
`calib-aware` (ours). Report: mean result fidelity vs. ideal, quality-SLO violation rate, mean
wait time. Output: CSV + charts + a generated `bench/report.md`. This becomes a public technical
report; treat it with paper-grade rigor (seeded, versioned data, honest methodology section).

**Why these two:** the demo converts design partners; the number converts skeptics. Everything
in this plan exists to serve one of them.

## 2. Hard constraints

- **Languages:** Go ≥1.22 for the control plane, CLI, and operator. Python ≥3.11 for adapters
  and SDK. Nothing else.
- **The spec is law.** All wire contracts come from `tangle-spec/proto/` (vendor the protos or
  add the repo as a submodule; generate code — never hand-write message types). The `QuantumJob`
  document validates against `tangle-spec/schemas/quantumjob.schema.json` at admission.
- **Storage:** PostgreSQL 15 via `pgx`. Single node. No etcd, no Redis, no NATS, no Kafka, no
  message broker of any kind — job dispatch uses Postgres (`FOR UPDATE SKIP LOCKED` work queues
  + `LISTEN/NOTIFY` for wakeups).
- **Topology:** ONE control-plane binary (`tangled`) containing API server, scheduler, registry,
  and accounting. Adapters are separate processes speaking `tangle.adapter.v1alpha1` over gRPC.
  No other services. No microservices.
- **Auth:** a single static API key from env (`TANGLE_API_KEY`). Multi-tenancy exists only as a
  `tenant` string recorded on jobs, quotas, and usage (real tenancy is post-MVP — but the string
  must flow through everything now).
- **Determinism:** every stochastic component (simulators, synthetic drift, workload generator,
  NSGA-style search if used) takes an explicit seed. `make bench` twice ⇒ identical CSVs.
- **License hygiene:** Apache-2.0 `LICENSE`, SPDX header in every file, DCO note in
  `CONTRIBUTING.md`, no copyleft dependencies.
- **CI:** GitHub Actions — lint (golangci-lint, ruff), unit tests, proto codegen check
  (generated code matches committed), and an integration job that runs the compose stack and
  executes a smoke job through the Aer adapter.

## 3. Repository layout (monorepo `tangle/`)

```
tangle/
├── cmd/tangled/            # control-plane binary (API, scheduler, registry, accounting)
├── cmd/qctl/               # CLI: submit, get, watch, cancel, targets, usage, bench helpers
├── internal/api/           # gRPC (tangle.api.v1alpha1) + REST gateway + schema validation
├── internal/store/         # Postgres: migrations (goose/atlas), repositories, work queue
├── internal/registry/      # target registry: adapter dialing, capability & state cache
├── internal/scheduler/     # policy pipeline: filter → score → bind (see §5)
├── internal/accounting/    # native-unit usage ledger
├── adapters/aer/           # Python: AdapterService over Aer, calibration replay (§4)
├── adapters/ibm/           # Python: AdapterService over qiskit-ibm-runtime (token-gated)
├── sdk/python/tangle_client/  # thin client: submit/get/watch; Qiskit circuit → QuantumJob helper
├── operator/               # M8 stretch: kubebuilder operator, QuantumJob CRD
├── bench/                  # workload generator, runner, analysis, report template, data/
├── deploy/compose/         # docker-compose.yml + seed.sh (Artifact A)
├── docs/                   # quickstart.md, architecture.md, decisions.md
└── spec/                   # vendored tangle-spec (read-only)
```

## 4. The calibration-replay fleet (the MVP's soul — build it well)

The simulated fleet must be *defensibly realistic*, because Artifact B's credibility rests on it.

- **Source data:** device snapshots from `qiskit-ibm-runtime`'s fake-backend providers (e.g.
  three distinct ≥20-qubit fake backends), which ship real historical calibration data offline.
  Store extracted snapshots as versioned JSON under `bench/data/snapshots/` with provenance
  notes (which backend, package version, extraction script).
- **Drift model (documented honestly):** fake backends give one snapshot each. Synthesize a
  time series per device: a seeded random walk on gate/readout errors (bounded, e.g. ±30% around
  the real snapshot values) with a sawtooth reset at simulated calibration events every N hours.
  Label it clearly in the report as *synthetic drift over real calibration baselines*. Optional
  flag: when `IBM_TOKEN` is set, record real longitudinal snapshots to enrich the dataset.
- **Noise realization:** the Aer adapter builds its noise model *from the current snapshot* —
  depolarizing error per gate from reported gate errors, readout error from reported readout
  fidelities, T1/T2 thermal relaxation. When the replay clock advances, the noise model updates.
  The adapter's `GetDeviceState` returns the same snapshot (with full `Metric` provenance,
  `methodology: "replayed-vendor-calibration"`) that the noise model uses — so the scheduler
  sees exactly what the physics does.
- **Replay clock:** a fleet-wide simulated clock (env-controlled acceleration, e.g. 1s wall =
  10 min sim) so a 30-minute benchmark spans days of calibration drift.

## 5. Scheduler v0 (exactly this, nothing fancier)

Pipeline per scheduling cycle (triggered by job arrival or 5s tick):

1. **Filter:** capability match (modality, format, qubits ≤ device qubits), quality floors
   evaluated against the *current* calibration snapshot (respecting `calibrationMaxAge`),
   maintenance windows, `backendSelector`, budget-unit sanity.
2. **Score** (policy = `calib-aware/v0`): for each feasible target, compute
   **ESP — Estimated Success Probability** = ∏(1 − ε_g) over the gates of the circuit
   *transpiled to that target* × ∏(1 − ε_ro) over measured qubits — the standard proxy from the
   literature (Ravi et al.). Combine: `score = w_q·ESP_norm − w_t·predicted_wait_norm − w_c·cost_norm`
   with weights from job intent (deadline present ⇒ w_t↑; quality floor tight ⇒ w_q↑; document
   the mapping in `docs/decisions.md`). Transpile via Qiskit inside the scoring path (Python
   sidecar or precomputed per-target depth estimates — decide, record, keep deterministic).
3. **Bind:** write placement record (policy id, snapshot id, ESP prediction, expected wait,
   human-readable reason listing filtered/rejected targets), enqueue task to adapter.

Policies implement a Go interface `SchedulingPolicy{Filter, Score}` registered by name —
`static-best` and `round-robin` are implemented as trivial policies so the benchmark compares
like with like inside the same machinery.

## 6. Milestones and acceptance criteria

**M0 — Scaffold.** Repo builds; CI green; protos vendored + codegen committed; LICENSE/SPDX/DCO
in place; `docker compose up` starts `tangled` + Postgres (no adapters yet); `qctl targets`
returns an empty list over the real API.
*Accept:* CI badge green; fresh-clone quickstart ≤5 commands reaches "0 targets".

**M1 — Job store + API.** SubmitJob validates against the JSON Schema (table-driven tests:
valid multi-modal examples + ≥10 rejection cases); job persists; state machine transitions
enforced in one place with unit tests; `qctl submit/get/watch` work; `dry_run` returns
validation-only.
*Accept:* invalid `workload.kind` rejected at admission with a precise error; job lifecycle
PENDING→CANCELLED via `qctl cancel`; restart of `tangled` loses nothing.

**M2 — Adapter protocol + Aer adapter (single target, no replay yet).** Python adapter serves
`ListTargets/GetCapabilities/GetDeviceState/SubmitTask/WatchTask/CancelTask` for one Aer-backed
target with a static snapshot; registry dials, caches capabilities, polls state; a submitted
Bell-pair `QuantumJob` (openqasm3, 1000 shots) returns a counts histogram end-to-end.
*Accept:* integration test in CI: compose up → submit Bell job → SUCCEEDED with |00⟩+|11⟩
dominant counts; idempotency test (same key twice ⇒ one execution); usage record present
(`shots: 1000`).

**M3 — Scheduler skeleton.** Policy interface + `fifo` policy binding to the only feasible
target; placement audit record written; three Aer targets with different static snapshots;
filter correctness tests (qubit count, format, quality floor each excluding the right targets).
*Accept:* golden tests — given fleet state X and job Y, placement record matches expectations
exactly (these goldens are the scheduler's regression suite forever).

**M4 — Calibration replay.** §4 built: snapshot extraction script, versioned data, drift
synthesis, replay clock, noise-model-from-snapshot in the Aer adapter, `GetDeviceState`
reflecting replayed metrics with provenance.
*Accept:* the same circuit run on the same target at sim-time T0 (fresh calibration) vs T0+20h
(drifted) shows measurably different fidelity vs ideal (test asserts direction, tolerant
magnitude); scheduler-visible metrics equal noise-model inputs (single-source test).

**M5 — `calib-aware/v0` policy.** §5 fully: ESP scoring with per-target transpilation, weight
mapping from job intent, plus `static-best` and `round-robin` reference policies.
*Accept:* unit tests on ESP arithmetic against hand-computed values; property test — tightening
a quality floor never worsens the chosen target's ESP; golden placements updated and reviewed.

**M6 — The benchmark (Artifact B).** `bench/`: workload generator (mixed widths 2–20 qubits,
mixed shot counts, ~30% with deadlines, ~40% with quality floors; circuits from MQT Bench or
QASMBench — vendor a fixed subset with attribution), runner executing the identical workload
under each policy against the identical replay timeline, analysis producing CSV + charts +
`report.md` with a methodology section (data provenance, drift synthesis, ESP definition,
limitations — including "synthetic drift" and "simulator-measured fidelity").
*Accept:* `make bench` twice ⇒ byte-identical CSVs; report renders with all three policies;
*no acceptance criterion on which policy wins* — if `calib-aware` doesn't beat `static-best`
on fidelity and SLO violations, that's a finding to debug openly, not to hide.

**M7 — Demo polish (Artifact A).** Compose file with 3 replay targets; `seed.sh` submitting the
20-job mix; `qctl watch --all` live view; IBM adapter behind `IBM_TOKEN` (feature-flagged, off
by default; docs warn about open-plan queue times); `docs/quickstart.md` — clone to demo in ≤5
commands; a recorded terminal session (asciinema or GIF) checked into `docs/`.
*Accept:* a stranger on a clean machine reaches the routed-jobs view in under 10 minutes
following only the quickstart.

**M8 — Kubernetes operator (stretch — only after M7 ships).** kubebuilder operator: `QuantumJob`
CRD mirroring the schema; controller submits to `tangled` API and reflects status into CRD
status; kind-based e2e test.
*Accept:* `kubectl apply -f examples/bell.yaml` → `kubectl get quantumjobs` shows SUCCEEDED.

## 7. Non-goals — the agent MUST NOT build these (v0 will reject the PR)

Real auth/SSO/RBAC · real multi-tenancy beyond the tenant string · QRMI/QDMI drivers · sessions
· space-sharing/multi-programming · circuit cutting · web console/UI · HA/clustering · Helm
chart (compose only) · accounting normalization/pricing (native units only) · any new IR or
circuit format · any scheduling technique beyond §5 (no RL, no genetic algorithms — that's a
post-MVP policy plugin).

Each of these has a designed seam waiting for it (see `full-build-plan.md`); building it early
burns the demo timeline and creates unreviewed API surface.

## 8. Definition of done

M0–M7 accepted + README with the two-paragraph pitch + `bench/report.md` publishable as-is +
tagged `v0.1.0` + a fresh-clone run-through performed by a human (Edward) following only the docs.
