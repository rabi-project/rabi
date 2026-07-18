# Certifying a Tangle adapter (driver authors' guide)

Any process that serves `tangle.adapter.v1alpha1.AdapterService` over gRPC
can be certified — language and runtime are your choice. Certification is
how a driver earns its place in a fleet: **declaring a capability obligates
passing its tests; declaring nothing is always legal.**

## Run the harness

```sh
go run github.com/rabi-project/rabi/cmd/rabi-conformance@latest \
    run --target localhost:50051
```

(or from a checkout: `./hack/conformance-report.sh` certifies the in-tree
adapters in one command.)

The harness dials your adapter, reads `GetCapabilities`, and runs every
applicable category:

| # | Category | What it proves |
|---|---|---|
| 1 | capability-honesty | declared formats/limits/technology are real (RFC-0001: `technology` must be set) |
| 2 | state-machine | task states move only along the spec FSM |
| 3 | idempotency | same idempotency key → same task, no double execution |
| 4 | cancellation | declared cancellation actually stops queued/running work |
| 5 | provenance | calibration snapshots carry id, timestamps, per-metric methodology |
| 6 | usage | terminal tasks report native-unit usage in declared billing units |
| 7 | error-taxonomy | induced failures map to `ErrorDetail` categories, never bare strings |
| 8 | sessions | declared sessions: open/submit/close honored, dead sessions are `SESSION_LOST` (undeclared: `OpenSession` returns `Unimplemented`) |

## The report

`--out` (default `conformance-report/`) receives:

- `report.json` — the versioned certification document (harness + spec
  version, capabilities, per-category results). This is the signed artifact.
- `report.sig` — ed25519 signature over `report.json`. Pass `--key` with a
  PKCS#8 PEM key to sign with your organization's key; without it an
  ephemeral key signs and its public half is written as `report.pub`.
- `report.md` — the same content, human-first.

Use `--note` to record run context (e.g. "fake-backend mode") — notes are
visible in the report and never soften failures.

## Self-test

The harness certifies itself in CI: intentionally broken adapter fixtures
(declared-but-refused sessions, ignored shot limits) must fail exactly the
right categories. If you suspect the harness, run
`go test ./conformance/ -run TestSelfTest`.

## Ground rules

- The harness submits real (tiny) programs; point it at hardware only when
  you accept the cost. `rabi.sim/delay-ms` is honored by simulators to make
  cancellation tests deterministic; hardware adapters may ignore it.
- Reports from fake/recorded backends are development artifacts; pilot
  certification uses a live run (nightly in this repo, token-gated).
