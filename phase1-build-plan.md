# Rabi Phase 1 — Agent-Executable Build Plan (Pilot-Grade Alpha)
### v1.0 · July 2026 · Hand to the agent with: the Rabi repo, `spec/` at v0.2 (RFCs 0001–0003 merged), and `test-and-verification-plan.md`.

---

## Kickoff prompt (paste to the agent)

> You are building Phase 1 of Rabi: everything a single friendly site needs to run the
> control plane in production for real users. Read `phase1-build-plan.md` completely before
> writing code. The vendored `spec/` (v0.2) is the source of truth for all wire contracts —
> RFCs 0001–0003 are merged and normative; implement exactly what they say, and if you find a
> gap, flag it as a SPEC QUESTION in `docs/decisions.md` — never decide spec yourself. Work
> strictly milestone by milestone (M0→M12); do not start milestone N+1 until milestone N's
> acceptance criteria pass, including its suites from `test-and-verification-plan.md` §3.
> Continue the decisions-log discipline (D-031 onward). Keep commits small. When a decision
> is not covered here, choose the boring option and record it.

## 1. Mission

Phase 0 proved the three MVP claims (E1/E2/E3). Phase 1's single goal: **a design-partner
site can deploy Rabi behind their SSO, give it to a real user group, meter and control their
usage, and trust it with their credentials — installed from a Helm chart or an air-gapped
bundle, with every adapter certified by the public conformance harness.** The Phase 1 exit
gate (a running pilot) is a human/business event; this plan covers everything code must do
to make that event possible.

Priority logic is **pilot-blocking first**: a site can pilot without a QDMI driver, but not
without login, tenancy, install, and backups. Drivers come after the operational core.

## 2. Hard constraints (Phase 0 constraints remain; these are additions)

- **Spec v0.2 is law.** RFC-0001 (`technology`/`cloud_queue` fields), RFC-0002 (quality-floor
  aggregate semantics), RFC-0003 (`scheduling.onConflict`) are merged before this plan starts.
  Regenerate protos from spec; never hand-edit generated code.
- **AuthN:** OIDC only, via `coreos/go-oidc` + standard `oauth2`. No password storage, ever.
  Per-project API tokens (hashed at rest) for non-interactive clients. The static
  `RABI_API_KEY` path is deleted in M1 (compose demo gets a dev-mode OIDC stub or bootstrap token).
- **No new infrastructure.** Postgres remains the only datastore. No Redis, no queue broker,
  no second database for the console. Cron-like work (probes, reconciliation) runs inside
  `rabi` via the existing Postgres work-queue.
- **Console is read-only and stateless:** a static SPA embedded with `go:embed`, consuming the
  public REST API with the viewer's own token; zero server-side session state; zero write endpoints.
- **Migrations:** goose, forward-only, with golden-database upgrade tests from every prior tag.
- **Vendor drivers in CI run on recorded cassettes** (deterministic, tokenless); live runs are
  nightly and token-gated. No secrets in the repo or in test fixtures.
- **Every new adapter must pass the conformance harness for its declared capabilities before
  its milestone closes.** No exceptions — we are never our own exception.
- **Air-gap rule:** nothing added in Phase 1 may require internet at runtime; anything needing
  a download must be included in the offline bundle.

## 3. Milestones (≈26 weeks; bands are planning guides, acceptance criteria are the law)

**M0 — Org migration & rename (week 1).** Repo transferred to the `rabi-project` org; module
path → `github.com/rabi-project/rabi` (and operator module); badges, links, and docs updated;
logo assets committed under `docs/brand/`; org-level CI secrets configured (IBM token for
nightly); branch protection + required checks on; `v0.1.0` tagged.
*Accept:* fresh clone from the org URL builds and passes CI; old URLs redirect; `go install
github.com/rabi-project/rabi/cmd/qctl@v0.1.0` works.

**M1 — AuthN/Z v1 (weeks 1–4).** OIDC login (any spec-compliant IdP; tested against dex in
CI); per-project API tokens (create/rotate/revoke via `qctl`); roles: `admin`, `operator`,
`member`, `viewer`; audit log (append-only table) for every denied call and every admin action.
*Accept:* authz matrix test — every endpoint × every role, auto-enumerated from proto, 100%
of cells asserted (test-plan §3); dex-backed e2e login in CI; deleted static-key path;
token hashes only at rest (repo-wide grep test).

**M2 — Tenancy v1 (weeks 3–7).** Org → project hierarchy replacing the tenant string (data
migration included); per-project quotas in native units; fair-share weights consumed by the
scheduler's priority ordering; project lifecycle (create/archive) via `qctl` + API.
*Accept:* migration test: a Phase-0 database upgrades with zero data loss and tenant strings
mapped to orgs/projects; quota race test — 100 concurrent submissions against a nearly-exhausted
quota admit exactly the affordable number; fair-share golden: two projects at 3:1 weights
under contention bind in ~3:1 ratio over a seeded run.

**M3 — Accounting v1 (weeks 6–8).** Immutable usage ledger (append-only enforced by DB
grants — no UPDATE/DELETE privilege exists); site-configurable normalization policy (native
units × rates → cost records, with policy version stamped); tenant usage reports via API/CLI;
weekly reconciliation job (Σ task usage == ledger).
*Accept:* append-only proven by a test that attempts UPDATE/DELETE and fails at the DB layer;
reconciliation on seeded workload = zero mismatches; normalization is replayable (same ledger
+ same policy version → identical cost records, byte-equal CSV export).

**M4 — Deploy v1 (weeks 8–10).** Helm chart (rabi + Postgres optional subchart + adapters as
sidecars/deployments); air-gapped bundle (images + chart + install script, no-egress
verified); backup/restore runbook with a scripted drill; upgrade path documented (goose
auto-migrate on boot, gated by a flag).
*Accept:* `helm install` on kind → smoke suite green; air-gap test installs inside a
no-egress network namespace; scripted backup→destroy→restore drill ends with reconciliation
clean and watch streams resuming; N-1→N upgrade test green from `v0.1.0` golden DB.

**M5 — Spec v0.2 alignment (weeks 10–12).** Implement the three RFCs end-to-end:
`technology`/`cloud_queue` first-class (registry fallback to `vendor_extensions` removed
after a deprecation window per RFC-0001); quality-floor `aggregate` option (RFC-0002);
`scheduling.onConflict` modes with placement-audit recording (RFC-0003).
*Accept:* schema/admission tests extended per RFC (each mode/value gets accept+reject cases);
DES tests for all three `onConflict` modes on the M4-replay fleet — `prefer-quality`
reproduces v0 behavior byte-identically (golden), `prefer-deadline` records explicit floor
violations, `reject` fails with the RFC's condition; goldens updated under `golden-change` label.

**M6 — Sessions end-to-end (weeks 12–14).** Session objects in the control plane; scheduler
affinity honoring `session.join`/`maxDuration`; Aer adapter declares + implements the session
capability; expiry → `SESSION_LOST` semantics.
*Accept:* conformance category 8 passes against the Aer adapter; affinity test — a seeded
iterative loop's tasks land on one target 100%; expiry test produces `SESSION_LOST`, never a
silent reschedule; session accounting attributes usage to the session's project.

**M7 — Conformance harness extraction (weeks 14–16).** The in-tree conformance suite becomes
a standalone runnable (`rabi-conformance run --target <addr>`), producing a versioned, signed
JSON+markdown certification report; category selection follows declared capabilities;
docs for third-party driver authors.
*Accept:* harness runs against the Aer adapter from a clean checkout with one command and
produces the report artifact; intentionally-broken adapter fixtures fail the right categories
(harness self-test); CI publishes the Aer + IBM reports as build artifacts.

**M8 — QRMI driver (weeks 16–19).** Out-of-process adapter wrapping QRMI (language per QRMI's
C/Python/Rust bindings — agent chooses, records decision) exposing QRMI-managed resources
(IBM Direct Access, Qiskit Runtime, Pasqal Cloud) as Targets with calibration provenance
mapped into `Metric` fields.
*Accept:* conformance pass (cassette-backed in CI) for declared capabilities; nightly live
run green with real credentials; provenance completeness — every metric carries source,
measured-at, methodology (`"qrmi-relayed"` + upstream tag); usage records in the vendor's
native units.

**M9 — QDMI driver (weeks 19–21).** Same contract against QDMI's C interface (device
properties, job submission, calibration queries), certified the same way.
*Accept:* conformance pass against a QDMI reference/mock device in CI; a documented
integration test recipe for a real QDMI site (to execute at a partner — cannot be CI'd here).

**M10 — Second cloud driver + GPU simulators (weeks 21–23).** One EU vendor cloud driver
(IQM Resonance or Pasqal Cloud — pick by credential availability, record decision) plus a
CUDA-Q/cuQuantum simulator adapter exposing GPU-backed simulator Targets.
*Accept:* conformance passes for both; the compose demo gains an optional GPU profile;
fleet with 3 replay + 1 cloud + 1 GPU target schedules a mixed workload in the e2e suite.

**M11 — Console v0, read-only (weeks 21–24).** Fleet view (targets, live calibration state
with provenance), queue/job explorer, and the placement-audit explorer (the "why did my job
land there" page — placement records rendered human-first); per-project usage view.
*Accept:* Playwright e2e against a seeded stack; proxy-asserted zero write calls; renders the
calibration age and methodology fields (provenance is UI, not just data); works served from
the single `rabi` binary.

**M12 — Pilot-readiness package (weeks 24–26).** Probe jobs as first-class system tenant
(known-output circuits per target on a schedule, feeding target health + estimator-error
metrics); Grafana dashboards shipped as code; security checklist (secrets handling, network
matrix, CVE scan gate in release CI); site install guide; `fleet-0` deploy scripts
(Terraform/cloud-init or plain compose-on-VM — boring option) so our own reference
deployment runs the same artifacts a pilot gets.
*Accept:* probes run on schedule in e2e with results visible in metrics + console; release
CI includes CVE gate (no HIGH/CRITICAL); a stranger-test of the install guide on a clean VM
reaches a working fleet in ≤ 60 minutes; fleet-0 scripts stand up a live instance end-to-end.

## 4. Non-goals — the agent MUST NOT build these in Phase 1

HA/clustering or leader election · federation/multi-control-plane · space-sharing /
multi-programming · circuit cutting · Pareto/RL scheduling policies (Phase 2 absorption
wave) · QEC bundle execution (spec placeholder only) · NVQLink/real-time integration ·
write-actions in the console · billing/invoicing UI · a CLA, a new license, or any
governance change (GOVERNANCE.md is frozen; changes are human-only) · any new IR, any new
datastore, any message broker.

## 5. Definition of done

M0–M12 accepted with all §3 suites green · every in-tree adapter certified by the extracted
harness with published reports · fleet-0 running the release artifacts · pilot install guide
stranger-tested · `v0.4.0` tagged and signed · decisions log current, with all SPEC QUESTIONS
either resolved by merged RFCs or explicitly parked for v0.3.
