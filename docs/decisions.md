# Decisions

Running log of choices not covered by `mvp-build-plan.md`. Boring options,
recorded so they stay arguable. Format: ID · date · decision · why.

## D-001 · 2026-07-17 · Spec vendored by copy at `spec/`

The `tangle-spec` repository is copied (not submoduled) into `spec/` and
treated as read-only. A copy keeps fresh clones self-contained (no submodule
init step in the quickstart) and CI hermetic. Syncing is a manual
`rsync` from the spec repo, reviewed like any other PR.

## D-002 · 2026-07-17 · Generated code lives in-module via buf managed mode

The spec's `go_package` options point at `tangle.dev/spec/...`, a module that
does not exist. Rather than publish a second module or edit the spec, buf
managed mode overrides `go_package_prefix` to `tangle.dev/tangle/gen/go` at
codegen time. Spec files stay byte-identical; generated code is committed and
CI verifies `make gen` produces no diff.

## D-003 · 2026-07-17 · buf lint: four naming rules relaxed — SPEC QUESTION

The spec's protos use compact shared request/response types (`TargetRef`,
`TaskRef`, `SessionHandle`, `Job`) and unprefixed enum values, which fail buf
STANDARD rules `RPC_REQUEST_STANDARD_NAME`, `RPC_RESPONSE_STANDARD_NAME`,
`RPC_REQUEST_RESPONSE_UNIQUE`, `ENUM_VALUE_PREFIX`. The spec is law, so those
four rules are excepted in `buf.yaml` instead of renaming spec messages.
**Flagged as a spec question:** `tangle-spec/CONTRIBUTING.md` claims
"buf lint (DEFAULT)" compliance, but the published protos do not pass it;
either the spec should adopt the standard names via RFC or officially declare
the relaxed lint profile.

## D-004 · 2026-07-17 · REST gateway via grpc-gateway + external API config

The API proto has no `google.api.http` annotations (editing it would change
the spec). grpc-gateway supports an external gRPC API Configuration file, so
HTTP bindings live in `api-config.yaml` following the conventions named in the
proto header (`POST /v1alpha1/jobs`, etc.). The gateway dials tangled's own
gRPC listener over loopback so streaming endpoints (WatchJob) behave
identically over REST.

## D-005 · 2026-07-17 · Auth header is `Authorization: Bearer <key>`

One static key (mvp-build-plan.md §2) presented as a standard Bearer token on
both gRPC metadata and REST. Constant-time comparison; `/healthz` is the only
unauthenticated path (compose healthchecks).

## D-006 · 2026-07-17 · Go module path `tangle.dev/tangle`; CLI uses cobra; migrations use goose

Module path matches the spec's domain. cobra is the standard Go CLI library.
goose runs embedded SQL migrations automatically at tangled startup — a single
binary must bootstrap its own schema with no operator steps.

## D-007 · 2026-07-17 · Ports: gRPC :9090, HTTP/REST :8080

Conventional defaults, overridable via `TANGLE_GRPC_ADDR`/`TANGLE_HTTP_ADDR`.

## D-008 · 2026-07-17 · SPDX scope: all comment-capable source files

"SPDX header in every file" is enforced (hack/check-spdx.sh) on `.go`, `.py`,
`.sh`, `.sql`, `.yaml/.yml`, `Dockerfile`, `Makefile`. JSON and Markdown do
not support comments cleanly and are covered by the repository LICENSE.
`spec/` (vendored, upstream provenance) and `gen/` (generated from spec) are
excluded from the check.

## D-009 · 2026-07-17 · Schema embedded via `make gen` copy, sync-tested

Go embed cannot reach outside a package directory, so the admission schema is
copied to `internal/specdata/` by `make gen`. A unit test asserts the copy is
byte-identical to `spec/schemas/quantumjob.schema.json`, and `make gen-check`
(CI) fails when the copy is stale. The spec file remains the only source.

## D-010 · 2026-07-17 · FSM details the spec diagram leaves open — SPEC QUESTION (minor)

Adopted transitions beyond the base diagram: SCHEDULED→PENDING and
SUBMITTED→PENDING (the spec allows returning to PENDING "before RUNNING" on
quality degradation) and SCHEDULED/SUBMITTED→FAILED (payloads can be
unsubmittable after binding — e.g. unresolvable source URI — and adapters can
reject at submission with INVALID_PROGRAM; the diagram only draws FAILED from
RUNNING). Worth an editorial clarification in the spec's lifecycle section.

## D-011 · 2026-07-17 · dry_run returns the validated document, no job_id

`dry_run` responses carry the accepted document plus a
`Validated/DryRun` condition, with empty `job_id` — making "nothing was
enqueued" visible to clients. Placement simulation is deferred until a
scheduler exists (M3+). Admission checks not implementable in the MVP are
recorded here: tenant existence/quota headroom (no tenancy system, tenant is
a string) and `session.maxDuration ≤ tenant policy max` (no tenant policy);
both admit unconditionally. Known native units start as the four the spec
names (qpu-seconds, shots, tasks, credits) and extend with adapters'
declared `billing_units` once the fleet is non-empty.

## D-012 · 2026-07-17 · Coverage floors measured over internal/

The T&V plan's floors (scheduler & FSM ≥90%, store ≥85%, overall ≥75%) are
enforced by hack/coverage-check.sh over `internal/...` — hand-written
control-plane logic. Generated `gen/`, vendored `spec/`, and thin `cmd/`
entrypoints are excluded; cmd binaries are exercised end-to-end by the smoke
suites instead. Watch streams poll the append-only `job_events` table (250ms)
for M1; LISTEN/NOTIFY wakeups arrive with the M2 dispatcher.

## D-013 · 2026-07-17 · Python toolchain: uv + Python 3.13, generated stubs committed

The Aer adapter pins Python 3.13 (qiskit/qiskit-aer wheels are not yet
published for 3.14) and uses uv for env management. gRPC stubs are generated
by `make gen-python` into `adapters/aer/src/tangle/` (so absolute imports
resolve) and committed; `make gen-check` enforces freshness.

## D-014 · 2026-07-17 · Adapter execution model: per-target single worker, delay knob

One worker thread per target gives an honest, observable queue (queue_depth =
queued+running) and makes cancellation semantics testable. The test-only
`tangle.sim/delay-ms` parameter holds tasks in QUEUED/RUNNING long enough for
deterministic cancellation tests — it is a simulator affordance, documented,
not hidden. Aer seeds derive from (target seed, idempotency key), so replays
of the same task are bit-identical. Inline payloads only in the MVP; a
`program.source` URI fails fast as INVALID_PROGRAM with a precise message
(admission cannot reject it: the schema allows URIs and the fleet may gain a
resolver post-MVP).

## D-015 · 2026-07-17 · Dispatch: task_id doubles as the adapter idempotency key

The control plane creates one task row per placement inside the bind
transaction; its UUID is the `idempotency_key` sent to the adapter. After a
restart, `resume()` re-submits active tasks with the same key — the adapter
conformance contract (category 3) guarantees no duplicate execution. Usage
recording is idempotent via UNIQUE (task_id, unit) on the append-only ledger.
M2's target selection is `direct/v0` (first feasible target, rejection
reasons recorded); M3 replaces exactly that function with the policy
pipeline.

## D-016 · 2026-07-17 · Scheduler filter semantics — two SPEC QUESTIONS

(a) **Device technology has no field in the adapter protocol** although
`requirements.technology` participates in filtering. Adapters expose it via
`Capabilities.vendor_extensions["technology"]` for now; the protocol should
grow a first-class field by RFC. Likewise `vendor_extensions["cloud"]="true"`
marks cloud-queue targets for `allowCloudBurst` filtering.
(b) **Quality floors are evaluated against the device's best (minimum) metric
value** — a device is feasible when at least one qubit/edge meets the floor,
since transpilation can steer toward the good region. The spec says floors
are "evaluated against a specific calibration snapshot" without fixing the
aggregate; best-value is the least surprising choice and is documented in
every rejection string ("best two-qubit error ... exceeds floor ...").

## D-017 · 2026-07-17 · Placement decisions are deterministic by construction

Targets are evaluated in lexicographic name order; score ties break toward
the first name; reason strings have fixed formats. The golden suite
(`internal/scheduler/testdata/golden/`, 24 scenarios) locks decisions
byte-for-byte; changing any golden requires the `golden-change` PR label
(CI-enforced) plus a per-scenario justification. Infeasible jobs stay PENDING
with a `Schedulable: False` condition that is re-written only when the reason
changes, so retry cycles do not spam the event history.

## D-018 · 2026-07-17 · Replay clock: sim world inside, wall clock outside

The fleet-wide replay clock (1 wall second = `TANGLE_SIM_ACCEL` sim seconds,
anchored at the earliest calibration baseline) lives entirely inside the
adapter. Control-plane-facing timestamps (`measured_at`, `observed_at`) are
mapped back to the wall timeline, so tangled and the scheduler stay
sim-agnostic. Consequence: in replay mode drift steps are seconds apart in
wall time, so `calibrationMaxAge` rarely triggers there — its filter
semantics are exercised against static-snapshot targets instead.

## D-019 · 2026-07-17 · Drift model: strictly-degrading seeded walk, +30% cap

Each drift step adds `degradation_per_hour·Δt + |N(0, σ)|` to a cumulative
walk per metric — strictly non-decreasing within a calibration period, so
"drifted is never better than fresh" holds by construction (T4.drift needs
the direction across all seeds). Error metrics scale by (1+walk) capped at
+30% over baseline; T1/T2 scale by 1/(1+walk). Sawtooth reset at calibration
events (period per target). Every value is a pure function of (seed, metric,
cycle, step) — stateless, deterministic, and identical for concurrent
readers. Disclosed in the benchmark report as synthetic drift over real
calibration baselines.

## D-020 · 2026-07-17 · Real baselines are 20-qubit subgraphs of fake backends

Aer cannot noise-simulate 127+ qubits, so each replay target is a connected
20-qubit BFS subgraph of a real device (fake_torino/cz,
fake_sherbrooke/ecr, fake_brisbane/ecr), carrying the vendor-reported
calibration for those physical qubits and the device's native 2q gate.
Physical→logical qubit mapping and package version are embedded in
bench/data/snapshots/*.json. The scheduler's 2q quality floor matches any
`gate.2q.<gate>.error` metric.

## D-021 · 2026-07-17 · ESP v0: lexical circuit profile + best-region mapping

The plan allows "Python sidecar or precomputed per-target depth estimates" for
the transpile-in-scoring-path problem; v0 takes the estimate route,
deterministically and in-process: a lexical scan of flat OpenQASM 2/3 counts
1q/2q gates and measured qubits (ccx/cswap via standard decompositions;
control flow is an error, not a guess). ESP then assumes best-region mapping:
mean of the best `Qubits` 1q errors per 1q gate, mean of the best `Qubits−1`
2q edge errors per 2q gate, exact product over the best `MeasuredQubits`
readout errors. No routing overhead is modeled (all fleet devices are sparse
superconducting graphs, so the bias is similar across targets and cancels in
ranking) — stated as a limitation in the benchmark methodology. Unprofilable
programs fall back to a GHZ-like width-only profile. Missing metric classes
use conservative defaults (1q 0.01, 2q 0.05, readout 0.05) so opaque targets
never outrank measured ones.

## D-022 · 2026-07-17 · calib-aware/v0 weights and baselines

score = w_q·ESP − w_t·wait/(wait+60s) − w_c·0. Weight table by job intent:
(no deadline/floor) 0.60/0.25/0.15 · (deadline) 0.45/0.45/0.10 ·
(quality floor) 0.75/0.15/0.10 · (both) 0.55/0.35/0.10. cost_norm ≡ 0 in v0
because pricing/normalization is an explicit MVP non-goal; the term stays in
the formula as the post-MVP seam. static-best/v0 ranks by the device's
advertised baseline (vendor_extensions["nominal-2q-error-median"], static
per target, drift- and queue-blind — the Ravi et al. behavioral baseline);
round-robin/v0 rotates a counter over the feasible set (advances once per
job, deterministic for a deterministic job order).

## D-023 · 2026-07-17 · Property tests use seeded stdlib rand, not rapid — PLAN DEVIATION

The T&V plan names the `rapid` library, but rapid is MPL-2.0 and the build
plan's license hygiene rule ("no copyleft dependencies") wins. T5.props runs
1,200 seeded stdlib-rand cases per property instead (deterministic, no
shrinking). Also refined: the property "tightening a quality floor never
selects a lower-ESP target" is unsound as stated when a floor excludes the
previous winner via its best-edge metric (a legitimately lower-ESP reroute
follows); the tested form is the sound core — the new selection always
satisfies the tightened floor, and while the previous winner remains
feasible, ESP never drops.

## D-024 · 2026-07-17 · Benchmark harness: deterministic DES + shared physics series

Artifact B runs as a discrete-event simulation in Go (`bench/runner`) using
the real `internal/scheduler` policy code — no wall clock, no goroutines, so
runs are byte-identical per seed. Physics executes in a seeded Python batch
(`bench/scripts/execute.py`); both sides consume one exported snapshot
series (`gen_series.py`), so scheduler view and noise model cannot diverge.
Baseline policies (static-best, round-robin) filter on capability/selector
dimensions only — current practice cannot act on calibration intent
(quality floors, calibrationMaxAge); quantifying that gap is the point of
the benchmark. SLO violations are judged post-hoc against the execution-time
snapshot. Fidelity proxy is 1−TVD at 1,000 probe shots against the exact
ideal distribution, on a circuit subset curated to concentrated ideal
outputs (flat-output families are unverifiable by sampling — excluded and
disclosed). Jobs sharing a (circuit, target, snapshot) context share one
seeded measurement. Full methodology and limitations live in the generated
`bench/out/report.md`.

## D-025 · 2026-07-17 · Identity initial_layout everywhere Qiskit transpiles

Qiskit's seeded layout search (VF2 passes with wall-clock budgets) is not
run-to-run deterministic even with `seed_transpiler` fixed — observed
directly on bv_12/bv_16. Determinism is a tested property (T6.det, adapter
idempotent replays), so both the adapter and the benchmark executor pin
`initial_layout = identity` onto the BFS-ordered subgraph and let seeded
routing do the rest. The layout is identical for every policy, so rankings
are unaffected; the fidelity cost of not searching layouts is shared and
disclosed. Aer `method="automatic"` is likewise banned in the benchmark (it
picks a method from free memory at runtime) — methods are explicit.

## D-026 · 2026-07-17 · `qctl watch --all` polls ListJobs client-side

The spec API deliberately has per-job watch streams only; the demo's live
fleet view is a client-side refresh loop over ListJobs (2s default). A
fleet-wide stream would be new API surface — post-MVP RFC territory.

## D-027 · 2026-07-17 · IBM adapter: local-mode tests, in-memory idempotency

The token-gated IBM adapter reuses qiskit-ibm-runtime's local mode so its
entire SamplerV2 path is tested offline with fake backends; only the network
needs the nightly token-gated probe. Known MVP limits (documented in code):
the idempotency key→job map is in-memory (an adapter restart may resubmit —
duplicates stay visible in usage), and `GetDeviceState` snapshot ids hash
the live vendor metrics. Compose keeps it dormant behind the `ibm` profile +
`TANGLE_ADAPTERS_EXTRA`, so the default stack provably never dials it.

## D-030 · 2026-07-18 · Operator: lean controller-runtime, namespace-as-tenant

M8 is a hand-rolled controller-runtime operator in a separate Go module
(`operator/`, `replace`-linked) — kubebuilder's CLI scaffold and
controller-gen would be the only consumers of their own boilerplate, so the
CRD manifest and deepcopy methods are written by hand (the types are tiny).
The CRD reuses the spec document's own GVK (`tangle.dev/v1alpha1
QuantumJob`) — the document was already shaped like a Kubernetes resource —
with the CR spec passed verbatim and validated by rabi's admission (the CRD
schema is deliberately permissive; one validator, not two). Tenant = the CR
namespace, overridable via the `tangle.dev/tenant` annotation. A finalizer
cancels the control-plane job on CR deletion. Crash-safety between
SubmitJob and the status write uses adoption: before submitting, the
reconciler searches the tenant's jobs for one whose document name matches
the CR (SubmitJob has no idempotency key in the v0.1 API — worth an
upstream RFC). Status resyncs every 2s while non-terminal (T8's <5s lag).

## D-028 · 2026-07-17 · Project renamed to Rabi; spec-derived identifiers unchanged

The project is now **Rabi** (after the Rabi oscillation), hosted at
github.com/rabi-project/rabi. Initially only branding was renamed; D-029
extended the rename to all project-owned identifiers on explicit request.

## D-029 · 2026-07-17 · Full rename of project-owned identifiers — plan overrides noted

On Edward's request, every identifier this project owns now says rabi:
Go module `github.com/rabi-project/rabi` (resolvable; generated code
regenerated), control-plane binary `tangled` → `rabi` (overriding the
mvp-build-plan §2 naming — the plan author asked for the rename), env vars
`TANGLE_*` → `RABI_*`, compose project/service/db `tangle` → `rabi`, Python
packages `rabi_aer`/`rabi_ibm`/`rabi-bench`, the LISTEN/NOTIFY channel, and
the sim delay parameter `rabi.sim/delay-ms`.

What deliberately keeps the tangle name, because the vendored spec defines
it and the spec is law: the `spec/` tree itself, proto packages
`tangle.adapter.v1alpha1`/`tangle.api.v1alpha1` (and therefore the generated
`gen/go/tangle/...` and Python `src/tangle/...` stub paths), the QuantumJob
`apiVersion: tangle.dev/v1alpha1`, the schema `$id`, and quotations from the
spec in docs. The committed migration `00001` still creates `tangle_info`
(rewriting applied migrations breaks existing databases; the table is an
inert marker). Historical decision entries keep their original wording.
