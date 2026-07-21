# Decisions

Running log of choices not covered by `mvp-build-plan.md`. Boring options,
recorded so they stay arguable. Format: ID · date · decision · why.

## D-001 · 2026-07-17 · Spec vendored by copy at `spec/`

The `rabi-spec` repository is copied (not submoduled) into `spec/` and
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
**Flagged as a spec question:** `rabi-spec/CONTRIBUTING.md` claims
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

## D-031 · 2026-07-19 · Spec v0.2 merged (RFCs 0001–0003); implementation deferred to phase1 M5

Edward, as spec author, merged spec v0.2 into the vendored copy via
format-patch: RFC-0001 resolves D-016a (first-class `technology` +
`cloud_queue`; vendor_extensions fallback legal until v0.3 — the current
adapters/scheduler intentionally keep using it), RFC-0002 ratifies D-016b
(best-value floors, optional `aggregate`), RFC-0003 adds
`scheduling.onConflict` (motivated by the benchmark's deadline trade-off).
Admission accepts the new fields today (schema-merged, tested); their
scheduler/adapter semantics land in phase1 M5 per the patch's own scoping.
Note: the upstream `rabi-spec` folder is now behind this vendored copy.

## D-032 · 2026-07-19 · P1-M0 completion state — PLAN QUESTION on the @v0.1.0 install criterion

Most of P1-M0 landed via Edward's migration patch before this plan started
(transfer, module path, badges, CI green from fresh clone, old-URL
redirects). Completed here: phase1 plan committed, original brand assets
under docs/brand/ (Rabi-oscillation mark), README logo, tag v0.2.0.
**Plan question:** the acceptance line "`go install …/cmd/qctl@v0.1.0`
works" is unsatisfiable post-migration — v0.1.0's go.mod permanently
declares the pre-org module path (Go tags are immutable snapshots).
Boring resolution: v0.2.0 is the first tag installable under
`github.com/rabi-project/rabi`; v0.1.0 remains the MVP-content tag.
**Blocked on org admin (Edward):** (a) branch protection returns 403 —
private repo on a free org plan; either make the repo public (consistent
with the open-source mission — his call, outward-facing) or upgrade the
plan; (b) org-level IBM_TOKEN secret needs `admin:org` scope
(`gh auth refresh -h github.com -s admin:org`) or the org settings UI.

## D-033 · 2026-07-19 · P1-M1 — SPEC QUESTION: admin surface is implementation-defined

Token lifecycle (create/rotate/revoke) and WhoAmI live in a new
`rabi.admin.v1alpha1.AdminService` under `proto/` — outside the vendored
spec, gRPC-only (no REST mapping; qctl is the client). The spec's client
API covers jobs/targets/usage and its own header calls tenancy
administration "RFC territory", which v0.2 did not claim. **SPEC
QUESTION:** should token/tenancy administration be standardized in a v0.3
RFC so qctl works across implementations? Until then this surface is
explicitly implementation-defined and excluded from conformance. Matrix
rationale: reads = viewer+, job mutations = member+, token inventory =
operator+, token lifecycle = admin.

## D-034 · 2026-07-19 · P1-M1 — auth model boring choices

- Bootstrap token (`RABI_BOOTSTRAP_TOKEN`): an admin principal through the
  same interceptor path (no side door); for compose/dev and first-admin
  setup, documented to be rotated away. Replaces the deleted RABI_API_KEY.
- API tokens are `rabi_<id>_<secret>`; at rest only SHA-256(full token) —
  unsalted is fine because the secret is 32 random bytes, and determinism
  gives O(1) lookup by the cleartext id half.
- OIDC: group→role map defaults to rabi-admin/-operator/-member/-viewer,
  overridable via `RABI_OIDC_GROUP_ROLES`; unmatched users get
  `RABI_OIDC_DEFAULT_ROLE` (default viewer = read-only).
- Scoping rule: roles gate verbs, a token's project gates nouns. OIDC
  users stay project-unscoped until M2 memberships exist.
- Client env var is `RABI_TOKEN` (any bearer: API token, JWT, bootstrap).

## D-035 · 2026-07-19 · P1-M1 — audit + e2e mechanics

Audit inserts are best-effort: a deny stands even if the audit write
fails (logged loudly) — an audit outage must not take down the API.
`audit_log`/`api_tokens` are append-only by code discipline until the M3
DB-grant pattern covers them. The dex e2e drives the real
authorization-code flow against the mockCallback connector (headless —
it approves without a form and its identity carries groups=[authors]);
mockPassword was rejected because its identity has no groups, which
would leave role mapping untested.

## D-036 · 2026-07-19 · P1-M2 — tenancy boring choices

- The spec API speaks tenant strings (ListJobsRequest.tenant et al. — spec
  law), so the exact wire string is the project identity/PK; org and
  project name are DERIVED display fields (first "/" segment; bare strings
  get project "default"). "Org/project inheritance" = this derivation
  rule, table-tested; org-level entities/policies (org quotas, org roles)
  arrive when something consumes them, and org-scoped role bindings are
  the natural M2 follow-up once OIDC users get project memberships.
- Projects auto-create on first submission (Phase 0 accepted arbitrary
  tenants; strict mode is a future deployment flag). Archive is one-way
  and blocks new submissions only.
- Quota model: per-(project, unit) limits in native units; admission locks
  the quota rows and inserts the job IN THE SAME transaction, so
  concurrency serializes per project and the race criterion holds exactly.
  Committed = ledger usage + declared demand of non-terminal jobs
  (declared_cost() in SQL and declaredCosts() in Go must agree). Only
  gate-model shots are declarable today; other units meter post-hoc via
  the ledger and are quota-unlimited until declarable.
- Fair share: weighted deficit round-robin over each dispatch cycle's
  pending set (virtual time (assigned+1)/weight, lexical tie-break, FIFO
  within a project), reset per cycle. Golden: 3:1 → exact aaab cadence.

## D-037 · 2026-07-19 · P1-M3 — accounting boring choices

- Append-only is enforced by Postgres, not discipline: migration 00006
  creates a NOLOGIN role `rabi_app` with no UPDATE/DELETE/TRUNCATE on
  usage_ledger, audit_log, job_events, reconciliation_runs; store.Open
  migrates as the owner, then serves every connection under
  `SET ROLE rabi_app`. The §3 test issues UPDATE/DELETE/TRUNCATE through
  the serving pool and asserts "permission denied". Closes D-035's interim.
- Normalization is a PURE FUNCTION of (ledger, policy document): cost
  records are computed on export, never stored, so same ledger + same
  policy version is byte-equal by construction. Policy is a site YAML
  (version + currency + ordered rates, first match wins, optional target
  glob); unpriced units surface at rate 0 rather than vanishing.
  `qctl usage export --policy site.yaml` emits canonical CSV; the raw
  ledger crosses the wire via admin ExportLedger (viewer role, project
  scope enforced server-side).
- Reconciliation (Σ ledger == per-job status usage for SUCCEEDED jobs)
  runs inside `rabi` on a ticker — weekly by default, RABI_RECONCILE_EVERY
  for demos/CI — and appends to reconciliation_runs (itself append-only).

## D-038 · 2026-07-19 · P1-M4 — deploy boring choices

- The Helm chart has ZERO dependencies: Postgres is an optional in-chart
  StatefulSet (postgres.enabled=false + externalDatabaseUrl to bring your
  own) and the Aer adapter is a sidecar. A remote subchart would need
  internet at install time and break the air-gap rule.
- Upgrade gate: RABI_AUTO_MIGRATE=false (helm value autoMigrate) serves
  without migrating and FAILS FAST on a lagging schema; the documented
  path is migrate-once-then-roll.
- Air-gap proof: bundle = single-platform image archive + packaged chart +
  install script; every pullPolicy=Never, so any egress attempt is a hard
  failure, and the verify script asserts zero "Pulling" events after
  install. This kind+archive+Never construction is the portable equivalent
  of the plan's no-egress network namespace. Gotcha: with Docker's
  containerd image store, `docker save` of a pulled multi-arch tag carries
  an index with absent foreign blobs — save with --platform.
- Backup = pg_dump PLUS pg_dumpall --roles-only: the rabi_app role is
  cluster-level, and without it a restored instance refuses to boot (SET
  ROLE fails) — the drill exercises exactly this. Restore order: postgres
  → roles → database → rabi (never boot rabi against an empty DB first).

## D-039 · 2026-07-19 · P1-M5 — spec v0.2 semantics implemented

- RFC-0002: floor evaluation aggregates (best default | median | worst) in
  the scheduler; deterministic lower-middle median; rejection strings name
  aggregate + winning value + floor (normative format). Golden suite
  unchanged — default path byte-identical.
- RFC-0001: dispatcher reads TargetInfo.technology /
  Capabilities.cloud_queue first-class; vendor_extensions fallback stays
  until spec v0.3 (then delete + reserve keys). Both in-tree adapters set
  the fields; conformance cat-1 fails empty technology, warns on
  non-registry strings (spec §2a registry mirrored in conformance).
- RFC-0003: onConflict resolved in the dispatcher when floors are the
  binding constraint (relaxed pass feasible + violations non-empty).
  Decision horizon = deadline − predicted wait, zero execution estimate —
  implementation-defined but recorded in the audit (horizonModel), per the
  RFC's unresolved-question resolution. prefer-deadline binds at the
  horizon with floorsRelaxed + per-floor limit/actual/aggregate;
  reject FAILs with CAPABILITY_MISMATCH retriable=true and condition
  UnsatisfiableBeforeDeadline; prefer-quality keeps v0 behavior and adds
  condition DeadlineExceededWaitingForQuality once the deadline passes.
- FSM: PENDING→FAILED edge added — required by RFC-0003 reject. SPEC
  QUESTION: spec/spec/overview.md §3's diagram still lacks this edge;
  needs a v0.3 editorial pass.

## D-040 · 2026-07-19 · P1-M6 — session boring choices

- The control-plane session id (uuid, `status.sessionId` on the opener) is
  what `spec.session.join` names; the adapter's own session id rides along
  on SubmitTask. Sessions live in a `sessions` table (closure recorded,
  never deleted); a per-cycle sweep closes expired records.
- Affinity = `requireTargets` pinning inside the normal pipeline (no
  special-case scheduler path). Joiners of a missing/closed/expired/
  foreign-tenant session FAIL with SESSION_LOST + condition SessionLost —
  never a silent reschedule; the tenant check is what makes session
  accounting attribute only to the session's project.
- Opener flow: session opens AFTER bind (on the actual bound target),
  before task submission; failure to open fails the job — running
  sessionless would break the affinity contract for followers.
- Aer adapter: wall-clock session expiry (the sim clock accelerates
  calibration drift, not session lifetimes); dead-session submissions
  fail the TASK with categorized SESSION_LOST (a precheck_error hook in
  the engine), never a bare gRPC abort. Conformance cat 8 covers declared
  adapters: open→submit→close→SESSION_LOST + unknown-session loss.

## D-041 · 2026-07-19 · P1-M7 — conformance harness extraction

- The suite's T abstraction gets a Recorder implementation, so the same
  category code runs under `go test` and the new `rabi-conformance run
  --target <addr>` CLI. Reports: canonical JSON (the signed document,
  ed25519 — --key PKCS#8 PEM, else ephemeral key + emitted pubkey),
  markdown rendering, harness+spec versions, capability summary, and
  free-form notes that never soften failures.
- Self-test: adaptertest.Fake became honest-by-default (format/shots/
  session checks, provenance methodology) plus broken-fixture knobs
  (IgnoreMaxShots → cat1, BrokenSessions → cat8); the self-test asserts
  each knob fails exactly its category and nothing else.
- Extraction immediately caught three real conformance bugs in our IBM
  adapter (fake-backend mode): parse failures surfaced as VENDOR_ERROR
  (openqasm3 raises QASM3ParsingError, not QASM3ImporterError — _parse
  now maps all parse failures to INVALID_PROGRAM), max_shots declared but
  unenforced, and cancellation unexercisable because tasks ran instantly
  and concurrently. Fix for the last: a per-backend single-worker queue
  (honest emulation of IBM's per-backend queueing) + rabi.sim/delay-ms
  honored as a hold-in-QUEUED simulator hint.
- IBM certification in CI runs `--fake` (FakeManilaV2, tokenless,
  deterministic) with an explicit report note; live certification remains
  nightly/token-gated. ci.yml publishes both reports as artifacts.

## D-042 · 2026-07-19 · P1-M8 — QRMI driver boring choices

- Language: QRMI's Python bindings (qrmi>=0.20 on PyPI) — matches the uv
  toolchain of the rest of the adapter fleet; Rust/C rejected for
  toolchain sprawl. The live dependency is an optional extra: cassette
  mode imports none of it.
- The adapter owns what QRMI does not define: idempotency keys, task FSM,
  error taxonomy (local QASM validation → INVALID_PROGRAM before any
  vendor call), per-resource single-worker queue, usage records (shots +
  tasks; qpu-seconds when live metadata provides it later).
- Technology/cloud mapping per QRMI ResourceType (IBM* → superconducting/
  cloud, PasqalCloud → neutral-atom/cloud, PasqalLocal → on-prem, ...);
  calibration provenance from the QRMI Target document mapped into Metric
  fields with methodology "qrmi-relayed (<upstream tag>)" and snapshot
  source "qrmi:<type>/<id>".
- "Cassette-backed in CI" = CassetteQrmi, a deterministic QRMI-shaped
  resource behind the same interface as LiveQrmi (reports carry an
  explicit note) — same precedent as ibm --fake. First live recording can
  replace the synthetic fixture later without touching the adapter layer.
- Nightly qrmi-live job exists but SKIPS until org secrets
  QRMI_RESOURCE/QRMI_ENV_FILE are configured (needs Edward) — provably
  dormant like ibm-live. The "nightly live ≥95% over 7 days" criterion is
  an operational gate that starts counting when credentials land.

## D-043 · 2026-07-19 · P1-M9 — QDMI driver boring choices

- One adapter chassis: QrmiAdapterService is parameterized (VENDOR,
  MAX_SHOTS, SNAPSHOT_PREFIX, _extensions hook) and any backend with
  describe/start/status/result/stop rides it. QdmiAdapterService is a
  ~15-line subclass; all FSM/idempotency/taxonomy/queue behavior — and
  its conformance record — is shared.
- QDMI's contract is a C ABI, so the binding is ctypes over a device
  shared library, and CI certifies through a COMPILED mock device
  (mock/mock_device.c) — the dlopen/marshalling path is the real thing,
  the device is synthetic (report notes it). The bound symbol table is
  QDMI 1.0-shaped and centralized in device.py SYMBOLS: real sites vary
  by QDMI version, and the site recipe (docs/qdmi-site-recipe.md) makes
  ABI drift a one-file fix + re-certification. Missing symbols fail fast
  at load with the exact list.
- QDMI devices are site-local: cloud_queue=false; technology defaults to
  superconducting/cz with the recipe instructing per-device correction.

## D-044 · 2026-07-19 · P1-M10 — second cloud + GPU simulator boring choices

- Second EU cloud = IQM Resonance (Pasqal Cloud is already reachable via
  the QRMI driver's resource types; a dedicated Pasqal driver would
  duplicate that path). Neither vendor has credentials in-repo, so the
  cassette/live split applies: CassetteIqm certifies in CI, LiveIqm binds
  qiskit-iqm — which must be installed in the SERVING environment (its
  qiskit pin conflicts with the fleet's qiskit 2.x lock; the import is
  lazy, so CI never needs it).
- GPU-backed simulator targets = the EXISTING Aer adapter with a
  sim_device: GPU config knob (cuQuantum/cuStateVec via the
  qiskit-aer-gpu build). CUDA-Q-the-framework was rejected: it cannot
  ingest the spec's wire format (no OpenQASM 3 import), so it would need
  a private translation layer — cuStateVec through Aer serves the actual
  goal (GPU-backed simulator Targets) with zero new adapter code. The
  gpu-sim target declares technology "simulator" (registry value). CI
  runs the CPU-device twin config; real GPU execution needs the NVIDIA
  container runtime (compose --profile gpu + RABI_GPU_CONFIG=gpu-sim.yaml)
  and is exercised at fleet-0/site.
- Mixed-fleet e2e (hack/e2e-mixed-fleet.sh, deploy workflow): 3 replay +
  IQM cassette + gpu-class sim; placements verified per segment, with the
  RFC-0001 technology filter steering the simulator-only job. It caught a
  real dispatcher bug: fast tasks can finish before a RUNNING observation
  and the linear FSM rejected SUBMITTED→SUCCEEDED — the dispatcher now
  passes through RUNNING (event history stays truthful).
- New-adapter env gotcha: every adapter dir needs .python-version=3.13
  (qiskit-aer lacks 3.14 wheels) — added to ibm/qrmi/qdmi/iqm.

## D-045 · 2026-07-19 · P1-M11 — console boring choices

- The console is dependency-free vanilla JS/CSS (no framework, no build
  step, no npm at build or runtime) embedded via go:embed and served at
  /console/ from the single binary — nothing to vendor for air-gap, and
  the whole "SPA" is three static files. The viewer's token lives in tab
  sessionStorage and is sent only to this server.
- Zero-write is enforced twice: the page only ever issues GET, and the
  Playwright suite intercepts every request and fails the run on any
  non-GET/HEAD to the server origin (the plan's proxy assertion).
- Provenance is asserted AS UI: the e2e requires the rendered calibration
  age ("N min ago") and a non-empty per-metric methodology column, not
  just fields in API payloads. The placement-audit page renders the
  decision facts (policy, snapshot, predictions, onConflict/horizon,
  floorsRelaxed details) plus the full rejected-target list; the e2e
  seeds a denyTargets job so a real rejection is always on screen.
- Playwright itself is fetched at TEST time via npx (test tooling is
  outside the air-gap rule, which governs runtime).

## D-046 · 2026-07-19 · P1-M12 — pilot package boring choices

- Probes: Bell pairs (probe_results append-only; fidelity = 1−TVD vs
  ideal; |predicted−measured| feeds the pilot estimator SLO), pinned per
  target via requireTargets under system/probes, scheduled in-binary
  (RABI_PROBE_EVERY, default 15m). /metrics is hand-rolled Prometheus
  text (zero new deps), tenant-blind aggregates only, unauthenticated
  like /healthz (see security checklist).
- Grafana dashboards are provisioned files (deploy/observability) behind
  a compose profile; anonymous-viewer Grafana for the demo, sites front
  their own.
- Release CI: govulncheck + trivy fs scan gate (no HIGH/CRITICAL,
  unfixed ignored), then linux-amd64 binaries + fresh conformance
  reports for all five adapters attached to the GitHub release.
- fleet-0 = compose-on-VM via cloud-init + idempotent provision.sh with
  a systemd unit (the plan's named boring option). Install guide targets
  ≤60 min; the stranger-test is a human event to run with a pilot.
- Dex tamper flake root cause worth remembering: the last base64url char
  of an RS256 signature carries 2 significant bits — tamper tests must
  flip a fully significant character.

## D-047 · 2026-07-19 · spec repo renamed: tangle-spec → rabi-spec

Edward (spec owner) directed the rename of all "tangle-spec" /
"Tangle spec(ification)" references inside this repo to rabi-spec /
"Rabi spec(ification)" — including the vendored spec's own README and the
historical Phase-0 plan text, plus three mentions inside earlier decision
entries (recorded here so the log's history is explicit about having been
renamed, not rewritten).

**Unchanged, deliberately:** the wire identifiers — proto packages
`tangle.adapter.v1alpha1` / `tangle.api.v1alpha1`, `apiVersion:
tangle.dev/v1alpha1`, the schema `$id`s, and the adapters' generated
`src/tangle/...` stub paths derived from them. Renaming those breaks
every existing client, adapter, and stored document; that is a breaking
spec release (v0.3+ RFC territory, alongside the already-parked
extension-key removals), not a documentation rename. D-028's boundary
still stands, with the spec now named rabi-spec.

## D-048 · 2026-07-19 · P1-M8 live QRMI needs Direct Access entitlement

First live QRMI run (against a free IBM Cloud Open-plan Qiskit Runtime
instance) 404s at `QuantumResource.acquire()`. Root cause (from the qrmi
0.20 binary's request paths, `/core-fast/api/v1/...`): QRMI's
`IBMQiskitRuntimeService` resource type targets IBM **Direct Access**, a
premium dedicated-access product — NOT the standard Qiskit Runtime API.
The Open plan grants the latter (which our *ibm* adapter uses via
qiskit-ibm-runtime, and `ibm-live` passes), not the former.

Consequences, all boring: the QRMI adapter stays fully conformance-
certified via the cassette (CI); live QRMI certification is parked on a
Direct Access entitlement, same shape as every other vendor driver's
"live needs real credentials." The nightly treats an acquire-time 404 as
an explicit skip-with-warning, not a red failure, so it doesn't nag about
an entitlement we don't have. Fixing the debugging journey's real
findings stays in the code: startup race (poll+log), per-resource env
prefixing (`<id>_QRMI_*`), unversioned endpoint base. When a Direct
Access instance exists, set QRMI_RESOURCE=<da-backend>=IBMQuantumSystem
(or keep IBMQiskitRuntimeService if the account is Direct-Access-enabled)
and the same job certifies live.

## D-049 · 2026-07-21 · P2-M1 chaos & invariants harness — boring choices

First Phase-2 milestone: the chaos & invariants harness (E4). Five load-bearing
choices, all deliberately boring:

**Invariants operate on the store alone.** `chaos.CheckAll(ctx, store, accepted,
declared)` runs the five invariants (no job lost · no duplicate execution ·
terminal immutable · usage within caps · audit gapless) by reading Postgres —
nothing about the fault-injection mechanism leaks in. This is why the *same*
checks run unchanged in the CI component suite and in the live `--fleet0`
game-day: the invariants don't know or care how the state got there.

**The harness must prove it can fail.** `TestSelfTest_InvariantsCatchPlanted­Violations`
plants an over-cap ledger row, a post-terminal event, an illegal transition, and
a missing job, and asserts each is caught. A chaos suite whose invariants can't
go red is theater; the self-test runs first in CI, before the eight scenarios.

**The Postgres-restart scenario needs a pinned host port.** testcontainers
remaps the host port when a container is stopped and started, which stranded the
shared pool and cascaded failures into every subsequent test (301 s hang → all-
red). Fix: pin Postgres to a fixed host port in `TestMain` so a restart is
transparent to the pool. The scenario now runs in ~5 s. (This is a test-harness
fact, not a product fact — production uses a real restart, not testcontainers.)

**The `--fleet0` game-day is a read-only invariant sweep, not fault injection.**
`rabi-chaos sweep` lists recent jobs and asserts the five invariants hold on the
system's *actual current state*. Against production that is the honest, safe
first game-day: it verifies the live ledger and event chains without injecting a
fault or mutating a job. Destructive fault-injection drills against fleet-0
remain scheduled, supervised, and gated behind `--i-mean-it` — and, per the
"Fleet-0 is production" constraint, go through the rehearsed upgrade path (M3),
never an ad-hoc SSH mutation. The eight destructive scenarios are exercised in
CI against the disposable compose/testcontainer stack, which is their home.

**Game-day annotations live in Postgres (`game_days` table), append-only.** No
new datastore (the no-new-infrastructure constraint). Each drill writes one
finalized row — both timestamps and the result in a single INSERT, so a drill is
never rewritten after it ran, matching every other measurement table. `Store.
LastGameDay` is the read the M7 status page renders for "last game-day date and
result."

**Forward dependency, recorded honestly:** M1's accept criterion has two clauses
— "one supervised `--fleet0` game-day executed with invariants green" AND "the
drill visible as an annotation on the status page." The second clause cannot be
satisfied before the status page exists (M7). So M1 ships the full capability
(driver, `--i-mean-it` guard, annotation storage, verified end to end) and the
weekly-green CI scenarios now; the *supervised production game-day execution and
its status-page rendering* complete in the M7 window. This split is inherent to
the plan's own cross-references, not a shortcut.


## D-050 · 2026-07-21 · P2-M2 load & soak harness — and two real bugs it caught

Second Phase-2 milestone: the load & soak harness (E4). Boring choices, plus two
genuine fixes the harness surfaced — which is the point of building it.

**One in-process stack, real code paths, synthetic fleet.** `loadtest.NewStack`
boots the real store, registry, dispatcher, and API server against a caller-
provided Postgres, with one `adaptertest.Fake` presenting N targets. The storm
seeds a backlog and measures scheduler-cycle p99 (read from the dispatcher) and
API read/write p99 (client-side, over real gRPC) while it drains; the soak
churns jobs and watches memory, goroutines, and stuck jobs. Same code production
runs — a load test that stubbed the scheduler would measure nothing.

**Scheduler cycle timing lives in the dispatcher, zero-dependency.** A bounded
ring buffer (`cycleRing`) records each cycle's duration; `Dispatcher.CycleP99`
exposes it. A ring, not a slice, so p99 stays computable over a 72h run without
unbounded sample growth. No Prometheus client was added — the metrics emitter
stays hand-rolled per the air-gap constraint.

**BUG 1 (throughput): the dispatcher drained one batch per wakeup.** `Run` called
`cycle` then always waited for a notify or the 5s tick, so a large backlog with
no new arrivals drained at `claimBatch` (32) per 5s — a 10k backlog would take
~26 min. Fix: `cycle` now reports how many jobs it *bound*, and `Run` re-cycles
immediately when it filled a batch AND made progress. Requiring progress avoids a
hot loop on a batch of currently-infeasible jobs. Draining 400 jobs went 123s →
2.9s. This is a real scheduler-throughput improvement, not a test artifact.

**BUG 2 (leak): one gRPC stream goroutine leaked per completed job.** `follow`
opened `WatchTask` on the long-lived dispatcher context and returned on the
terminal status without cancelling it, so every finished job left a client-side
stream goroutine alive until process exit — invisible at low volume, unbounded
over a soak. The smoke soak showed goroutines climbing 1:1 with jobs (peak 1221,
never released). Fix: each `WatchTask` gets a child context cancelled when the
task finishes or we reconnect. After the fix, goroutines peak at 24 and return to
the 20-goroutine baseline; heap growth dropped 80% → 17%. The soak harness exists
precisely to catch this class of bug, and it did on its first real run.

**Thresholds are the test-plan's, asserted in code.** `MaxSchedulerCycleP99` 2s,
`MaxAPIReadP99` 300ms, `MaxAPIWriteP99` 1s (`storm.go`); post-warmup heap growth,
a quiescent-baseline goroutine bound, and zero stuck jobs (`soak.go`). The
goroutine baseline is captured quiescent *before* load — the honest reference: a
leak makes the post-drain count exceed it regardless of how many jobs ran. Soak
"RSS growth < 5%/24h" is asserted in accelerated form: a genuine leak balloons
the GC'd heap, so a modest growth tripwire catches it without flapping on noise.

**The in-process fake needed a retention cap.** The first full-scale CI soak
(36k jobs) tripped the heap tripwire at 244% — not a product leak (goroutines
were flat) but the `adaptertest.Fake` retaining every task record in a map. A
real adapter is a separate process, so its backend history never counts against
the control plane; the in-process fake does. Fix: the fake now evicts oldest
*terminal* tasks past a cap (8192, far above any functional test), so its memory
plateaus and the soak's whole-process heap reflects the control plane. Growth at
18k jobs fell 244% → 32%. The heap baseline is also taken later (35% warmup) so
the working set — fake cap included — has stabilized before it's measured.

**Scheduled + gated.** Storm weekly, soak monthly (`load.yml`), reports published
as artifacts; a PR touching the harness runs a fast smoke. A breached threshold
exits non-zero, and `release.yml`'s `perf-gate` runs storm+soak so a performance
regression blocks the release tag (test-plan §4 accept). The `rabi-load` CLI
drives the fleet-0-sized variant (1,000 jobs) against a *throwaway* DB — never
production, since the harness runs its own dispatcher and would double-schedule.

## D-051 · 2026-07-21 · P2-M3 upgrade & migration hardening — and a resume bug it caught

Third Phase-2 milestone: upgrade & migration hardening (E4). Boring choices, plus
a real correctness bug the upgrade rehearsal surfaced.

**Goldens are reconstructed from immutable migrations, not committed dumps.**
`store.OpenAt(dsn, version)` (which already existed, commented "for upgrade
tests") migrates a fresh database to exactly the schema a released tag shipped —
v0.1.0/v0.2.0 = migration 3, v0.4.x = 8. The golden's *data* is a committed seed
per tag (`internal/upgrade/testdata/`); the schema comes from OpenAt. Goose
migrations are immutable by convention, so OpenAt(8) reproduces v0.4.2's schema
exactly — cleaner than committing multi-MB dumps that would drift. The matrix
restores each golden, migrates forward, and asserts jobs/events/usage survive.

**Rollback is additive-compatibility, not a goose-down chain.** Only 3 of 9
migrations ship a `Down`, and re-migrating forward is the tested direction. The
real rollback guarantee for a single-node deployment is that the schema stays a
strict superset of the last release, so the N-1 binary serves the N schema
unchanged — roll the image tag back, leave the schema. `TestRollbackSafety`
asserts every v8 table.column still exists at HEAD (78 → 87, additive). The
newest migration (00009) did get a `Down` for hygiene, but that is not the
routine rollback path.

**BUG (correctness, load-bearing): a control-plane restart stranded in-flight
jobs.** The upgrade rehearsal — roll the plane while jobs execute on a persistent
adapter — found that only 14 of 60 jobs completed after the roll; 46 that were
SUBMITTED/RUNNING at the cut hung forever. Root cause: `resume()` re-runs
`execute()`, which unconditionally transitions the job to SUBMITTED, and
`applyTaskStatus` transitions to RUNNING — both illegal for a job already at or
past that phase (the FSM is linear with no self-edges). The illegal transition
was mis-read as "cancelled concurrently", so the code cancelled the adapter task
and abandoned the job. This fires on EVERY restart with in-flight RUNNING work —
a direct violation of the zero-lost-jobs SLO, invisible until something forced a
restart under load. Fix: on a failed forward transition, distinguish a genuine
terminal conclusion (stop, cancel the task) from resume re-attaching to an
already-advanced job (adopt the live phase, keep following). After the fix: 60/60
complete, API unavailability ~21 ms. The SUCCEEDED path was already tolerant
(best-effort RUNNING first), so only the SUBMITTED and RUNNING transitions needed
the guard.

**Fleet-0 adopts the rehearsed path.** `docs/fleet0/upgrade.md` is the runbook:
pin the tag, pull, recreate `rabi` with `--no-deps` (Postgres and the adapter
stay up so in-flight tasks survive and `resume()` re-attaches), health-gate on
`/healthz`, roll back by reverting the tag (additive schema). CI runs the whole
suite weekly (`upgrade.yml`) and on any PR touching migrations, the store, the
dispatcher, or the goldens.

## D-052 · 2026-07-21 · P2-M4 security wave — fuzzing, supply chain, secret hygiene, mutation

Fourth Phase-2 milestone: the security wave (E4). Findings and boring choices.

**Fuzzing every untrusted-input parser.** Native Go fuzz harnesses for the four
parsers that touch attacker-controlled bytes: OpenQASM ingestion
(`FuzzProfileQASM`), admission (`FuzzAdmit`, JSON → map → `Admit`), adapter
result decoding (`FuzzPayloadFor`, `FuzzResultDecode`), and policy YAML
(`FuzzParsePolicy`). Seed corpora are committed in the `f.Add` calls. CI runs
each for ≥ 1,000,000 executions (`-fuzztime=1000000x`) weekly and on any parser
PR. Result: 18M+ executions against `Admit` alone, **zero crashers** — the
flagged unchecked type assertions are in fact guarded by JSON-Schema validation
running first. Good news, now enforced.

**BUG (secret hygiene): the DB password was logged at startup.** `cmd/rabi`
logged `"store ready", "url", dbURL` — and the DSN embeds the password
(`postgres://rabi:PASSWORD@…`). Fix: `store.RedactDSN` masks the password (URL
and libpq keyword forms) and the startup log uses it. A new log-scan test
(`TestNoSecretsInLogs`) drives the authenticator with sentinel bootstrap/bearer
values and asserts neither reaches the log stream, then proves the scanner has
teeth by planting one. Tokens were already hashed-at-rest and compared in
constant time; this closes the one place a credential leaked in cleartext.

**Supply chain on releases.** `release.yml` now generates an SPDX SBOM (syft via
anchore/sbom-action) and a signed build-provenance attestation
(`actions/attest-build-provenance`, keyless OIDC) for the binaries and SBOM,
published alongside `SHA256SUMS`. The operator tools (`rabi-chaos`, `rabi-load`)
join the shipped binaries. The CVE gate (govulncheck + trivy) stays blocking.

**Mutation testing without gremlins.** gremlins 0.5.0 does not run under this
module (its module-wide coverage baseline pulls in the testcontainer suites and
every mutant reports "timed out"). Rather than depend on a stale tool, the
mutation harness is a small curated script (`hack/mutation-test.sh`): each mutant
is one semantic edit to a load-bearing line in the FSM (`phase.go`) or the
scheduler filter (`filter.go`) — negate a terminal check, invert a transition,
flip a `>` to `<`. A mutant is KILLED when the pure `internal/job` +
`internal/scheduler` tests fail with it applied. Both packages are Docker-free,
so it runs anywhere. Result: 9/9 mutants killed, 100% efficacy (floor 65%),
proving the suite catches planted logic errors. Runs quarterly and on any
scheduler/FSM PR. Curated mutants are the honest bootstrap; the list grows as
the code does.

**SECURITY.md** publishes the disclosure process (GitHub private advisories),
response-time commitments (ack ≤ 3 business days, triage ≤ 7, critical fix ≤ 30
days), and coordinated-disclosure terms.
