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
quality degradation) and SUBMITTED→FAILED (adapters can reject at submission
with e.g. INVALID_PROGRAM; the diagram only draws FAILED from RUNNING).
Worth an editorial clarification in the spec's lifecycle section.

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
