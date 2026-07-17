# Tangle

[![ci](https://github.com/tangle-dev/tangle/actions/workflows/ci.yml/badge.svg)](https://github.com/tangle-dev/tangle/actions/workflows/ci.yml)

Tangle is an open-source control plane for quantum compute fleets. You declare
a `QuantumJob` — what to run, how good the result must be, by when, and at what
cost — and Tangle places it across a heterogeneous fleet of QPUs, simulators,
and vendor cloud queues, using each device's *current calibration* to decide
where the job will actually succeed. Every placement is recorded with a
human-readable reason, so scheduling is arguable instead of magic.

Under the hood: one control-plane binary (`tangled`) backed by PostgreSQL, a
gRPC adapter protocol any vendor can implement out of process, a `qctl` CLI,
and a calibration-replay simulator fleet that reproduces real device drift
offline — the same machinery behind our public benchmark of calibration-aware
placement against today's static device selection.

**Status:** pre-v0.1, building toward the MVP milestones in
`spec/mvp-build-plan.md`. Current milestone: **M0 — scaffold**.

- [Quickstart](docs/quickstart.md) — clone to running stack in 4 commands
- [Architecture](docs/architecture.md)
- [Decisions log](docs/decisions.md)
- Spec (vendored, read-only): [`spec/`](spec/)

## License

Apache-2.0. See [LICENSE](LICENSE) and [CONTRIBUTING.md](CONTRIBUTING.md) (DCO).
