# RFC-0003: Explicit deadline/quality-floor conflict policy (`scheduling.onConflict`)

- **Status:** Accepted (2026-07-18)
- **Author(s):** Edward (Levangie Laboratories), drafted with Claude
- **Created:** 2026-07-18
- **Affects:** `spec/quantumjob.md`, `schemas/quantumjob.schema.json`, `spec/overview.md` §3–4 (conditions), scheduler conformance + goldens

## Summary

When a job declares both a quality floor and a deadline, they can become unsatisfiable
together: no target meets the floor before the deadline. The v0 scheduler silently favors
the floor — it holds the job for a calibration event and lets the deadline slip. This RFC
makes the resolution an explicit, user-declared choice: `spec.scheduling.onConflict:
prefer-quality | prefer-deadline | reject`, default `prefer-quality` (current behavior),
with the resolution recorded in the placement audit.

## Motivation

Benchmark evidence (Artifact B): `calib-aware/v0` achieved zero quality-SLO violations by
deliberately holding floor-carrying jobs (median wait 0, mean 719 s of *deliberate* waiting)
— at the cost of a **97.0% deadline-met rate** vs 100% for baselines. The trade-off is
defensible; making it *silently* on the user's behalf is not, for a scheduler whose core
promise is that placements are arguable. The two constraints are both user intent; when they
conflict, the user owns the priority.

## Design

New optional block in `QuantumJob.spec` (additive):

```yaml
spec:
  scheduling:
    onConflict: prefer-quality    # prefer-quality | prefer-deadline | reject; default prefer-quality
```

Normative semantics, evaluated whenever the scheduler predicts (or observes) that the floor
and the deadline cannot both be satisfied:

- **`prefer-quality`** (default; v0 behavior): the job MAY wait past its deadline for floor
  feasibility. If the deadline passes while waiting, the job continues waiting; `status`
  gains condition `DeadlineExceededWaitingForQuality` at the moment the deadline passes.
- **`prefer-deadline`**: at the *decision horizon* — the latest placement time that still
  meets the deadline, computed from predicted wait + execution — the scheduler binds to the
  best-ESP target among those feasible **ignoring quality floors**, and `status.placement`
  records `floorsRelaxed: true` with the violated floor(s) and the actual values. The job
  never silently violates floors: the violation is explicit, attributed, and auditable.
- **`reject`**: at the same decision horizon, the job transitions to `FAILED` with
  `ErrorDetail.category = CAPABILITY_MISMATCH`, `retriable = true`, and condition
  `UnsatisfiableBeforeDeadline` naming the binding constraint. (Admission-time detection —
  no target could *ever* satisfy the floor — already leaves the job `PENDING` with a
  condition; this RFC does not change admission.)

Jobs with only one of floor/deadline are unaffected. Sessions inherit the setting from the
job that opened them. The placement audit record MUST name the mode in effect and, for
`prefer-deadline`, the relaxation details.

## Compatibility

Additive schema block; default reproduces v0 behavior byte-identically (guarded by the
golden placement suite). No proto changes; `floorsRelaxed` and the two conditions are new
`status` fields (control-plane-written, additive). Benchmark harness gains an
`onConflict`-mix workload knob; published benchmark defaults remain `prefer-quality` so
historical results stay comparable.

## Alternatives considered

**Automatic minimal relaxation** (loosen the floor just enough to meet the deadline).
Rejected: turns a hard constraint into a soft one without consent — the floor stops meaning
anything. **Deadline as hard timeout (auto-cancel).** Rejected as default: destroys work the
user may still want; available in spirit via `reject`. **Scheduler-level (site) default
instead of per-job.** Deferred: a site-policy override belongs in the tenancy-policy layer
(v0.3); per-job intent is the primitive and must exist first.

## Conformance impact

Scheduler DES tests, one per mode, on a crafted replay where the conflict provably occurs:
`prefer-quality` reproduces the v0 golden byte-identically; `prefer-deadline` binds by the
horizon with `floorsRelaxed` recorded and correct floor/value details; `reject` fails with
the specified category/condition and `retriable=true`. Admission tests: unknown `onConflict`
value rejected. Audit-record schema extended and validated.

## Unresolved questions

Whether the *decision horizon* computation (predicted wait + execution estimate, both
uncertain) needs a normative safety margin, or remains implementation-defined with the
requirement that it be recorded in the audit — this draft chooses recorded-but-
implementation-defined; revisit with pilot data.
