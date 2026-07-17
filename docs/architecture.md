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
| M0 scaffold | **current** |
| M1–M8 | pending |
