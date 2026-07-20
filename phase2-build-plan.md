# Rabi Phase 2 — Agent-Executable Build Plan (Production Era)
### v1.0 · July 2026 · Hand to the agent with the repo (≥ v0.4.2) and `test-and-verification-plan.md`.

---

## Kickoff prompt (paste to the agent)

> You are building Phase 2 of Rabi: production hardening — the E4 rung of the evidence
> ladder. Read `phase2-build-plan.md` completely before writing code. This plan has two
> waves. **Wave A (M1–M8) is dispatchable now** — it is deliberately pilot-independent.
> **Wave B (M9–M13) is gated**: do not start any Wave B milestone until the file
> `docs/gates/pilot-gate.md` exists on main with every required field filled in and a human
> sign-off line — that file records facts about a real pilot site that Wave B decisions
> depend on. If you finish Wave A and the gate file does not exist, STOP and report; do not
> improvise Wave B. The vendored spec remains law; new SPEC QUESTIONS go to
> `docs/decisions.md`, never into improvised behavior. Milestone discipline is unchanged:
> acceptance criteria plus the §4 suites from `test-and-verification-plan.md`, one milestone
> at a time, decisions logged (D-049 onward).

## 1. Mission

Phase 1 made Rabi operable by one friendly site. Phase 2 makes it **trustworthy under
failure, load, attack, upgrade, and time** — and builds the machinery that turns scheduling
improvements from claims into promotions. The split principle: Wave A contains everything
whose design no pilot would change (harnesses, drills, security process, policy plugins,
ops formalization). Wave B contains everything whose design a pilot *shapes* (HA topology,
multi-site trust, compliance surfaces, the v1.0 freeze). Building Wave B on guesses is the
documented failure mode of this field's prior art; the gate file exists so we cannot.

## 2. Hard constraints (Phase 0/1 constraints remain; additions)

- **No HA-topology decisions in Wave A.** Nothing in Wave A may assume leader election,
  multiple control-plane replicas, or an external HA Postgres — that is Wave B, informed by
  the gate file.
- **Chaos tooling is dual-target:** every scenario runs against the compose stack in CI
  *and* has a `--fleet0` game-day mode requiring an explicit `--i-mean-it` confirmation and
  a maintenance-window annotation on the status page.
- **Absorbed policies are guests:** implementations of published algorithms carry
  attribution in code, docs, and release notes; they ship **shadow-only** and cannot become
  default without the M5 promotion pipeline's evidence. Changing the default policy requires
  a human-approved PR labeled `policy-promotion`.
- **No new infrastructure.** Postgres remains the only datastore; the status page is static
  files rendered by the control plane; no message brokers, no external cron.
- **Release integrity:** every release ships SBOM + provenance attestation; fuzz corpora are
  committed; the CVE gate stays blocking.
- **Fleet-0 is production.** Anything that touches it goes through the upgrade path, never
  ad-hoc SSH mutation; drills are scheduled and status-page-annotated.

## 3. Wave A — dispatch now (≈ 10–12 weeks)

**P2.M1 — Chaos & invariants harness (weeks 1–3).** A scenario runner with the eight
scenarios from test-plan §4: adapter killed mid-RUNNING · control-plane↔adapter partition
(5 min) · Postgres restart mid-bind · duplicate LISTEN/NOTIFY delivery · replay-clock skew ·
garbage protobuf from an adapter · 10× latency on a vendor cassette · disk-full on ledger
write. After every scenario, the invariant suite asserts: every accepted job reaches a
queryable state; no duplicate execution (idempotency ledger); terminal states immutable;
usage within caps + tolerance; audit trail gapless.
*Accept:* all eight scenarios green in CI weekly; one supervised `--fleet0` game-day executed
with invariants green and the drill visible as an annotation on the status page.

**P2.M2 — Load & soak automation (weeks 2–4).** Storm harness: 10,000 pending jobs across
100 synthetic targets on a CI runner (and a 1,000-job variant sized for fleet-0), measuring
scheduler-cycle p99 (< 2 s), API read p99 (< 300 ms), write p99 (< 1 s), and queue-growth
boundedness. Soak harness: 72 h accelerated-replay run with RSS-growth (< 5%/24 h
post-warmup), goroutine-bound, and stuck-job (zero non-terminal older than policy max)
checks.
*Accept:* storm + soak green on schedule (weekly storm, monthly soak) with results published
as CI artifacts; a failing threshold blocks release tags.

**P2.M3 — Upgrade & migration hardening (weeks 3–5).** Golden databases captured from every
released tag (v0.1.0 → current); forward-migration tests from each golden green in CI; an
automated zero-downtime upgrade rehearsal (N-1 → N under live synthetic workload: zero jobs
failed attributable to upgrade, API unavailability < 30 s) and a scripted rollback
rehearsal.
*Accept:* migration matrix green; upgrade + rollback rehearsals run in CI weekly and pass;
fleet-0's own upgrades switch to the rehearsed path.

**P2.M4 — Security wave (weeks 4–7).** Fuzz harnesses for every payload parser (OpenQASM
ingestion, schema admission, adapter result decoding): ≥ 1M executions each, zero
crashes/hangs, corpora committed. SBOM (SPDX) + SLSA-style provenance attestation attached
to releases. Log-scan test proving secrets never appear in logs. `SECURITY.md` with a
disclosure process and response-time commitment. Mutation testing on `internal/scheduler` +
the FSM: score ≥ 65%, enforced quarterly in CI.
*Accept:* all of the above in CI; a planted secret in a test fixture is caught by the scan;
a planted logic mutant is killed by the suite.

**P2.M5 — Shadow scheduling & policy promotion (weeks 6–8).** The machinery that lets
scheduling evolve safely: candidate policies run in shadow on fleet-0 and on the replay
benchmark (placements computed and recorded, never executed); a comparison report
(fidelity-proxy delta, SLO delta, wait delta with CIs) generated continuously;
benchmark-as-regression wired into release CI (a > 5% regression on any headline metric
blocks the tag unless the release notes carry an RFC-referenced justification).
*Accept:* shadow reports visible for at least one candidate policy over ≥ 2 weeks of
fleet-0 operation; a deliberately-worse test policy is correctly *not* promotable; release
CI demonstrably blocks on a planted regression.

**P2.M6 — Absorption wave 1 (weeks 7–10).** Two attributed policy plugins from the
literature: a Pareto multi-objective policy (completion-time vs fidelity, NSGA-II-style —
Qonductor lineage) and an adaptive-deferral policy (calibration-window awareness — Ravi et
al. lineage). Both registered behind the standard interface, both shadow-only, both
documented with citations and a "differences from the paper" section.
*Accept:* both pass the policy conformance goldens; shadow reports comparing each against
`calib-aware/v0` published; attribution present in code headers, docs, and release notes.
*(Human hook, not agent work: these are the artifacts for recruiting the papers' authors as
maintainers — flag when merged.)*

**P2.M7 — Ops formalization & public status page (weeks 8–11).** A public, static status
page served by fleet-0: uptime, days-since-a-job-was-lost, probe success rates, estimator
calibration error (with the probe-circuit caveat printed honestly), last game-day date and
result. Game-day calendar (monthly), backup→restore drill (monthly, scripted, results
published), runbooks for the five most likely operator pages.
*Accept:* status page live at fleet-0's address; two consecutive scheduled drills executed
and published; a stranger can answer "is Rabi healthy and how do you know?" from the page
alone.

**P2.M8 — Hybrid workflow example (weeks 10–12).** `examples/hybrid-workflow/`: a runnable
pipeline in two variants — Kubernetes (Argo or plain Jobs) and Slurm batch — where classical
pre/post stages run natively and the quantum stage is a `QuantumJob` via SDK/CLI, results
flowing back. README explains the boundary ("Rabi schedules the machine that drifts;
your scheduler runs everything else") — this is the canonical answer to "can it manage
CPU/GPU."
*Accept:* both variants run end-to-end in CI (kind for K8s; a containerized Slurm for
batch); docs page published; linked from the landing page.

**P2.M8+ — Sanctioned overflow (ONLY if Wave A completes before the pilot gate file
exists): HA-readiness mechanics.** The pilot-independent *mechanics* of high availability —
replica-safe binding (prove two schedulers cannot double-bind under concurrency; the
row-locked binder should already guarantee this — turn it into a test), leader election
behind a feature flag (Postgres advisory-lock based; no new infrastructure), health/readiness
endpoints, and a failover drill in the compose stack (kill the leader, standby takes over,
invariants hold, RTO measured). Explicitly OUT of scope even here: deployment topology,
managed-vs-local Postgres posture, and site-shaped failover expectations — those consume
pilot-gate fields and remain Wave B (M9 finalizes them).
*Accept:* double-bind impossibility test green under 100-way concurrency; compose failover
drill green with RTO recorded; the flag defaults OFF.

## 4. Wave B — GATED on `docs/gates/pilot-gate.md` (do not start without it)

The gate file must record, signed by Edward: pilot site name & contact; infrastructure shape
(K8s cluster / VMs / HPC login-node pattern; managed vs local Postgres); network topology
(egress rules, proxy, air-gap level); identity provider; expected user count & workload mix;
compliance/SIEM regime; agreed pilot SLOs; hardware entitlements (Direct Access? QDMI
device?). Each Wave B milestone lists the fields it consumes.

**P2.M9 — HA control plane.** Active/standby via Postgres leader election (no consensus
zoo), health-checked failover RTO < 60 s / RPO = 0, deployment topology chosen to match the
gate file's infrastructure shape. *(Consumes: infra shape, DB posture, SLOs.)*

**P2.M10 — Multi-site agent & credential isolation.** The optional `qlet` gateway becomes
real: outbound-only site agents with scoped secrets, per-site data residency rules.
*(Consumes: network topology, air-gap level, secret-management stack.)*

**P2.M11 — Enterprise audit & LTS.** SIEM-exportable audit stream in the pilot's format;
LTS branch policy and support窗 windows. *(Consumes: compliance regime.)*

**P2.M12 — External pen-test & go-live hardening.** Commissioned firm, scope from the gate
file; findings triaged to fix/accept with public summary. *(Precedes pilot go-live.)*

**P2.M13 — Spec v1.0 freeze + conformance program launch.** Requires: the v0.3 RFC batch
settled (including the wire-identifier decision — the last breaking window), ≥ 2 external
RFC participants, Wave B experience folded in. Launch = versioned certification policy,
public registry, signed reports.

## 5. Non-goals (Phase 3 territory — the agent MUST NOT build)

Federation/multi-control-plane · bundle co-scheduling implementation (design RFC only, and
that's human-track) · space-sharing/multi-programming · circuit cutting · `ClassicalJob` or
any general classical scheduling · the wire-identifier rename (RFC decision first) · QEC
logical-modality execution · NVQLink integration.

## 6. Definition of done

**Wave A:** M1–M8 accepted, all §4 suites scheduled and green, status page public, two
drills published, shadow pipeline live with one candidate evaluated — tag `v0.5.0`.
**Wave B:** M9–M13 accepted against a real pilot's gate file, pen-test summary published,
spec `v1.0-rc` tagged with the conformance program live. Phase 2 closes when the pilot's
six-week SLO window completes: availability ≥ 99.5%, placement p99 < 1 s, zero lost jobs.
