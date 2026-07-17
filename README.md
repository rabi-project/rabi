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

![demo](docs/demo.gif)

**Five-minute demo:** `make compose-up && ./deploy/compose/seed.sh` starts a
control plane managing three simulated QPUs that replay **real IBM device
calibration** (drifting at 600× wall time) and routes a 20-job mix across
them by live calibration quality — watch it with `qctl watch --all`.
**The number:** `make bench` reproduces our benchmark of calibration-aware
placement against static best-device selection — real calibration baselines,
seeded synthetic drift, exact simulator ground truth, byte-identical reruns.

**Status:** pre-v0.1, building toward the MVP milestones in
`spec/mvp-build-plan.md`.

- [Quickstart](docs/quickstart.md) — clone to routed jobs in 5 commands
- [Architecture](docs/architecture.md)
- [Decisions log](docs/decisions.md)
- Spec (vendored, read-only): [`spec/`](spec/)

## License

Apache-2.0. See [LICENSE](LICENSE) and [CONTRIBUTING.md](CONTRIBUTING.md) (DCO).
