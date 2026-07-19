# Rabi documentation

Rabi is a control plane for quantum compute fleets: declare a QuantumJob —
what to run, how good the result must be, by when, at what cost — and Rabi
places it across a heterogeneous fleet using each device's live calibration,
recording a human-readable reason for every placement.

## Learn & use

- **[Concepts](concepts.md)** — start here. The mental model: calibration-aware
  placement, the QuantumJob, quality floors, sessions, the job lifecycle.
- **[Quickstart](quickstart.md)** — clone to routed jobs in five commands.
- **[QuantumJob reference](quantumjob-reference.md)** — every job field, keyed
  to the schema.
- **[qctl reference](qctl-reference.md)** — the CLI, command by command.
- **[API guide](api-guide.md)** — the REST / gRPC surface for tools and SDKs.

## Operate a deployment

- **[Site install guide](site-install-guide.md)** — clean machine to a working
  fleet in ≤ 60 minutes (compose or Helm).
- **[Security checklist](security-checklist.md)** — secrets, network matrix,
  release integrity.
- **[Backup & restore](backup-restore.md)** — the runbook and drill.

## Extend it

- **[Conformance for driver authors](conformance-authors.md)** — certify a new
  adapter for any vendor.
- **[QDMI site recipe](qdmi-site-recipe.md)** — integrate a real QDMI device.

## Understand the build

- **[Architecture](architecture.md)** — how the control plane is put together.
- **[Decisions log](decisions.md)** — every non-obvious choice and why (D-001…).

## Brand

- **[Brand assets](brand/README.md)** — the Rabi-oscillation mark and palette.
