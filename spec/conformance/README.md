# Tangle Conformance (skeleton)

The conformance suite is what makes Tangle a standard rather than a codebase. It certifies
**adapters** (and later, independent control-plane implementations) against the spec. Harness
implementation lives in `tangle-conformance`; this document is the normative list of what is tested.

## Adapter conformance categories (v0.1 targets)

1. **Capability honesty.** Everything declared in `Capabilities` must be demonstrable:
   declared program formats accept valid programs and reject invalid ones with
   `INVALID_PROGRAM`; declared `max_shots` is enforced; undeclared capabilities
   (sessions, cancellation) are never exercised against you.
2. **State-machine correctness.** Task states only move forward
   (QUEUED→RUNNING→terminal); terminal states are immutable; `WatchTask` and polled
   `GetTask` agree; timestamps are monotonic.
3. **Idempotency.** Re-submission with the same `idempotency_key` returns the same task and
   MUST NOT duplicate execution or usage.
4. **Cancellation semantics** (if declared). Cancel on a QUEUED task prevents execution;
   cancel on RUNNING is best-effort but always converges to a terminal state; usage reflects
   reality either way.
5. **Provenance completeness.** Every `Metric` carries `measured_at`, `source`, and
   `methodology`; `snapshot_id` is stable for identical data and changes when data changes.
6. **Usage accuracy.** Usage records are present at terminal state, in declared
   `billing_units`, and are plausible (nonzero for executed work; zero for never-run cancels).
7. **Error taxonomy.** Induced failures (bad program, offline target, exceeded limits) map to
   the correct `ErrorDetail.category` with sane `retriable` flags — never bare vendor strings.
8. **Session semantics** (if declared). Tasks with a `session_id` execute with the promised
   affinity; expiry produces `SESSION_LOST`, not silent rescheduling.

## Certification policy (sketch — finalized by RFC before v1.0)

- Suite runs against a live adapter endpoint; results are a signed, versioned report.
- "**Tangle-conformant (spec vX.Y)**" mark: granted per adapter version, listed publicly,
  revocable for regressions. The mark gates *naming*, never usage (see GOVERNANCE.md).
- Simulators are first-class certification targets — the Aer reference adapter must pass
  100% before any hardware adapter is attempted (it is the harness's own control experiment).
