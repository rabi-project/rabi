# rabi-spec

**The specification for Tangle: a vendor-neutral control plane for quantum compute fleets.**

This repository is the source of truth for the Tangle standard. It contains no implementation —
the reference implementation lives in [`tangle`](../tangle) and the conformance harness in
[`tangle-conformance`](../tangle-conformance) (built against this spec, in that order of authority).

Current spec version: **v0.2** (RFC-0001, RFC-0002, RFC-0003 accepted and merged 2026-07-18).

## Layout

| Path | Contents |
|---|---|
| `spec/overview.md` | Architecture, terminology, job lifecycle, error semantics |
| `spec/quantumjob.md` | The `QuantumJob` object: fields, validation, status |
| `schemas/quantumjob.schema.json` | Machine-readable JSON Schema for `QuantumJob` |
| `proto/tangle/adapter/v1alpha1/adapter.proto` | **The adapter protocol** — the contract every device driver implements |
| `proto/tangle/api/v1alpha1/jobs.proto` | Control-plane external API (client-facing) |
| `conformance/README.md` | Conformance test categories and certification policy (skeleton) |
| `rfcs/` | Design proposals; start from `0000-template.md` |
| `GOVERNANCE.md` | How this project is governed, and what we pre-commit to |

## Design principles (normative)

1. **Declarative jobs.** Users state requirements; the fleet decides placement. Naming a device is allowed, never required.
2. **Multi-modal workloads.** Gate-model, analog-Hamiltonian, annealing, pulse, and logical workloads are typed payloads under one object. Adding a modality must never break existing adapters.
3. **The protocol is the standard, not the language.** Adapters are out-of-process gRPC servers; any implementation language is first-class.
4. **Provenance or it didn't happen.** Every quality metric carries source, measurement time, and methodology. The spec never implies unlike vendor benchmarks are comparable.
5. **Native units end-to-end.** Billing/usage is reported in the provider's native units; normalization is an accounting-layer concern.
6. **Adopt, don't invent.** Program payloads are OpenQASM 3 / QIR / vendor-native formats declared by capability — this spec defines no IR.

## Versioning

The spec versions independently of any implementation (`v0.1`, `v0.2`, … `v1.0`).
Proto packages are versioned (`v1alpha1` → `v1beta1` → `v1`); breaking changes require a new package version and an RFC.

## Contributing

Changes to anything under `spec/`, `schemas/`, or `proto/` require an RFC (see `rfcs/0000-template.md`) —
open a PR adding your RFC, discussion happens on the PR. Sign-off (DCO) required on all commits.
License: Apache-2.0 (add `LICENSE` and SPDX headers on repo publication).
