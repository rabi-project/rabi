<!-- SPDX-License-Identifier: Apache-2.0 -->
# Operator runbooks

The five pages an on-call operator is most likely to get, each with how to
confirm it, what usually causes it, and how to resolve it. Rabi is a single
control-plane process plus Postgres (per site); most incidents are one of these.

Health at a glance: the status page (`/status`) and `/metrics`
(`rabi_jobs_total{phase=...}`, probe fidelity/age). Liveness: `/healthz`.

---

## 1. Jobs stuck in PENDING

**Confirm.** `rabi_jobs_total{phase="PENDING"}` is high and not falling; affected
jobs carry a `Schedulable=False` condition (`qctl get job <id>`).

**Usual causes & fixes.**
- **No feasible target.** The condition message names the failed dimension
  (qubits, technology, quality floor, calibration age). Either the fleet lacks a
  matching device or every device is filtered out. Confirm targets are online
  (`qctl get targets`); if a quality floor is unmeetable, the job waits by design
  until calibration improves.
- **Adapter offline.** See runbook 2 — a PENDING backlog with all targets
  `OFFLINE` is an adapter problem, not a scheduler one.
- **Scheduler not cycling.** Check the process logs for the dispatcher loop; if
  the process is up but not binding, restart it (runbook 3 covers the DB case).

## 2. A target/adapter is offline or unreachable

**Confirm.** `qctl get targets` shows the target `OFFLINE`; logs show
`watch task failed` / `adapter for site … is not configured`.

**Usual causes & fixes.**
- **Adapter process down.** Restart the adapter container
  (`docker compose … up -d adapter-<site>`). The registry re-discovers it within
  a poll interval; in-flight tasks resume automatically (the dispatcher
  re-attaches on the next successful watch).
- **Credential/endpoint change (cloud vendor).** Verify the adapter's env
  (token, instance CRN). A 401/403 in adapter logs is a credential issue.
- **Nothing lost.** Jobs already bound stay SUBMITTED/RUNNING and complete once
  the adapter returns; the control plane does not drop them.

## 3. Database unavailable / Postgres down

**Confirm.** `/healthz` fails; logs show `opening store` / connection errors.

**Usual causes & fixes.**
- **Postgres container down.** `docker compose … up -d postgres`; rabi retries
  the connection on startup and reconnects.
- **Disk full.** Check volume usage. The append-only ledgers grow; provision
  headroom. Never manually `DELETE` from `usage_ledger`/`audit_log`/`job_events`
  — the serving role cannot, and neither should an operator.
- **After recovery**, run the reconciliation audit and check `/status` shows a
  clean reconciliation.

## 4. Queue growing / scheduler not draining

**Confirm.** `rabi_jobs_total{phase="PENDING"}` climbs without bound while
targets are online.

**Usual causes & fixes.**
- **Arrival outruns capacity.** Expected under a storm; the queue is bounded and
  drains once arrivals slow (see the load harness, P2.M2). If it never drains,
  the fleet is under-provisioned for the workload — add targets.
- **All head-of-line jobs infeasible.** A batch of unschedulable jobs at the
  front can starve schedulable ones behind them; inspect the oldest PENDING jobs'
  conditions and cancel or fix the infeasible ones.
- **Regressed scheduler.** If a recent deploy changed placement, compare against
  the golden decisions and the shadow report; roll back per the upgrade runbook.

## 5. Reconciliation mismatch (usage/billing discrepancy)

**Confirm.** `/status` shows a non-clean reconciliation, or logs:
`reconciliation found mismatches — investigate before billing`.

**Usual causes & fixes.**
- **Do not bill** until resolved. The audit compares recorded usage against
  task-derived expectations; a mismatch means a usage row is missing or
  duplicated.
- Inspect `usage_ledger` for the flagged tasks (the audit logs task ids). The
  `UNIQUE (task_id, unit)` constraint prevents double-billing at write time, so a
  mismatch is usually a *missing* record (an adapter that reported usage after a
  crash) — reconcile against the adapter's own accounting.
- The ledger is append-only; corrections are new compensating rows with an audit
  note, never edits.

---

See also: `docs/fleet0/upgrade.md` (upgrade & rollback), `docs/ops-calendar.md`
(drill schedule), `SECURITY.md` (vulnerability response).
