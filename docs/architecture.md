# Architecture

See `spec/spec/overview.md` for the normative four-plane picture. This page
describes how this repository implements it; it grows with each milestone.

## The one binary: `tangled`

`cmd/tangled` contains the entire control plane — API server, scheduler,
target registry, and accounting. PostgreSQL 15 is the only stateful
dependency: job dispatch uses `FOR UPDATE SKIP LOCKED` work queues with
`LISTEN/NOTIFY` wakeups. There is no message broker and no other service.

```
qctl / SDK / REST ──> internal/api (gRPC tangle.api.v1alpha1 + gateway)
                          │
                      internal/store (Postgres: jobs, placements, ledger)
                          │
        internal/scheduler (filter → score → bind)   [M3+]
                          │
        internal/registry (adapter dialing, capability & state cache)
                          │  gRPC tangle.adapter.v1alpha1
        adapters/* (separate processes: Aer replay fleet, IBM)  [M2+]
```

- **API** (`internal/api`): generated from the vendored spec protos; REST via
  grpc-gateway with bindings in `api-config.yaml`; one static API key.
- **Store** (`internal/store`): pgx pool + embedded goose migrations, applied
  automatically at startup.
- **Registry** (`internal/registry`): the fleet view. Empty at M0; from M2 it
  dials adapters, caches capabilities, and polls device state.
- **Scheduler** (`internal/scheduler`, M3+): policy pipeline
  filter → score → bind; policies register by name behind one interface so
  `static-best`, `round-robin`, and `calib-aware/v0` compare like with like.
- **Accounting** (`internal/accounting`, M2+): immutable native-unit ledger.

## Milestone state

| Milestone | State |
|---|---|
| M0 scaffold | done |
| M1 job store + API | done |
| M2 adapter protocol + Aer adapter | **current** |
| M3–M8 | pending |

## Execution path (M2)

The registry dials adapters from `TANGLE_ADAPTERS` (site=host:port), caches
capabilities, and polls device state every 5s. The dispatcher claims PENDING
jobs from the Postgres work queue (`FOR UPDATE SKIP LOCKED`, woken by
`LISTEN/NOTIFY` on submit), binds them with a placement audit record, submits
to the adapter with the task UUID as idempotency key, and mirrors task states
onto the job until terminal. Usage lands in an append-only ledger
(`UNIQUE (task_id, unit)` makes recording idempotent) served by
`GetTenantUsage` in native units. The reference Aer adapter builds its noise
model from the same snapshot `GetDeviceState` reports, and passes the
public conformance suite (`conformance/`) in CI — categories 1–8.

## Job lifecycle (M1)

Admission validates the submitted document against the spec schema plus the
normative semantic rules (deadline in future, known budget units, exactly one
modality payload, tenant envelope consistency); a program format the fleet
lacks is a warning condition, not a rejection. Accepted jobs persist in
Postgres as `PENDING` with an append-only `job_events` history; every phase
change goes through `internal/job.Transition` (the single state-machine
authority) inside a row lock. `WatchJob` replays the event history and tails
it, so watchers see every transition in order.
